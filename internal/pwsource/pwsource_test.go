// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (C) 2026 The YACM Authors. Additional terms under GPLv3 section 7: see NOTICE.md.

package pwsource

import (
	"context"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tobischo/gokeepasslib/v3"
	w "github.com/tobischo/gokeepasslib/v3/wrappers"
)

func entry(title, pass string) gokeepasslib.Entry {
	e := gokeepasslib.NewEntry()
	e.Values = append(e.Values,
		gokeepasslib.ValueData{Key: "Title", Value: gokeepasslib.V{Content: title}},
		gokeepasslib.ValueData{Key: "Password", Value: gokeepasslib.V{Content: pass, Protected: w.NewBoolWrapper(true)}})
	return e
}

// writeKDBX writes a KDBX 4 database with two groups.
func writeKDBX(t *testing.T, password string) (string, gokeepasslib.UUID) {
	t.Helper()
	core := gokeepasslib.NewGroup()
	core.Name = "Core"
	core.Entries = append(core.Entries, entry("tacacs", "core-secret"), entry("shared", "a"))
	lab := gokeepasslib.NewGroup()
	lab.Name = "Lab"
	lab.Entries = append(lab.Entries, entry("tacacs", "lab-secret"), entry("shared", "b"), entry("only-here", "unique"))
	root := gokeepasslib.NewGroup()
	root.Name = "Network"
	root.Groups = append(root.Groups, core, lab)
	db := gokeepasslib.NewDatabase(gokeepasslib.WithDatabaseKDBXVersion4())
	db.Content.Root = &gokeepasslib.RootData{Groups: []gokeepasslib.Group{root}}
	db.Credentials = gokeepasslib.NewPasswordCredentials(password)
	if err := db.LockProtectedEntries(); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "team.kdbx")
	f, _ := os.Create(p)
	if err := gokeepasslib.NewEncoder(f).Encode(db); err != nil {
		t.Fatal(err)
	}
	f.Close()
	return p, lab.Entries[2].UUID
}

func TestKeePass(t *testing.T) {
	path, uuid := writeKDBX(t, "kp-master")
	if _, err := OpenKeePass(path, "wrong", ""); err == nil {
		t.Fatal("opened with a wrong password")
	}
	kp, err := OpenKeePass(path, "kp-master", "")
	if err != nil {
		t.Fatal(err)
	}
	for ref, want := range map[string]string{
		"Core/tacacs":               "core-secret",
		"Network/Lab/tacacs":        "lab-secret",
		"only-here":                 "unique",
		hex.EncodeToString(uuid[:]): "unique",
	} {
		if got, err := kp.Password(ref); err != nil || got != want {
			t.Errorf("%s: %q %v", ref, got, err)
		}
	}
	if _, err := kp.Password("tacacs"); err == nil || !strings.Contains(err.Error(), "Group/tacacs") {
		t.Fatalf("an ambiguous title: %v", err)
	}
	if _, err := kp.Password("Core/none"); err == nil {
		t.Fatal("a missing entry was found")
	}
}

func TestBitwarden(t *testing.T) {
	var got [][]string
	runBW = func(ctx context.Context, program string, env []string, args ...string) ([]byte, error) {
		got = append(got, append(append([]string{program}, env...), args...))
		switch args[0] {
		case "unlock":
			if env[0] != "YACM_BW_PASSWORD=bw-master" {
				return nil, errors.New("Invalid master password.")
			}
			return []byte("SESSIONKEY\n"), nil
		case "get":
			if env[0] != "BW_SESSION=SESSIONKEY" {
				return nil, errors.New("locked")
			}
			return []byte("item-secret\n"), nil
		}
		return nil, errors.New("?")
	}
	b := &Bitwarden{}
	if _, err := b.Password(context.Background(), "core"); err == nil {
		t.Fatal("a locked vault answered")
	}
	if err := b.Unlock(context.Background(), "nope"); err == nil {
		t.Fatal("unlocked with a wrong password")
	}
	if err := b.Unlock(context.Background(), "bw-master"); err != nil {
		t.Fatal(err)
	}
	if p, err := b.Password(context.Background(), "core tacacs"); err != nil || p != "item-secret" {
		t.Fatalf("%q %v", p, err)
	}
	last := got[len(got)-1]
	if strings.Join(last[2:], " ") != "get password core tacacs" {
		t.Fatalf("bw called as %q", last)
	}
	if _, err := b.Password(context.Background(), "--raw"); err == nil {
		t.Fatal("an option as item name")
	}
	b.Lock()
	if b.Unlocked() {
		t.Fatal("still unlocked")
	}
}
