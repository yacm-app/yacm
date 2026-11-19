// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (C) 2026 The YACM Authors. Additional terms under GPLv3 section 7: see NOTICE.md.

package fileserver

import (
	"bytes"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

// sftpClient starts the SCP server with cfg and logs in over SFTP.
func sftpClient(t *testing.T, m *Manager, cfg Config) (*sftp.Client, Status) {
	t.Helper()
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	cfg.Kind, cfg.Bind, cfg.Port = "scp", "127.0.0.1", port
	st, err := m.Start(cfg)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := ssh.Dial("tcp", "127.0.0.1:"+strconv.Itoa(port), &ssh.ClientConfig{User: st.User, Auth: []ssh.AuthMethod{ssh.Password(st.Password)}, HostKeyCallback: ssh.InsecureIgnoreHostKey()})
	if err != nil {
		t.Fatal(err)
	}
	c, err := sftp.NewClient(conn)
	if err != nil {
		conn.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close(); conn.Close() })
	return c, st
}

func waitEvent(ev *events, state string, ok func(*Event) bool) *Event {
	for i := 0; i < 200; i++ {
		if e := ev.last(state); e != nil && ok(e) {
			return e
		}
		time.Sleep(10 * time.Millisecond)
	}
	return nil
}

func TestSFTPServer(t *testing.T) {
	m, ev, dir := newManager(t)
	img := data(300000)
	os.WriteFile(filepath.Join(dir, "ios.bin"), img, 0o644)
	os.WriteFile(filepath.Join(dir, "cbr copy.txt"), []byte("spaces"), 0o644)
	os.WriteFile(filepath.Join(dir, "half.bin.part"), []byte("x"), 0o644)
	os.Mkdir(filepath.Join(dir, "sub"), 0o755)
	c, _ := sftpClient(t, m, Config{Folder: dir, AllowUpload: true})

	// Download, with the device-style name for a file with spaces.
	f, err := c.Open("/ios.bin")
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(f)
	f.Close()
	if err != nil || !bytes.Equal(got, img) {
		t.Fatalf("get: %v", err)
	}
	if e := waitEvent(ev, "done", func(e *Event) bool { return e.Kind == "sftp" && !e.Upload }); e == nil || e.File != "ios.bin" || e.Done != int64(len(img)) {
		t.Fatalf("download event %+v", e)
	}
	if f, err := c.Open("cbr_copy.txt"); err != nil {
		t.Fatalf("space alias: %v", err)
	} else {
		f.Close()
	}

	// Listing hides unfinished uploads.
	des, err := c.ReadDir("/")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, d := range des {
		names = append(names, d.Name())
	}
	if strings.Join(names, ",") != "cbr copy.txt,ios.bin,sub" {
		t.Fatalf("list: %v", names)
	}

	// Upload into a sub folder.
	up := data(123457)
	w, err := c.Create("sub/show_tech.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.ReadFrom(bytes.NewReader(up)); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "sub", "show_tech.txt")); !bytes.Equal(b, up) {
		t.Fatal("upload differs")
	}
	if _, err := os.Stat(filepath.Join(dir, "sub", "show_tech.txt.part")); err == nil {
		t.Fatal(".part left behind")
	}
	if e := waitEvent(ev, "done", func(e *Event) bool { return e.Kind == "sftp" && e.Upload }); e == nil || e.File != "sub/show_tech.txt" || e.Done != int64(len(up)) {
		t.Fatalf("upload event %+v", e)
	}
	// A client that sets times after the upload gets no error.
	if err := c.Chtimes("sub/show_tech.txt", time.Now(), time.Now()); err != nil {
		t.Fatalf("setstat: %v", err)
	}

	// Refused: replacing, .part names, folders, renaming, removing, leaving.
	if _, err := c.Create("sub/show_tech.txt"); err == nil || !strings.Contains(err.Error(), "exists") {
		t.Fatalf("overwrite: %v", err)
	}
	if _, err := c.Open("half.bin.part"); err == nil {
		t.Fatal("read a .part file")
	}
	if err := c.Mkdir("new"); err == nil {
		t.Fatal("made a folder")
	}
	if err := c.Rename("ios.bin", "x.bin"); err == nil {
		t.Fatal("renamed")
	}
	if err := c.Remove("ios.bin"); err == nil {
		t.Fatal("removed")
	}
	if w, err := c.Create("../escape.txt"); err == nil {
		w.Write([]byte("x"))
		w.Close()
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(dir), "escape.txt")); err == nil {
		t.Fatal("escaped the folder")
	}
	if _, err := os.Stat(filepath.Join(dir, "escape.txt")); err != nil {
		t.Fatalf("../ should stay in the folder: %v", err)
	}
}

func TestSFTPServerLimits(t *testing.T) {
	m, _, dir := newManager(t)
	os.WriteFile(filepath.Join(dir, "R1-confg"), []byte("old"), 0o644)

	// Uploads off.
	c, _ := sftpClient(t, m, Config{Folder: dir})
	if _, err := c.Create("new.txt"); err == nil || !strings.Contains(err.Error(), "uploads are off") {
		t.Fatalf("uploads off: %v", err)
	}

	// Too large: refused and nothing left behind.
	c, _ = sftpClient(t, m, Config{Folder: dir, AllowUpload: true, MaxUpload: 1000})
	w, err := c.Create("big.bin")
	if err != nil {
		t.Fatal(err)
	}
	_, werr := w.Write(data(5000))
	cerr := w.Close()
	if werr == nil && cerr == nil {
		t.Fatal("large upload accepted")
	}
	for _, n := range []string{"big.bin", "big.bin.part"} {
		if _, err := os.Stat(filepath.Join(dir, n)); err == nil {
			t.Fatalf("%s kept", n)
		}
	}

	// Replacing when allowed.
	c, _ = sftpClient(t, m, Config{Folder: dir, AllowUpload: true, Overwrite: true})
	w, err = c.Create("R1-confg")
	if err != nil {
		t.Fatal(err)
	}
	w.Write([]byte("new"))
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "R1-confg")); string(b) != "new" {
		t.Fatalf("replace: %q", b)
	}
}
