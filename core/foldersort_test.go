// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (C) 2026 The YACM Authors. Additional terms under GPLv3 section 7: see NOTICE.md.

package core

import (
	"testing"

	"github.com/yacm-app/yacm/internal/store"
)

func TestSetFolderSort(t *testing.T) {
	a := newTestApp(t)
	g, err := a.store.Upsert(&store.Node{Name: "Sites", IsGroup: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.SetFolderSort(g.ID, &store.FolderSort{Key: "name"}); err != nil {
		t.Fatal(err)
	}
	if n, _ := a.store.Get(g.ID); n.SortBy == nil || n.SortBy.Key != "name" {
		t.Fatalf("group: %+v", n.SortBy)
	}
	// The top level is kept on this computer.
	if err := a.SetFolderSort("", &store.FolderSort{Key: "count", Subfolders: true}); err != nil {
		t.Fatal(err)
	}
	m := a.LocalFolderSorts()
	if m[""] == nil || m[""].Key != "count" || !m[""].Subfolders {
		t.Fatalf("local: %+v", m)
	}
	if err := a.SetFolderSort("", nil); err != nil {
		t.Fatal(err)
	}
	if len(a.LocalFolderSorts()) != 0 {
		t.Fatal("top level still sorted")
	}
	if err := a.SetFolderSort("", &store.FolderSort{Key: "nope"}); err == nil {
		t.Fatal("unknown key accepted")
	}
}
