// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (C) 2026 The YACM Authors. Additional terms under GPLv3 section 7: see NOTICE.md.

package core

import (
	"testing"

	"github.com/yacm-app/yacm/internal/store"
)

// Editing many sessions stores the password and hidden rule values for
// each session, under that session's own rule IDs.
func TestPatchSessionsSecrets(t *testing.T) {
	a := newTestApp(t)
	g, _ := a.store.Upsert(&store.Node{Name: "Sites", IsGroup: true})
	var ids []string
	for _, h := range []string{"10.0.0.1", "10.0.0.2"} {
		n, err := a.SaveNode(store.Node{Name: h, ParentID: g.ID, Session: &store.Session{Protocol: store.ProtoSSH, Host: h,
			Expect: []store.ExpectRule{{ID: "old", Active: true, Expect: "secret:", Hidden: true}}}}, &SecretInput{Expect: map[string]*string{"old": sp("x")}})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, n.ID)
	}
	res, err := a.PatchSessions(PatchRequest{
		IDs: []string{g.ID},
		Patch: store.SessionPatch{
			Session: store.Session{Expect: []store.ExpectRule{{ID: "p1", Active: true, Expect: "[Pp]assword:", Send: "leak?", Hidden: true}}},
			Set:     []string{"expect"}, Lists: map[string]string{"expect": store.ListReplace},
		},
		Password: sp("pw"), Expect: map[string]string{"p1": "enable-pw"},
	})
	if err != nil || res.Changed != 2 {
		t.Fatalf("%v %+v", err, res)
	}
	for _, id := range ids {
		n, _ := a.store.Get(id)
		r := n.Session.Expect
		if len(r) != 1 || r[0].Send != "" {
			t.Fatalf("rules %+v", r)
		}
		if a.secret(store.SecretPassword(id)) != "pw" || a.secret(store.SecretExpect(id, r[0].ID)) != "enable-pw" {
			t.Fatal("secrets not stored per session")
		}
		if a.vault.Has(store.SecretExpect(id, "old")) {
			t.Fatal("the replaced rule's secret is still stored")
		}
	}
}
