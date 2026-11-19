// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (C) 2026 The YACM Authors. Additional terms under GPLv3 section 7: see NOTICE.md.

package core

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/yacm-app/yacm/internal/store"
)

// scrtWithProfiles writes SecureCRT sessions with credential sets and a
// logon action.
func scrtWithProfiles(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	// "xml-pw", encrypted the "02:" way without a passphrase.
	const pw = "02:cf6fd9633a9e5a055cd863dcb6a322c7ed32c8bb926618581a25e4d2bcfa1e70851f790b1ffffcd218e07791612230b51dcab99aceacd3f01647b11656fa40a8"
	files := map[string]string{
		"Credentials/A1-admin.ini": "S:\"Username\"=admin\nS:\"Password V2\"=" + pw + "\n",
		"Credentials/lab.ini":      "S:\"Username\"=labuser\n",
		"Sessions/APIC1.ini":       "S:\"Protocol Name\"=SSH2\nS:\"Hostname\"=10.12.0.36\nS:\"Credential Title\"=A1-admin\n",
		"Sessions/S2.ini":          "S:\"Protocol Name\"=SSH2\nS:\"Hostname\"=10.12.0.37\nS:\"Credential Title\"=lab\n",
	}
	for f, text := range files {
		p := filepath.Join(root, "Config", filepath.FromSlash(f))
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(text), 0o600)
	}
	return root
}

func sessionNamed(a *App, name string) *store.Node {
	for _, n := range a.store.List() {
		if n.Name == name {
			return n
		}
	}
	return nil
}

func TestImportPartsWithoutProfiles(t *testing.T) {
	a := newTestApp(t)
	root := scrtWithProfiles(t)
	mine, _ := a.store.UpsertCredential(&store.Credential{Name: "LAB", User: "me"})
	rep, err := a.ImportForeign(ForeignRequest{Kind: "securecrt", Path: root, Parts: &ImportParts{Sessions: true, Passwords: true, Automation: true, JumpHosts: true}})
	if err != nil || rep.Sessions != 2 || rep.Profiles != 0 {
		t.Fatalf("%v %+v", err, rep)
	}
	if n := len(a.store.Credentials()); n != 1 {
		t.Fatalf("%d profiles", n)
	}
	// A profile of the same name you have is used; else the session gets
	// the login and its password itself.
	if s2 := sessionNamed(a, "S2"); s2.Session.CredentialID != mine.ID {
		t.Fatalf("S2 uses %q", s2.Session.CredentialID)
	}
	ap := sessionNamed(a, "APIC1")
	if ap.Session.CredentialID != "" || ap.Session.User != "admin" || a.secret(store.SecretPassword(ap.ID)) != "xml-pw" {
		t.Fatalf("APIC1 %+v", ap.Session)
	}
}

func TestImportPartsOnlyProfilesNoPasswords(t *testing.T) {
	a := newTestApp(t)
	root := scrtWithProfiles(t)
	rep, err := a.ImportForeign(ForeignRequest{Kind: "securecrt", Path: root, Parts: &ImportParts{Profiles: true}})
	if err != nil || rep.Sessions != 0 || rep.Profiles != 2 || rep.Secrets != 0 {
		t.Fatalf("%v %+v", err, rep)
	}
	if len(a.store.List()) != 0 {
		t.Fatal("sessions imported")
	}
	for _, c := range a.store.Credentials() {
		if a.vault.Has(store.SecretCredPassword(c.ID)) {
			t.Fatal("password imported")
		}
	}
	if _, err := a.ImportForeign(ForeignRequest{Kind: "securecrt", Path: root, Parts: &ImportParts{Passwords: true}}); err == nil {
		t.Fatal("nothing chosen, no error")
	}
}

func TestImportPartsWithoutAutomation(t *testing.T) {
	a := newTestApp(t)
	dir := "../internal/importer/testdata/foreign/securecrt-logon/Sessions"
	all := ImportParts{Sessions: true, Profiles: true, Passwords: true, Automation: true, JumpHosts: true}
	rep, err := a.ImportForeign(ForeignRequest{Kind: "securecrt", Path: dir, Parts: &all})
	if err != nil || rep.Automated == 0 {
		t.Fatalf("with automation: %v %+v", err, rep)
	}
	none := all
	none.Automation = false
	rep, err = a.ImportForeign(ForeignRequest{Kind: "securecrt", Path: dir, Parts: &none, GroupName: "plain"})
	if err != nil || rep.Automated != 0 {
		t.Fatalf("without: %v %+v", err, rep)
	}
	var g string
	for _, n := range a.store.List() {
		if n.Name == "plain" {
			g = n.ID
		}
	}
	nodes := a.store.List()
	parent := map[string]string{}
	for _, n := range nodes {
		parent[n.ID] = n.ParentID
	}
	under := func(id string) bool {
		for cur := parent[id]; cur != ""; cur = parent[cur] {
			if cur == g {
				return true
			}
		}
		return false
	}
	found := 0
	for _, n := range nodes {
		if n.Session != nil && under(n.ID) {
			found++
			if len(n.Session.Expect) > 0 {
				t.Fatalf("%s kept automation", n.Name)
			}
		}
	}
	if found == 0 {
		t.Fatal("nothing imported")
	}
}
