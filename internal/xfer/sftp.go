// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (C) 2026 The YACM Authors. Additional terms under GPLv3 section 7: see NOTICE.md.

package xfer

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"io"
	"os"
	"sort"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

type sftpRemote struct {
	c *sftp.Client
	s *ssh.Session // the channel the subsystem runs on
}

func (s *sftpRemote) Kind() string { return "sftp" }

// TempDir makes a new folder in /tmp that only this user can open.
func (s *sftpRemote) TempDir(context.Context) (string, error) {
	b := make([]byte, 6)
	rand.Read(b)
	p := "/tmp/yacm-" + hex.EncodeToString(b)
	if err := s.c.Mkdir(p); err != nil {
		return "", err
	}
	if err := s.c.Chmod(p, 0o700); err != nil {
		_ = s.c.RemoveDirectory(p)
		return "", err
	}
	return p, nil
}

func (s *sftpRemote) Home(context.Context) string {
	if wd, err := s.c.Getwd(); err == nil && wd != "" {
		return wd
	}
	return "/"
}

func toEntry(dir string, fi os.FileInfo) Entry {
	return Entry{Name: fi.Name(), Path: Join(dir, fi.Name()), Size: fi.Size(), Dir: fi.IsDir(), Mod: fi.ModTime()}
}

func (s *sftpRemote) List(_ context.Context, dir string) ([]Entry, error) {
	fis, err := s.c.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	out := make([]Entry, 0, len(fis))
	for _, fi := range fis {
		e := toEntry(dir, fi)
		if fi.Mode()&os.ModeSymlink != 0 {
			if t, err := s.c.Stat(e.Path); err == nil {
				e.Dir, e.Size = t.IsDir(), t.Size()
			}
		}
		out = append(out, e)
	}
	SortEntries(out)
	return out, nil
}

// SortEntries puts folders first, then sorts by name.
func SortEntries(es []Entry) {
	sort.Slice(es, func(i, j int) bool {
		if es[i].Dir != es[j].Dir {
			return es[i].Dir
		}
		return es[i].Name < es[j].Name
	})
}

func (s *sftpRemote) Stat(_ context.Context, p string) (Entry, error) {
	fi, err := s.c.Stat(p)
	if err != nil {
		return Entry{}, err
	}
	return toEntry(Parent(p), fi), nil
}

func (s *sftpRemote) Get(ctx context.Context, p string, w io.Writer, progress func(int64)) error {
	f, err := s.c.Open(p)
	if err != nil {
		return err
	}
	defer f.Close()
	cw := &countingWriter{w: ctxWriter{ctx, w}, tick: progress}
	_, err = f.WriteTo(cw)
	if progress != nil {
		progress(cw.n)
	}
	return err
}

func (s *sftpRemote) Put(ctx context.Context, p string, r io.Reader, _ int64, progress func(int64)) error {
	f, err := s.c.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_TRUNC)
	if err != nil {
		return err
	}
	cr := &countingReader{r: r, tick: progress, ctx: ctx}
	// ReadFrom sends requests in parallel only when it knows the size of
	// the reader, which countingReader hides: one request at a time made
	// uploads 25 times slower through jump hosts.
	_, err = f.ReadFromWithConcurrency(cr, 0)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if progress != nil {
		progress(cr.n)
	}
	return err
}

func (s *sftpRemote) GetFrom(ctx context.Context, p string, w io.Writer, off int64, progress func(int64)) error {
	f, err := s.c.Open(p)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Seek(off, io.SeekStart); err != nil {
		return err
	}
	tick := func(n int64) {
		if progress != nil {
			progress(off + n)
		}
	}
	cw := &countingWriter{w: ctxWriter{ctx, w}, tick: tick}
	_, err = f.WriteTo(cw)
	tick(cw.n)
	return err
}

func (s *sftpRemote) PutFrom(ctx context.Context, p string, r io.Reader, off, _ int64, progress func(int64)) error {
	f, err := s.c.OpenFile(p, os.O_WRONLY)
	if err != nil {
		return err
	}
	if _, err := f.Seek(off, io.SeekStart); err != nil {
		f.Close()
		return err
	}
	tick := func(n int64) {
		if progress != nil {
			progress(off + n)
		}
	}
	cr := &countingReader{r: r, tick: tick, ctx: ctx}
	_, err = f.ReadFromWithConcurrency(cr, 0)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	tick(cr.n)
	return err
}

func (s *sftpRemote) Remove(_ context.Context, p string) error {
	fi, err := s.c.Stat(p)
	if err == nil && fi.IsDir() {
		return s.c.RemoveDirectory(p)
	}
	return s.c.Remove(p)
}

func (s *sftpRemote) Mkdir(_ context.Context, p string) error     { return s.c.Mkdir(p) }
func (s *sftpRemote) Rename(_ context.Context, a, b string) error { return s.c.Rename(a, b) }

func (s *sftpRemote) Free(_ context.Context, p string) int64 {
	if st, err := s.c.StatVFS(p); err == nil {
		return int64(st.FreeSpace())
	}
	return -1
}

func (s *sftpRemote) Close() error {
	err := s.c.Close()
	if s.s != nil {
		_ = s.s.Close()
	}
	return err
}

func (s *sftpRemote) Roots(ctx context.Context) []Root {
	home := s.Home(ctx)
	out := []Root{{Path: home, Size: -1, Free: s.Free(ctx, home)}}
	if home != "/" {
		out = append(out, Root{Path: "/", Size: -1, Free: -1})
	}
	return out
}
