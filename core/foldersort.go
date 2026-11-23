// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (C) 2026 The YACM Authors. Additional terms under GPLv3 section 7: see NOTICE.md.

package core

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/yacm-app/yacm/internal/store"
)

// Sorting of folders. Your own folders keep it in sessions.json (sortBy on
// the group); the top level and team library folders, which have no node of
// yours to keep it, keep it per computer in folder-sort.json.

var sortMu sync.Mutex

func (a *App) sortFile() string { return filepath.Join(a.loc.Default, "folder-sort.json") }

func (a *App) readLocalSorts() map[string]*store.FolderSort {
	out := map[string]*store.FolderSort{}
	data, err := os.ReadFile(a.sortFile())
	if err != nil {
		return out
	}
	if json.Unmarshal(data, &out) != nil || out == nil {
		return map[string]*store.FolderSort{}
	}
	for id, s := range out {
		if s == nil || !store.SortKeys[s.Key] {
			delete(out, id)
		}
	}
	return out
}

// LocalFolderSorts returns the sorting of the top level (key "") and of
// team library folders, kept on this computer.
func (a *App) LocalFolderSorts() map[string]*store.FolderSort {
	sortMu.Lock()
	defer sortMu.Unlock()
	return a.readLocalSorts()
}

// SetFolderSort keeps a folder sorted (nil: back to your own order). id ""
// is the top level.
func (a *App) SetFolderSort(id string, sort *store.FolderSort) error {
	if sort != nil && !store.SortKeys[sort.Key] {
		return fmt.Errorf("unknown sort order %q", sort.Key)
	}
	local := id == ""
	if !local {
		n, err := a.store.Get(id)
		if err != nil {
			return err
		}
		if !n.IsGroup {
			return errors.New("only folders can be sorted")
		}
		local = n.Library != ""
	}
	if !local {
		return a.store.SetSortBy(id, sort)
	}
	sortMu.Lock()
	defer sortMu.Unlock()
	m := a.readLocalSorts()
	if sort == nil {
		delete(m, id)
	} else {
		m[id] = &store.FolderSort{Key: sort.Key, Subfolders: sort.Subfolders}
	}
	data, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return store.WriteFileAtomic(a.sortFile(), data, 0o600)
}

// SetChildOrder stores the order of a folder's contents as shown (ids of
// its children), when a sorted folder goes back to your own order.
func (a *App) SetChildOrder(parentID string, ids []string) error {
	return a.store.SetChildOrder(parentID, ids)
}
