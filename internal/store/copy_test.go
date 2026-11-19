// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (C) 2026 The YACM Authors. Additional terms under GPLv3 section 7: see NOTICE.md.

package store

import (
	"path/filepath"
	"testing"
)

func TestCopyNodes(t *testing.T) {
	s, _ := Open(filepath.Join(t.TempDir(), "sessions.json"))
	site, _ := s.Upsert(&Node{Name: "Site A", IsGroup: true})
	sub, _ := s.Upsert(&Node{Name: "Access", IsGroup: true, ParentID: site.ID})
	jump, _ := s.Upsert(&Node{Name: "jump", ParentID: site.ID, Session: &Session{Protocol: ProtoSSH, Host: "10.0.0.1"}})
	outside, _ := s.Upsert(&Node{Name: "bastion", Session: &Session{Protocol: ProtoSSH, Host: "10.9.9.9"}})
	sw, _ := s.Upsert(&Node{Name: "sw1", ParentID: sub.ID, Session: &Session{Protocol: ProtoSSH, Host: "10.0.0.2", JumpVia: jump.ID,
		Expect: []ExpectRule{{ID: "r1", Active: true, Expect: "Password:", Send: "x"}}}})
	sw2, _ := s.Upsert(&Node{Name: "sw2", ParentID: sub.ID, Session: &Session{Protocol: ProtoSSH, Host: "10.0.0.3", JumpVia: outside.ID}})
	sub.Defaults = &GroupDefaults{JumpVia: jump.ID}
	if _, err := s.Upsert(sub); err != nil {
		t.Fatal(err)
	}
	dest, _ := s.Upsert(&Node{Name: "Site B", IsGroup: true})

	// The whole site, and sw2 again (inside it: copied once).
	tops, idMap, rules, err := s.CopyNodes([]string{site.ID, sw2.ID}, dest.ID, -1)
	if err != nil {
		t.Fatal(err)
	}
	if len(tops) != 1 || tops[0].Name != "Site A" || tops[0].ParentID != dest.ID || len(idMap) != 5 {
		t.Fatalf("tops %+v, %d copied", tops, len(idMap))
	}
	get := func(id string) *Node { n, _ := s.Get(id); return n }
	csw, csub, cjump := get(idMap[sw.ID]), get(idMap[sub.ID]), get(idMap[jump.ID])
	if csw == nil || csw.ParentID != csub.ID || csub.ParentID != tops[0].ID || cjump.ParentID != tops[0].ID {
		t.Fatal("tree not copied")
	}
	// Jump hosts inside the copy point at the copies, outside ones stay.
	if csw.Session.JumpVia != cjump.ID || csub.Defaults.JumpVia != cjump.ID || get(idMap[sw2.ID]).Session.JumpVia != outside.ID {
		t.Fatal("jump hosts not remapped")
	}
	if r := csw.Session.Expect[0].ID; r == "r1" || rules[csw.ID]["r1"] != r {
		t.Fatalf("rule ids %v", rules[csw.ID])
	}
	if get(sw.ID).Session.JumpVia != jump.ID {
		t.Fatal("original changed")
	}

	// A name taken where it goes gets "(copy)", "(copy 2)"; index places it.
	tops, _, _, err = s.CopyNodes([]string{outside.ID}, "", 0)
	if err != nil || tops[0].Name != "bastion (copy)" || tops[0].Order != 0 {
		t.Fatalf("%v %+v", err, tops)
	}
	tops, _, _, _ = s.CopyNodes([]string{outside.ID}, "", -1)
	if tops[0].Name != "bastion (copy 2)" {
		t.Fatalf("second copy %q", tops[0].Name)
	}

	// Not into itself or into a session.
	if _, _, _, err := s.CopyNodes([]string{site.ID}, sub.ID, -1); err == nil {
		t.Fatal("pasted a group into itself")
	}
	if _, _, _, err := s.CopyNodes([]string{sw.ID}, sw2.ID, -1); err == nil {
		t.Fatal("pasted into a session")
	}
}
