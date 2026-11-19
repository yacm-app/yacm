// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (C) 2026 The YACM Authors. Additional terms under GPLv3 section 7: see NOTICE.md.

package fileserver

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"

	"github.com/yacm-app/yacm/internal/xfer"
)

// linkedFolder makes a shared folder with symbolic links: "secret" and
// "out" lead outside it, "inside" to a file in it.
func linkedFolder(t *testing.T, dir string) (outside string) {
	t.Helper()
	outside = t.TempDir()
	os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("secret"), 0o644)
	os.WriteFile(filepath.Join(dir, "ios.bin"), data(5000), 0o644)
	for link, target := range map[string]string{
		"secret": filepath.Join(outside, "secret.txt"),
		"out":    outside,
		"inside": "ios.bin", // os.Root follows only relative links
	} {
		if err := os.Symlink(target, filepath.Join(dir, link)); err != nil {
			t.Skipf("symbolic links: %v", err)
		}
	}
	return outside
}

func onlySecret(t *testing.T, outside string) {
	t.Helper()
	des, _ := os.ReadDir(outside)
	if len(des) != 1 {
		t.Fatalf("a file was written outside the shared folder: %v", des)
	}
}

func TestLinksOutsideTheFolder(t *testing.T) {
	m, _, dir := newManager(t)
	outside := linkedFolder(t, dir)

	// TFTP
	st, err := m.Start(Config{Kind: "tftp", Folder: dir, Bind: "127.0.0.1", Port: freeUDP(t), AllowUpload: true})
	if err != nil {
		t.Fatal(err)
	}
	addr := "127.0.0.1:" + strconv.Itoa(st.Port)
	var buf bytes.Buffer
	if _, err := TFTPGet(addr, "secret", &buf, 0); err == nil || strings.Contains(buf.String(), "secret") {
		t.Fatalf("tftp read through a link: %v %q", err, buf.String())
	}
	buf.Reset()
	if n, err := TFTPGet(addr, "inside", &buf, 0); err != nil || n != 5000 {
		t.Fatalf("tftp link inside the folder: %d %v", n, err)
	}
	if _, err := TFTPPut(addr, "out/x.txt", strings.NewReader("x"), 1, 0); err == nil {
		t.Fatal("tftp wrote through a link")
	}
	onlySecret(t, outside)

	// HTTP
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	if _, err := m.Start(Config{Kind: "http", Folder: dir, Bind: "127.0.0.1", Port: port, AllowUpload: true, Listing: true}); err != nil {
		t.Fatal(err)
	}
	base := "http://127.0.0.1:" + strconv.Itoa(port)
	for _, p := range []string{"/secret", "/out/secret.txt", "/out/"} {
		resp, err := http.Get(base + p)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode == http.StatusOK || strings.Contains(string(b), "secret") {
			t.Fatalf("http %s: %s %q", p, resp.Status, b)
		}
	}
	if resp, err := http.Get(base + "/inside"); err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("http link inside the folder: %v %v", resp, err)
	} else {
		resp.Body.Close()
	}
	req, _ := http.NewRequest(http.MethodPut, base+"/out/x.txt", strings.NewReader("x"))
	if resp, err := http.DefaultClient.Do(req); err == nil && resp.StatusCode == http.StatusCreated {
		t.Fatal("http wrote through a link")
	}
	onlySecret(t, outside)

	// SCP
	ln, _ = net.Listen("tcp", "127.0.0.1:0")
	port = ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	sst, err := m.Start(Config{Kind: "scp", Folder: dir, Bind: "127.0.0.1", Port: port, AllowUpload: true})
	if err != nil {
		t.Fatal(err)
	}
	c, err := ssh.Dial("tcp", "127.0.0.1:"+strconv.Itoa(port), &ssh.ClientConfig{User: "yacm", Auth: []ssh.AuthMethod{ssh.Password(sst.Password)}, HostKeyCallback: ssh.InsecureIgnoreHostKey()})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	ctx := context.Background()
	buf.Reset()
	if err := xfer.SCPGet(ctx, c, "secret", &buf, nil); err == nil || strings.Contains(buf.String(), "secret") {
		t.Fatalf("scp read through a link: %v %q", err, buf.String())
	}
	buf.Reset()
	if err := xfer.SCPGet(ctx, c, "inside", &buf, nil); err != nil || buf.Len() != 5000 {
		t.Fatalf("scp link inside the folder: %v", err)
	}
	if err := xfer.SCPPut(ctx, c, "out/x.txt", strings.NewReader("x"), 1, nil); err == nil {
		t.Fatal("scp wrote through a link")
	}
	onlySecret(t, outside)
}

func TestLinksOutsideTheFolderSFTP(t *testing.T) {
	m, _, dir := newManager(t)
	outside := linkedFolder(t, dir)
	c, _ := sftpClient(t, m, Config{Folder: dir, AllowUpload: true})
	for _, p := range []string{"secret", "out/secret.txt", "/out/secret.txt"} {
		if f, err := c.Open(p); err == nil {
			b, _ := io.ReadAll(f)
			f.Close()
			if strings.Contains(string(b), "secret") {
				t.Fatalf("sftp read %s through a link", p)
			}
		}
	}
	if _, err := c.ReadDir("out"); err == nil {
		t.Fatal("sftp listed a folder through a link")
	}
	if f, err := c.Open("inside"); err != nil {
		t.Fatalf("sftp link inside the folder: %v", err)
	} else {
		f.Close()
	}
	if f, err := c.Create("out/x.txt"); err == nil {
		f.Write([]byte("x"))
		f.Close()
	}
	onlySecret(t, outside)
}
