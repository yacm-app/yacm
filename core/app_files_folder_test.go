// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (C) 2026 The YACM Authors. Additional terms under GPLv3 section 7: see NOTICE.md.

package core

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yacm-app/yacm/internal/fakedevice"
	"github.com/yacm-app/yacm/internal/store"
)

// sftpTestConn opens the files of a pretend Linux server with SFTP.
func sftpTestConn(t *testing.T) (*App, *xferEvents, FilesInfo, string) {
	t.Helper()
	a, x := fileTestApp(t)
	port, fs := startFakeDeviceWith(t, "srv1", func(s *fakedevice.Server) { s.SFTP = true })
	n, err := a.SaveNode(store.Node{Name: "srv1", Session: &store.Session{Protocol: store.ProtoSSH, Host: "127.0.0.1", Port: port, User: "admin", Auth: store.AuthPassword}},
		&SecretInput{Password: sp("pw")})
	if err != nil {
		t.Fatal(err)
	}
	info, err := a.FilesOpen(FilesOpenRequest{NodeID: n.ID})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.FilesClose(info.ID) })
	if info.Kind != "sftp" {
		t.Fatalf("kind %q", info.Kind)
	}
	return a, x, info, fs.Root
}

func TestFilesFolders(t *testing.T) {
	a, x, info, root := sftpTestConn(t)

	// A folder on the device with a subfolder.
	src := filepath.Join(root, "configs")
	os.MkdirAll(filepath.Join(src, "old"), 0o755)
	os.WriteFile(filepath.Join(src, "r1.cfg"), []byte("hostname r1\n"), 0o644)
	os.WriteFile(filepath.Join(src, "old", "r2.cfg"), bytes.Repeat([]byte("x"), 100000), 0o644)

	dst := filepath.Join(t.TempDir(), "configs")
	id, err := a.FilesDownloadFolder(info.ID, src, dst)
	if err != nil {
		t.Fatal(err)
	}
	ev := x.wait(t, id)
	if ev.State != "done" || ev.Files != 2 || ev.Done != 100012 || ev.Total != 100012 {
		t.Fatalf("download folder %+v", ev)
	}
	if b, _ := os.ReadFile(filepath.Join(dst, "old", "r2.cfg")); len(b) != 100000 {
		t.Fatal("nested file missing")
	}
	if b, _ := os.ReadFile(filepath.Join(dst, "r1.cfg")); string(b) != "hostname r1\n" {
		t.Fatal("file differs")
	}

	// And back to a new folder on the device.
	up := filepath.Join(root, "back")
	id, err = a.FilesUploadFolder(info.ID, dst, up)
	if err != nil {
		t.Fatal(err)
	}
	if ev := x.wait(t, id); ev.State != "done" || ev.Files != 2 {
		t.Fatalf("upload folder %+v", ev)
	}
	if b, _ := os.ReadFile(filepath.Join(up, "old", "r2.cfg")); len(b) != 100000 {
		t.Fatal("nested file not sent")
	}

	// Send with sudo starts with a folder of its own in /tmp.
	tmp, err := a.FilesTempDir(info.ID)
	if err != nil || !strings.HasPrefix(tmp, "/tmp/yacm-") {
		t.Fatalf("temp dir %q %v", tmp, err)
	}
	defer os.RemoveAll(tmp)
	if st, err := os.Stat(tmp); err != nil || st.Mode().Perm() != 0o700 {
		t.Fatalf("temp dir mode %v %v", st, err)
	}

	// A file is not a folder.
	if _, err := a.FilesUploadFolder(info.ID, filepath.Join(dst, "r1.cfg"), up); err == nil {
		t.Fatal("a file sent as a folder")
	}
}

func TestFilesResume(t *testing.T) {
	a, x, info, root := sftpTestConn(t)
	f, err := a.filesGet(info.ID)
	if err != nil {
		t.Fatal(err)
	}
	data := bytes.Repeat([]byte("0123456789"), 50000)
	remote := filepath.Join(root, "image.bin")
	os.WriteFile(remote, data, 0o644)

	// A download that stopped after 1000 bytes goes on from its .part
	// file: the marked start shows that it was not sent again.
	local := filepath.Join(t.TempDir(), "image.bin")
	os.WriteFile(local+".part", bytes.Repeat([]byte("A"), 1000), 0o644)
	a.keepResume(f, TransferEvent{ID: "d1", Name: "image.bin", Dir: "down", Local: local, Remote: remote}, false, local+".part")
	id, err := a.FilesResume("d1", info.ID)
	if err != nil {
		t.Fatal(err)
	}
	if ev := x.wait(t, id); ev.State != "done" || ev.Done != int64(len(data)) {
		t.Fatalf("resumed download %+v", ev)
	}
	got, _ := os.ReadFile(local)
	if len(got) != len(data) || !bytes.Equal(got[:1000], bytes.Repeat([]byte("A"), 1000)) || !bytes.Equal(got[1000:], data[1000:]) {
		t.Fatal("resumed download is not the .part plus the rest")
	}
	if _, err := a.FilesResume("d1", info.ID); err == nil {
		t.Fatal("resumed twice")
	}

	// An upload goes on from what the device has.
	src := filepath.Join(t.TempDir(), "up.bin")
	os.WriteFile(src, data, 0o644)
	dev := filepath.Join(root, "up.bin")
	os.WriteFile(dev, bytes.Repeat([]byte("B"), 2000), 0o644)
	a.keepResume(f, TransferEvent{ID: "u1", Name: "up.bin", Dir: "up", Local: src, Remote: dev}, false, "")
	id, err = a.FilesResume("u1", info.ID)
	if err != nil {
		t.Fatal(err)
	}
	if ev := x.wait(t, id); ev.State != "done" || ev.Done != int64(len(data)) {
		t.Fatalf("resumed upload %+v", ev)
	}
	got, _ = os.ReadFile(dev)
	if len(got) != len(data) || !bytes.Equal(got[:2000], bytes.Repeat([]byte("B"), 2000)) || !bytes.Equal(got[2000:], data[2000:]) {
		t.Fatal("resumed upload is not the device part plus the rest")
	}

	// After Reconnect it goes on over the new connection to the session.
	info2, err := a.FilesOpen(FilesOpenRequest{NodeID: f.nodeID})
	if err != nil {
		t.Fatal(err)
	}
	defer a.FilesClose(info2.ID)
	local3 := filepath.Join(t.TempDir(), "image3.bin")
	os.WriteFile(local3+".part", data[:5000], 0o644)
	a.keepResume(f, TransferEvent{ID: "d3", Name: "image.bin", Dir: "down", Local: local3, Remote: remote}, false, local3+".part")
	a.FilesClose(info.ID)
	if _, err := a.FilesResume("d3", info.ID); err == nil {
		t.Fatal("resumed over a closed connection")
	}
	id, err = a.FilesResume("d3", info2.ID)
	if err != nil {
		t.Fatal(err)
	}
	if ev := x.wait(t, id); ev.State != "done" {
		t.Fatalf("resumed after reconnect %+v", ev)
	}
	if got, _ := os.ReadFile(local3); !bytes.Equal(got, data) {
		t.Fatal("resumed after reconnect differs")
	}
	// Not over a connection to another session.
	a.keepResume(&filesConn{nodeID: "other"}, TransferEvent{ID: "d4", Dir: "down", Local: local3, Remote: remote}, false, "")
	if _, err := a.FilesResume("d4", info2.ID); err == nil || !strings.Contains(err.Error(), "another session") {
		t.Fatalf("other session: %v", err)
	}
	f = a.files[info2.ID]

	// A stopped transfer that will not go on loses its .part file.
	part := filepath.Join(t.TempDir(), "x.bin.part")
	os.WriteFile(part, []byte("x"), 0o644)
	a.keepResume(f, TransferEvent{ID: "d2", Dir: "down", Local: strings.TrimSuffix(part, ".part"), Remote: remote}, false, part)
	a.FilesDiscard([]string{"d2"})
	if _, err := os.Stat(part); !os.IsNotExist(err) {
		t.Fatal(".part kept")
	}

	// A missing file is an error that cannot go on.
	os.Remove(remote)
	id, _ = a.FilesDownload(info2.ID, remote, filepath.Join(t.TempDir(), "gone.bin"), 0, false)
	if ev := x.wait(t, id); ev.State != "error" || ev.Resume {
		t.Fatalf("missing file %+v", ev)
	}
}

func TestFilesDenied(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes everywhere")
	}
	a, x, info, root := sftpTestConn(t)
	ro := filepath.Join(root, "etc")
	os.Mkdir(ro, 0o555)
	t.Cleanup(func() { os.Chmod(ro, 0o755) })
	src := filepath.Join(t.TempDir(), "a.conf")
	os.WriteFile(src, []byte("x"), 0o644)
	id, err := a.FilesUpload(info.ID, src, filepath.Join(ro, "a.conf"), false)
	if err != nil {
		t.Fatal(err)
	}
	if ev := x.wait(t, id); ev.State != "error" || !ev.Denied || ev.Resume {
		t.Fatalf("denied upload %+v", ev)
	}
}

func TestFilesFolderFromNetworkDevice(t *testing.T) {
	a, x := fileTestApp(t)
	port, fs := startFakeDevice(t, "R7")
	n, err := a.SaveNode(store.Node{Name: "R7", Session: &store.Session{Protocol: store.ProtoSSH, Host: "127.0.0.1", Port: port, User: "admin", Auth: store.AuthPassword}},
		&SecretInput{Password: sp("pw")})
	if err != nil {
		t.Fatal(err)
	}
	info, err := a.FilesOpen(FilesOpenRequest{NodeID: n.ID})
	if err != nil {
		t.Fatal(err)
	}
	defer a.FilesClose(info.ID)
	os.MkdirAll(filepath.Join(fs.Root, "configs"), 0o755)
	os.WriteFile(filepath.Join(fs.Root, "configs", "r7.cfg"), []byte("hostname R7\n"), 0o644)
	dst := filepath.Join(t.TempDir(), "configs")
	id, err := a.FilesDownloadFolder(info.ID, "flash:configs", dst)
	if err != nil {
		t.Fatal(err)
	}
	if ev := x.wait(t, id); ev.State != "done" || ev.Files != 1 {
		t.Fatalf("folder from flash: %+v", ev)
	}
	if b, _ := os.ReadFile(filepath.Join(dst, "r7.cfg")); string(b) != "hostname R7\n" {
		t.Fatalf("got %q", b)
	}
	if _, err := a.FilesUploadFolder(info.ID, dst, "flash:configs2"); err == nil {
		t.Fatal("folder sent over SCP")
	}
}
