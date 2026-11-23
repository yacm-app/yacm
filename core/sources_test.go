// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (C) 2026 The YACM Authors. Additional terms under GPLv3 section 7: see NOTICE.md.

package core

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/tobischo/gokeepasslib/v3"
	w "github.com/tobischo/gokeepasslib/v3/wrappers"

	"github.com/yacm-app/yacm/internal/store"
)

func testKDBX(t *testing.T) string {
	t.Helper()
	e := gokeepasslib.NewEntry()
	e.Values = append(e.Values,
		gokeepasslib.ValueData{Key: "Title", Value: gokeepasslib.V{Content: "tacacs"}},
		gokeepasslib.ValueData{Key: "Password", Value: gokeepasslib.V{Content: "from-keepass", Protected: w.NewBoolWrapper(true)}})
	g := gokeepasslib.NewGroup()
	g.Name = "Network"
	g.Entries = append(g.Entries, e)
	db := gokeepasslib.NewDatabase(gokeepasslib.WithDatabaseKDBXVersion4())
	db.Content.Root = &gokeepasslib.RootData{Groups: []gokeepasslib.Group{g}}
	db.Credentials = gokeepasslib.NewPasswordCredentials("kp-master")
	db.LockProtectedEntries()
	p := filepath.Join(t.TempDir(), "net.kdbx")
	f, _ := os.Create(p)
	defer f.Close()
	if err := gokeepasslib.NewEncoder(f).Encode(db); err != nil {
		t.Fatal(err)
	}
	return p
}

// A profile's password comes from KeePass once it is unlocked; the
// session's connect asks for the master password.
func TestKeePassProfile(t *testing.T) {
	a := newTestApp(t)
	st := a.store.Settings()
	st.KeePassFile = testKDBX(t)
	if err := a.SaveSettings(st); err != nil {
		t.Fatal(err)
	}
	c, err := a.SaveCredential(store.Credential{Name: "TACACS", User: "jsmith", Source: &store.SecretSource{Kind: "keepass", Ref: "Network/tacacs"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if v := a.secret(store.SecretCredPassword(c.ID)); v != "" {
		t.Fatalf("a locked KeePass answered %q", v)
	}
	if err := a.CheckSecretSource(*c.Source); err == nil {
		t.Fatal("check passed while locked")
	}
	if err := a.UnlockKeePass("wrong"); err == nil {
		t.Fatal("unlocked with a wrong password")
	}
	if err := a.UnlockKeePass("kp-master"); err != nil {
		t.Fatal(err)
	}
	if v := a.secret(store.SecretCredPassword(c.ID)); v != "from-keepass" {
		t.Fatalf("password %q", v)
	}
	if !a.PasswordManagerStatus().KeePassUnlocked {
		t.Fatal("status says locked")
	}
	a.LockVault()
	if a.PasswordManagerStatus().KeePassUnlocked {
		t.Fatal("locking the vault left KeePass open")
	}
	if _, err := a.SaveCredential(store.Credential{Name: "x", Source: &store.SecretSource{Kind: "keepass"}}, nil); err == nil {
		t.Fatal("a source without an entry was accepted")
	}
}
