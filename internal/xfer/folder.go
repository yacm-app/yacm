// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (C) 2026 The YACM Authors. Additional terms under GPLv3 section 7: see NOTICE.md.

package xfer

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Folder does the file operations of a file server inside its shared
// folder through os.Root. Names are the full paths InRoot returns; InRoot
// keeps them inside the folder by their spelling, and Folder also refuses
// symbolic links (or other tricks) that lead outside it. Relative links
// that stay inside the folder work; absolute links are refused.
type Folder struct {
	dir  string
	root *os.Root
}

var errOutside = errors.New("outside the shared folder")

// OpenFolder opens dir for a server.
func OpenFolder(dir string) (*Folder, error) {
	r, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	return &Folder{dir: dir, root: r}, nil
}

// Close releases the folder.
func (r *Folder) Close() error { return r.root.Close() }

// rel turns a full path into one relative to the folder.
func (r *Folder) rel(name string) (string, error) {
	rel, err := filepath.Rel(r.dir, name)
	if err != nil || filepath.IsAbs(rel) || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", errOutside
	}
	return rel, nil
}

// Open opens a file for reading.
func (r *Folder) Open(name string) (*os.File, error) {
	rel, err := r.rel(name)
	if err != nil {
		return nil, err
	}
	return r.root.Open(rel)
}

// OpenFile opens a file like os.OpenFile.
func (r *Folder) OpenFile(name string, flag int, perm os.FileMode) (*os.File, error) {
	rel, err := r.rel(name)
	if err != nil {
		return nil, err
	}
	return r.root.OpenFile(rel, flag, perm)
}

// Create creates or truncates a file for writing.
func (r *Folder) Create(name string) (*os.File, error) {
	return r.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
}

// Stat describes a file, following links inside the folder.
func (r *Folder) Stat(name string) (os.FileInfo, error) {
	rel, err := r.rel(name)
	if err != nil {
		return nil, err
	}
	return r.root.Stat(rel)
}

// Remove removes a file or an empty folder.
func (r *Folder) Remove(name string) error {
	rel, err := r.rel(name)
	if err != nil {
		return err
	}
	return r.root.Remove(rel)
}

// Rename renames a file inside the folder.
func (r *Folder) Rename(oldname, newname string) error {
	o, err := r.rel(oldname)
	if err != nil {
		return err
	}
	n, err := r.rel(newname)
	if err != nil {
		return err
	}
	return r.root.Rename(o, n)
}

// ReadDir lists a folder.
func (r *Folder) ReadDir(name string) ([]fs.DirEntry, error) {
	rel, err := r.rel(name)
	if err != nil {
		return nil, err
	}
	return fs.ReadDir(r.root.FS(), filepath.ToSlash(rel))
}

// Mkdir creates a folder.
func (r *Folder) Mkdir(name string, perm os.FileMode) error {
	rel, err := r.rel(name)
	if err != nil {
		return err
	}
	return r.root.Mkdir(rel, perm)
}

// Files is what an SCPHandler opens, renames and removes files with: a
// *Folder for a file server, the os package when nil.
type Files interface {
	Open(name string) (*os.File, error)
	OpenFile(name string, flag int, perm os.FileMode) (*os.File, error)
	Remove(name string) error
	Rename(oldname, newname string) error
}

type osFiles struct{}

func (osFiles) Open(name string) (*os.File, error) { return os.Open(name) }
func (osFiles) OpenFile(name string, flag int, perm os.FileMode) (*os.File, error) {
	return os.OpenFile(name, flag, perm)
}
func (osFiles) Remove(name string) error             { return os.Remove(name) }
func (osFiles) Rename(oldname, newname string) error { return os.Rename(oldname, newname) }
