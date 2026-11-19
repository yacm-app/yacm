// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (C) 2026 The YACM Authors. Additional terms under GPLv3 section 7: see NOTICE.md.

package fileserver

import (
	"errors"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/pkg/sftp"

	"github.com/yacm-app/yacm/internal/xfer"
)

// The SCP server also answers the sftp subsystem: same port, folder, login
// and rules. Devices read and write single files; folders cannot be made,
// and nothing can be renamed or removed.

var errReadOnly = errors.New("this server only copies files")

type sftpFS struct {
	cfg    Config
	remote string
	limit  int64
	lerr   error
}

func (s *SCP) serveSFTP(ch io.ReadWriteCloser, remote string) {
	limit, lerr := s.cfg.uploadLimit()
	fs := &sftpFS{cfg: s.cfg, remote: remote, limit: limit, lerr: lerr}
	srv := sftp.NewRequestServer(ch, sftp.Handlers{FileGet: fs, FilePut: fs, FileCmd: fs, FileList: fs})
	_ = srv.Serve()
	_ = srv.Close()
}

// full maps an SFTP path to a path in the shared folder.
func (f *sftpFS) full(p string) (string, error) {
	clean := path.Clean("/" + strings.ReplaceAll(p, `\`, "/"))
	if strings.HasSuffix(clean, ".part") {
		return "", os.ErrPermission
	}
	return filepath.Join(f.cfg.Folder, filepath.FromSlash(clean)), nil
}

func (f *sftpFS) Fileread(r *sftp.Request) (io.ReaderAt, error) {
	full, err := f.full(r.Filepath)
	if err != nil {
		return nil, err
	}
	full = xfer.SpaceAlias(full)
	st, err := f.cfg.files.Stat(full)
	if err != nil {
		return nil, os.ErrNotExist
	}
	if st.IsDir() {
		return nil, errors.New("this is a folder")
	}
	file, err := f.cfg.files.Open(full)
	if err != nil {
		return nil, err
	}
	rd := &sftpReader{f: file, fs: f, ev: Event{Kind: "sftp", Remote: f.remote, File: displayName(f.cfg.Folder, full), Total: st.Size(), State: "start"}}
	f.cfg.emit(rd.ev)
	return rd, nil
}

func (f *sftpFS) Filewrite(r *sftp.Request) (io.WriterAt, error) {
	if !f.cfg.AllowUpload {
		return nil, errors.New("uploads are off on this server")
	}
	if f.lerr != nil {
		return nil, f.lerr
	}
	full, err := f.full(r.Filepath)
	if err != nil {
		return nil, err
	}
	if st, err := f.cfg.files.Stat(full); err == nil {
		if st.IsDir() {
			return nil, errors.New("this is a folder")
		}
		if !f.cfg.Overwrite {
			return nil, errors.New("file already exists")
		}
	}
	tmp := full + ".part"
	file, err := f.cfg.files.Create(tmp)
	if err != nil {
		return nil, err
	}
	w := &sftpWriter{f: file, fs: f, full: full, tmp: tmp, ev: Event{Kind: "sftp", Remote: f.remote, File: displayName(f.cfg.Folder, full), Upload: true, State: "start"}}
	f.cfg.emit(w.ev)
	return w, nil
}

func (f *sftpFS) Filecmd(r *sftp.Request) error {
	if r.Method == "Setstat" {
		// Times and permissions a client sets after an upload: not kept,
		// and not an error.
		if _, err := f.full(r.Filepath); err != nil {
			return err
		}
		return nil
	}
	return errReadOnly
}

func (f *sftpFS) Filelist(r *sftp.Request) (sftp.ListerAt, error) {
	full, err := f.full(r.Filepath)
	if err != nil {
		return nil, err
	}
	switch r.Method {
	case "List":
		des, err := f.cfg.files.ReadDir(full)
		if err != nil {
			return nil, err
		}
		var list listerAt
		for _, d := range des {
			if strings.HasSuffix(d.Name(), ".part") {
				continue
			}
			if fi, err := f.cfg.files.Stat(filepath.Join(full, d.Name())); err == nil {
				list = append(list, named{fi, d.Name()})
			}
		}
		sort.Slice(list, func(i, j int) bool { return list[i].Name() < list[j].Name() })
		return list, nil
	case "Stat", "Lstat":
		st, err := f.cfg.files.Stat(xfer.SpaceAlias(full))
		if err != nil {
			return nil, os.ErrNotExist
		}
		return listerAt{st}, nil
	}
	return nil, errReadOnly
}

// named keeps a listed entry's own name when Stat followed a link.
type named struct {
	os.FileInfo
	name string
}

func (n named) Name() string { return n.name }

type listerAt []os.FileInfo

func (l listerAt) ListAt(out []os.FileInfo, off int64) (int, error) {
	if off >= int64(len(l)) {
		return 0, io.EOF
	}
	n := copy(out, l[off:])
	if n < len(out) || off+int64(n) >= int64(len(l)) {
		return n, io.EOF
	}
	return n, nil
}

// sftpReader reports how much of a file a device read.
type sftpReader struct {
	f  *os.File
	fs *sftpFS
	mu sync.Mutex
	ev Event
	n  int64 // furthest byte read
}

func (r *sftpReader) ReadAt(p []byte, off int64) (int, error) {
	n, err := r.f.ReadAt(p, off)
	r.mu.Lock()
	if end := off + int64(n); n > 0 && end > r.n {
		r.n = end
	}
	ev := r.ev
	ev.Done, ev.State = r.n, "progress"
	r.mu.Unlock()
	r.fs.cfg.emitProgress(ev)
	return n, err
}

func (r *sftpReader) Close() error {
	err := r.f.Close()
	r.mu.Lock()
	ev := r.ev
	ev.Done = r.n
	r.mu.Unlock()
	ev.State = "done"
	if ev.Done < ev.Total {
		ev.State = "checked"
	}
	r.fs.cfg.emit(ev)
	return err
}

// sftpWriter writes an upload to a .part file and puts it in place when
// the device closes it.
type sftpWriter struct {
	f         *os.File
	fs        *sftpFS
	full, tmp string
	mu        sync.Mutex
	ev        Event
	n         int64 // furthest byte written
	failed    error
}

func (w *sftpWriter) WriteAt(p []byte, off int64) (int, error) {
	if l := w.fs.limit; l > 0 && off+int64(len(p)) > l {
		err := tooLarge(l)
		w.TransferError(err)
		return 0, err
	}
	n, err := w.f.WriteAt(p, off)
	w.mu.Lock()
	if end := off + int64(n); n > 0 && end > w.n {
		w.n = end
	}
	ev := w.ev
	ev.Done, ev.State = w.n, "progress"
	w.mu.Unlock()
	if err != nil {
		w.TransferError(err)
		return n, err
	}
	w.fs.cfg.emitProgress(ev)
	return n, nil
}

// TransferError is told when the device goes away in the middle.
func (w *sftpWriter) TransferError(err error) {
	w.mu.Lock()
	if w.failed == nil {
		w.failed = err
	}
	w.mu.Unlock()
}

func (w *sftpWriter) Close() error {
	err := w.f.Close()
	w.mu.Lock()
	if err == nil {
		err = w.failed
	}
	ev := w.ev
	ev.Done, ev.Total = w.n, w.n
	w.mu.Unlock()
	files := w.fs.cfg.files
	if err == nil {
		_ = files.Remove(w.full)
		err = files.Rename(w.tmp, w.full)
	}
	if err != nil {
		_ = files.Remove(w.tmp)
		ev.State, ev.Error = "error", err.Error()
	} else {
		ev.State = "done"
	}
	w.fs.cfg.emit(ev)
	return err
}
