// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (C) 2026 The YACM Authors. Additional terms under GPLv3 section 7: see NOTICE.md.

package core

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/yacm-app/yacm/internal/store"
)

// PatchRequest edits many sessions at once from the session editor: the
// changed settings, plus secrets for each session (nil = unchanged).
type PatchRequest struct {
	IDs        []string           `json:"ids"`
	Patch      store.SessionPatch `json:"patch"`
	Password   *string            `json:"password"`
	Passphrase *string            `json:"passphrase"`
	// Expect: hidden values of automation rules in the patch, by rule ID.
	Expect map[string]string `json:"expect"`
	// Jump: passwords of the jump hosts in the patch, by hop index.
	Jump map[string]string `json:"jump"`
}

// PatchResult says how many sessions changed and which were left alone.
type PatchResult struct {
	Changed int      `json:"changed"`
	Skipped []string `json:"skipped"`
}

// PatchSessions applies the editor's changes to each of the sessions.
func (a *App) PatchSessions(req PatchRequest) (*PatchResult, error) {
	hasSecrets := req.Password != nil || req.Passphrase != nil || len(req.Expect) > 0 || len(req.Jump) > 0
	if hasSecrets && !a.vault.Unlocked() {
		return nil, errors.New("unlock the vault to store passwords")
	}
	for i := range req.Patch.Session.Expect {
		if req.Patch.Session.Expect[i].Hidden {
			req.Patch.Session.Expect[i].Send = "" // hidden values live in the vault only
		}
	}
	ids := a.store.SessionsUnder(req.IDs)
	a.snapshot("before editing many sessions")
	res, err := a.store.PatchSessions(ids, req.Patch, nil)
	if err != nil {
		return nil, err
	}
	out := &PatchResult{Changed: len(res.Changed), Skipped: res.Skipped}
	if out.Skipped == nil {
		out.Skipped = []string{}
	}
	if !a.vault.Unlocked() {
		return out, nil
	}
	setJump := false
	for _, k := range req.Patch.Set {
		setJump = setJump || k == "jump"
	}
	kv := map[string]string{}
	for _, id := range res.Changed {
		n, err := a.store.Get(id)
		if err != nil || n.Session == nil {
			continue
		}
		if req.Password != nil {
			kv[store.SecretPassword(id)] = *req.Password
		}
		if req.Passphrase != nil && n.Session.Protocol == store.ProtoSSH {
			kv[store.SecretPassphrase(id)] = *req.Passphrase
		}
		for rid, v := range req.Expect {
			if to, ok := res.RuleIDs[id][rid]; ok {
				kv[store.SecretExpect(id, to)] = v
			}
		}
		if setJump && len(n.Session.Jump) > 0 {
			for i := 0; i < len(n.Session.Jump)+8; i++ {
				kv[store.SecretJumpPassword(id, i)] = req.Jump[strconv.Itoa(i)]
			}
		}
		// Secrets of hidden rules that are gone.
		keep := map[string]bool{}
		for _, r := range n.Session.Expect {
			if r.Hidden {
				keep[r.ID] = true
			}
		}
		prefix := store.SecretPrefix(id) + "expect/"
		for _, k := range a.vault.Keys(prefix) {
			if _, set := kv[k]; !set && !keep[strings.TrimPrefix(k, prefix)] {
				kv[k] = ""
			}
		}
	}
	if len(kv) > 0 {
		if err := a.vault.SetMany(kv); err != nil {
			return out, fmt.Errorf("sessions changed, but passwords were not saved: %w", err)
		}
	}
	return out, nil
}
