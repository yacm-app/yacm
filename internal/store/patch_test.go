// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (C) 2026 The YACM Authors. Additional terms under GPLv3 section 7: see NOTICE.md.

package store

import (
	"fmt"
	"path/filepath"
	"reflect"
	"testing"
)

func patchStore(t *testing.T) (*Store, map[string]string) {
	t.Helper()
	s, _ := Open(filepath.Join(t.TempDir(), "sessions.json"))
	g, _ := s.Upsert(&Node{Name: "G", IsGroup: true})
	ids := map[string]string{"group": g.ID}
	add := func(name string, ss *Session) {
		n, err := s.Upsert(&Node{Name: name, ParentID: g.ID, Session: ss})
		if err != nil {
			t.Fatal(err)
		}
		ids[name] = n.ID
	}
	add("ssh1", &Session{Protocol: ProtoSSH, Host: "10.0.0.1", User: "a", Clusters: []string{"CORE"},
		Expect: []ExpectRule{{ID: "x1", Active: true, Expect: ">", Send: "enable", Return: true, OnFail: "stop"}}})
	add("ssh2", &Session{Protocol: ProtoSSH, Host: "10.0.0.2", Port: 2222, User: "b", Clusters: []string{"EDGE"}})
	add("tel", &Session{Protocol: ProtoTelnet, Host: "10.0.0.3", Clusters: []string{"CORE"}})
	add("con", &Session{Protocol: ProtoSerial, Serial: &Serial{Device: "COM3", Baud: 9600, DataBits: 8, Parity: "none", StopBits: "1"}})
	return s, ids
}

func get(t *testing.T, s *Store, id string) *Session {
	t.Helper()
	n, err := s.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	return n.Session
}

func TestPatchSetsOnlyChangedMembers(t *testing.T) {
	s, ids := patchStore(t)
	all := []string{ids["ssh1"], ids["ssh2"], ids["tel"], ids["con"]}
	p := SessionPatch{
		Session: Session{User: "jsmith", Algorithms: AlgoLegacy, Port: 8022, FontSize: 16, Serial: &Serial{Device: "COM9", Baud: 115200, DataBits: 8, Parity: "none", StopBits: "1"}},
		Set:     []string{"user", "algorithms", "port", "fontSize", "serial"},
	}
	res, err := s.PatchSessions(all, p, nil)
	if err != nil || len(res.Changed) != 4 {
		t.Fatalf("%v %+v", err, res)
	}
	a, b, tel, con := get(t, s, ids["ssh1"]), get(t, s, ids["ssh2"]), get(t, s, ids["tel"]), get(t, s, ids["con"])
	if a.User != "jsmith" || b.User != "jsmith" || tel.User != "jsmith" || con.User != "jsmith" {
		t.Fatal("user not set everywhere")
	}
	if a.Host != "10.0.0.1" || b.Host != "10.0.0.2" {
		t.Fatal("hosts changed")
	}
	if a.Algorithms != AlgoLegacy || tel.Algorithms == AlgoLegacy {
		t.Fatalf("algorithms: ssh %q telnet %q", a.Algorithms, tel.Algorithms)
	}
	if a.Port != 8022 || tel.Port != 8022 || con.Port != 0 {
		t.Fatalf("ports %d %d %d", a.Port, tel.Port, con.Port)
	}
	if con.Serial.Device != "COM3" || con.Serial.Baud != 115200 || a.Serial != nil {
		t.Fatalf("serial %+v / %+v", con.Serial, a.Serial)
	}
	if len(a.Expect) != 1 || !reflect.DeepEqual(a.Clusters, []string{"CORE"}) {
		t.Fatal("members that were not changed moved")
	}
	if _, err := s.PatchSessions(all, SessionPatch{Session: Session{Host: "x"}, Set: []string{"host"}}, nil); err == nil {
		t.Fatal("host set for many")
	}
}

func TestPatchLists(t *testing.T) {
	s, ids := patchStore(t)
	two := []string{ids["ssh1"], ids["ssh2"]}
	n := 0
	newID := func() string { n++; return fmt.Sprintf("new%d", n) }
	rule := ExpectRule{ID: "p1", Active: true, Expect: "[Pp]assword:", Hidden: true, Return: true, OnFail: "stop"}
	if _, err := s.PatchSessions(two, SessionPatch{Session: Session{Expect: []ExpectRule{rule}}, Set: []string{"expect"}}, newID); err == nil {
		t.Fatal("a list without a mode was accepted")
	}
	res, err := s.PatchSessions(two, SessionPatch{Session: Session{Expect: []ExpectRule{rule}, Clusters: []string{"LAB"}}, Set: []string{"expect", "clusters"}, Lists: map[string]string{"expect": ListAdd, "clusters": ListAdd}}, newID)
	if err != nil {
		t.Fatal(err)
	}
	a, b := get(t, s, ids["ssh1"]), get(t, s, ids["ssh2"])
	if len(a.Expect) != 2 || a.Expect[1].Expect != "[Pp]assword:" || len(b.Expect) != 1 {
		t.Fatalf("add: %+v / %+v", a.Expect, b.Expect)
	}
	if a.Expect[1].ID == b.Expect[0].ID || res.RuleIDs[ids["ssh1"]]["p1"] != a.Expect[1].ID || res.RuleIDs[ids["ssh2"]]["p1"] != b.Expect[0].ID {
		t.Fatalf("rule IDs %+v", res.RuleIDs)
	}
	if !reflect.DeepEqual(a.Clusters, []string{"CORE", "LAB"}) || !reflect.DeepEqual(b.Clusters, []string{"EDGE", "LAB"}) {
		t.Fatalf("tags %v %v", a.Clusters, b.Clusters)
	}
	// Remove the enable rule (by what it waits for and sends) and a tag.
	_, err = s.PatchSessions(two, SessionPatch{Session: Session{Expect: []ExpectRule{{Expect: ">", Send: "enable"}}, Clusters: []string{"lab"}}, Set: []string{"expect", "clusters"}, Lists: map[string]string{"expect": ListRemove, "clusters": ListRemove}}, newID)
	if err != nil {
		t.Fatal(err)
	}
	a, b = get(t, s, ids["ssh1"]), get(t, s, ids["ssh2"])
	if len(a.Expect) != 1 || a.Expect[0].Expect != "[Pp]assword:" || len(b.Expect) != 1 {
		t.Fatalf("remove: %+v / %+v", a.Expect, b.Expect)
	}
	if !reflect.DeepEqual(a.Clusters, []string{"CORE"}) {
		t.Fatalf("tags %v", a.Clusters)
	}
	// Replace saved commands.
	_, err = s.PatchSessions(two, SessionPatch{Session: Session{Macros: []Macro{{Name: "ver", Text: "show version", Return: true}}}, Set: []string{"macros"}, Lists: map[string]string{"macros": ListReplace}}, newID)
	if err != nil || len(get(t, s, ids["ssh2"]).Macros) != 1 {
		t.Fatal(err)
	}
}

func TestPatchProtocolAndMove(t *testing.T) {
	s, ids := patchStore(t)
	g2, _ := s.Upsert(&Node{Name: "Other", IsGroup: true})
	all := []string{ids["ssh1"], ids["ssh2"], ids["tel"], ids["con"]}
	_, err := s.PatchSessions(all, SessionPatch{Session: Session{Protocol: ProtoTelnet}, Set: []string{"protocol"}, ParentID: &g2.ID}, nil)
	if err != nil {
		t.Fatal(err)
	}
	a, b, con := get(t, s, ids["ssh1"]), get(t, s, ids["ssh2"]), get(t, s, ids["con"])
	if a.Protocol != ProtoTelnet || a.Port != 23 || b.Port != 2222 || con.Protocol != ProtoSerial {
		t.Fatalf("protocol %s:%d %d %s", a.Protocol, a.Port, b.Port, con.Protocol)
	}
	for _, id := range all {
		if n, _ := s.Get(id); n.ParentID != g2.ID {
			t.Fatal("not moved")
		}
	}
	if _, err := s.PatchSessions(all, SessionPatch{Session: Session{Protocol: ProtoSerial}, Set: []string{"protocol"}}, nil); err == nil {
		t.Fatal("switched to serial")
	}
}

func TestPatchLeavesNewerTypesAlone(t *testing.T) {
	s, ids := patchStore(t)
	n, _ := s.Get(ids["ssh1"])
	n.Session.Protocol = "rdp"
	s.mu.Lock()
	s.nodes[n.ID] = n
	s.mu.Unlock()
	res, err := s.PatchSessions([]string{ids["ssh1"], ids["ssh2"]}, SessionPatch{Session: Session{User: "x"}, Set: []string{"user"}}, nil)
	if err != nil || len(res.Changed) != 1 || len(res.Skipped) != 1 {
		t.Fatalf("%v %+v", err, res)
	}
	if get(t, s, ids["ssh1"]).User == "x" {
		t.Fatal("a newer connection type was changed")
	}
}

func TestPatchToSSHDropsLoginRules(t *testing.T) {
	s, ids := patchStore(t)
	n, _ := s.Get(ids["tel"])
	n.Session.Expect = []ExpectRule{{ID: "u", Active: true, Expect: "Username:", Send: "<USER>", Return: true, OnFail: "stop"}, {ID: "e", Active: true, Expect: ">", Send: "enable", Return: true, OnFail: "stop"}}
	if _, err := s.Upsert(n); err != nil {
		t.Fatal(err)
	}
	_, err := s.PatchSessions([]string{ids["tel"], ids["con"]}, SessionPatch{Session: Session{Protocol: ProtoSSH}, Set: []string{"protocol"}, DropLoginRules: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	tel := get(t, s, ids["tel"])
	if tel.Protocol != ProtoSSH || tel.Port != 22 || len(tel.Expect) != 1 || tel.Expect[0].Send != "enable" || tel.Algorithms != Inherit {
		t.Fatalf("%+v", tel)
	}
}
