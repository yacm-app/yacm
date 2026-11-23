// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (C) 2026 The YACM Authors. Additional terms under GPLv3 section 7: see NOTICE.md.

// Package pwsource reads passwords from password managers for credential
// profiles: a KeePass database (.kdbx, read here) and Bitwarden (through
// its bw command line program). Nothing is written to them.
package pwsource

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/tobischo/gokeepasslib/v3"
)

// ---- KeePass --------------------------------------------------------------

// KeePass is an unlocked KeePass database.
type KeePass struct {
	path string
	db   *gokeepasslib.Database
}

// OpenKeePass opens and unlocks a .kdbx file with its master password and,
// if given, its key file.
func OpenKeePass(path, password, keyFile string) (*KeePass, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	db := gokeepasslib.NewDatabase()
	if keyFile != "" {
		if password == "" {
			db.Credentials, err = gokeepasslib.NewKeyCredentials(keyFile)
		} else {
			db.Credentials, err = gokeepasslib.NewPasswordAndKeyCredentials(password, keyFile)
		}
		if err != nil {
			return nil, fmt.Errorf("key file: %w", err)
		}
	} else {
		db.Credentials = gokeepasslib.NewPasswordCredentials(password)
	}
	if err := gokeepasslib.NewDecoder(f).Decode(db); err != nil {
		return nil, fmt.Errorf("the KeePass database could not be opened (wrong master password or key file?): %w", err)
	}
	if err := db.UnlockProtectedEntries(); err != nil {
		return nil, err
	}
	return &KeePass{path: path, db: db}, nil
}

// Path is the database file.
func (k *KeePass) Path() string { return k.path }

// Password returns the password of the entry ref: its UUID (32 hex digits,
// as KeePass shows it), its path of groups and title ("Network/Core/sw1",
// with or without the database's top group) or a title found once.
func (k *KeePass) Password(ref string) (string, error) {
	e, err := k.find(ref)
	if err != nil {
		return "", err
	}
	return e.GetPassword(), nil
}

func (k *KeePass) find(ref string) (*gokeepasslib.Entry, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil, errors.New("no KeePass entry given")
	}
	if k.db.Content == nil || k.db.Content.Root == nil {
		return nil, errors.New("the KeePass database is empty")
	}
	var want *gokeepasslib.UUID
	if b, err := hex.DecodeString(strings.ReplaceAll(ref, "-", "")); err == nil && len(b) == 16 {
		var u gokeepasslib.UUID
		copy(u[:], b)
		want = &u
	}
	parts := strings.Split(strings.Trim(ref, "/"), "/")
	var byTitle []*gokeepasslib.Entry
	var byPath *gokeepasslib.Entry
	var walk func(g *gokeepasslib.Group, path []string)
	walk = func(g *gokeepasslib.Group, path []string) {
		for i := range g.Entries {
			e := &g.Entries[i]
			if want != nil && e.UUID.Compare(*want) {
				byPath = e
			}
			title := e.GetTitle()
			if title == ref {
				byTitle = append(byTitle, e)
			}
			full := append(append([]string{}, path...), title)
			if byPath == nil && (pathEqual(full, parts) || len(full) > 1 && pathEqual(full[1:], parts)) {
				byPath = e
			}
		}
		for i := range g.Groups {
			walk(&g.Groups[i], append(append([]string{}, path...), g.Groups[i].Name))
		}
	}
	for i := range k.db.Content.Root.Groups {
		g := &k.db.Content.Root.Groups[i]
		walk(g, []string{g.Name})
	}
	switch {
	case byPath != nil:
		return byPath, nil
	case len(byTitle) == 1:
		return byTitle[0], nil
	case len(byTitle) > 1:
		return nil, fmt.Errorf("%d KeePass entries are called %q: give its group too (Group/%s)", len(byTitle), ref, ref)
	}
	return nil, fmt.Errorf("no KeePass entry %q", ref)
}

func pathEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// ---- Bitwarden ------------------------------------------------------------

// Bitwarden runs the bw program. Session is the key bw unlock gave.
type Bitwarden struct {
	Program string // "" = bw on the PATH
	mu      sync.Mutex
	session string
}

func (b *Bitwarden) program() string {
	if b.Program != "" {
		return b.Program
	}
	return "bw"
}

// run starts bw with an environment that has no session of the user's
// shell, plus env.
var runBW = func(ctx context.Context, program string, env []string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, program, args...)
	cmd.Env = append(os.Environ(), env...)
	hideWindow(cmd)
	out, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && len(ee.Stderr) > 0 {
			return nil, errors.New(strings.TrimSpace(string(ee.Stderr)))
		}
		if errors.Is(err, exec.ErrNotFound) {
			return nil, errors.New("the Bitwarden command line program (bw) was not found: install it and sign in once with bw login")
		}
		return nil, err
	}
	return out, nil
}

// Unlocked reports whether bw unlock has given a session.
func (b *Bitwarden) Unlocked() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.session != ""
}

// Unlock unlocks the vault with the master password (bw unlock).
func (b *Bitwarden) Unlock(ctx context.Context, password string) error {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	out, err := runBW(ctx, b.program(), []string{"YACM_BW_PASSWORD=" + password}, "unlock", "--raw", "--passwordenv", "YACM_BW_PASSWORD")
	if err != nil {
		return fmt.Errorf("Bitwarden could not be unlocked: %w", err)
	}
	s := strings.TrimSpace(string(out))
	if s == "" {
		return errors.New("Bitwarden gave no session: sign in once with bw login")
	}
	b.mu.Lock()
	b.session = s
	b.mu.Unlock()
	return nil
}

// Lock forgets the session.
func (b *Bitwarden) Lock() {
	b.mu.Lock()
	b.session = ""
	b.mu.Unlock()
}

// Password returns the password of item ref (its name or ID).
func (b *Bitwarden) Password(ctx context.Context, ref string) (string, error) {
	b.mu.Lock()
	s := b.session
	b.mu.Unlock()
	if s == "" {
		return "", errors.New("Bitwarden is locked")
	}
	if strings.TrimSpace(ref) == "" || strings.HasPrefix(ref, "-") {
		return "", errors.New("give the Bitwarden item's name or ID")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	out, err := runBW(ctx, b.program(), []string{"BW_SESSION=" + s}, "get", "password", ref)
	if err != nil {
		return "", fmt.Errorf("Bitwarden item %q: %w", ref, err)
	}
	return strings.TrimRight(string(out), "\r\n"), nil
}
