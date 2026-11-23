// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (C) 2026 The YACM Authors. Additional terms under GPLv3 section 7: see NOTICE.md.

package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSetSortBy(t *testing.T) {
	p := filepath.Join(t.TempDir(), "sessions.json")
	s, _ := Open(p)
	g, _ := s.Upsert(&Node{Name: "Core", IsGroup: true})
	r, _ := s.Upsert(&Node{Name: "R1", ParentID: g.ID, Session: &Session{Protocol: ProtoSSH, Host: "10.0.0.1"}})
	if err := s.SetSortBy(g.ID, &FolderSort{Key: "address", Subfolders: true}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetSortBy(g.ID, &FolderSort{Key: "colour"}); err == nil {
		t.Fatal("unknown key accepted")
	}
	if err := s.SetSortBy(r.ID, &FolderSort{Key: "name"}); err == nil {
		t.Fatal("a session was sorted")
	}
	// Saving the group from the editor keeps its sorting.
	edit, _ := s.Get(g.ID)
	edit.SortBy = nil
	edit.Name = "Core 2"
	if _, err := s.Upsert(edit); err != nil {
		t.Fatal(err)
	}
	s2, _ := Open(p)
	got, _ := s2.Get(g.ID)
	if got.SortBy == nil || got.SortBy.Key != "address" || !got.SortBy.Subfolders || got.Name != "Core 2" {
		t.Fatalf("after reload: %+v", got.SortBy)
	}
	if err := s2.SetSortBy(g.ID, nil); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(p)
	if strings.Contains(string(data), "sortBy") {
		t.Fatal("sortBy still written after turning it off")
	}
}

// A newer version may add members to sortBy; they survive a save here.
func TestFolderSortKeepsUnknown(t *testing.T) {
	var n Node
	if err := json.Unmarshal([]byte(`{"id":"g","isGroup":true,"name":"G","sortBy":{"key":"name","natural":false}}`), &n); err != nil {
		t.Fatal(err)
	}
	out, _ := json.Marshal(n)
	if !strings.Contains(string(out), `"natural":false`) {
		t.Fatalf("unknown member lost: %s", out)
	}
	DropUnknown(&n)
	out, _ = json.Marshal(n)
	if strings.Contains(string(out), "natural") || !strings.Contains(string(out), `"key":"name"`) {
		t.Fatalf("DropUnknown: %s", out)
	}
}

func TestSetChildOrder(t *testing.T) {
	s, _ := Open(filepath.Join(t.TempDir(), "sessions.json"))
	g, _ := s.Upsert(&Node{Name: "G", IsGroup: true})
	var ids []string
	for _, name := range []string{"a", "b", "c", "d"} {
		n, _ := s.Upsert(&Node{Name: name, ParentID: g.ID, Session: &Session{Protocol: ProtoSSH, Host: name}})
		ids = append(ids, n.ID)
	}
	other, _ := s.Upsert(&Node{Name: "elsewhere", Session: &Session{Protocol: ProtoSSH, Host: "x"}})
	// c, a and something outside the folder; b and d keep their order after them.
	if err := s.SetChildOrder(g.ID, []string{ids[2], ids[0], other.ID}); err != nil {
		t.Fatal(err)
	}
	order := map[string]int{}
	for _, n := range s.List() {
		if n.ParentID == g.ID {
			order[n.Name] = n.Order
		}
	}
	if !(order["c"] < order["a"] && order["a"] < order["b"] && order["b"] < order["d"]) {
		t.Fatalf("order %v", order)
	}
	if o, _ := s.Get(other.ID); o.Order != other.Order {
		t.Fatal("a node outside the folder moved")
	}
}
