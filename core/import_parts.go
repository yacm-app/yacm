// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (C) 2026 The YACM Authors. Additional terms under GPLv3 section 7: see NOTICE.md.

package core

import (
	"fmt"
	"strings"

	"github.com/yacm-app/yacm/internal/importer"
	"github.com/yacm-app/yacm/internal/store"
)

// ImportParts chooses what an import from another program brings in.
type ImportParts struct {
	Sessions   bool `json:"sessions"`   // off: only the credential profiles
	Profiles   bool `json:"profiles"`   // off: sessions get the login itself, or a profile of the same name you have
	Passwords  bool `json:"passwords"`  // off: nothing goes into the vault
	Automation bool `json:"automation"` // off: no logon actions (automation rules)
	JumpHosts  bool `json:"jumpHosts"`  // off: sessions connect directly
}

// countJumps counts the sessions that go through a jump host.
func countJumps(nodes []*store.Node) int {
	n := 0
	for _, x := range nodes {
		if ss := x.Session; ss != nil && (len(ss.Jump) > 0 || (ss.JumpVia != "" && ss.JumpVia != store.Inherit)) {
			n++
		}
	}
	for _, x := range nodes {
		if d := x.Defaults; d != nil && d.JumpVia != "" && d.JumpVia != store.Inherit {
			return max(n, 1)
		}
	}
	return n
}

// applyParts leaves out of res what p does not want. existing are your
// credential profiles: with Profiles off a session links to one of the same
// name, else it takes the login itself.
func applyParts(res *importer.Result, p ImportParts, existing []*store.Credential) error {
	if !p.Sessions && !p.Profiles {
		return fmt.Errorf("choose sessions, credential profiles or both")
	}
	rep := &res.Report
	if !p.Passwords {
		res.Secrets = nil
	}
	if !p.Sessions {
		// Only the profiles, with their passwords.
		for k := range res.Secrets {
			if !strings.HasPrefix(k, "cred/") {
				delete(res.Secrets, k)
			}
		}
		res.Nodes = nil
		rep.Groups, rep.Sessions, rep.Automated, rep.Skipped = 0, 0, 0, nil
		return nil
	}
	byID := map[string]*store.Node{}
	for _, n := range res.Nodes {
		byID[n.ID] = n
	}
	if !p.JumpHosts {
		for _, n := range res.Nodes {
			if d := n.Defaults; d != nil {
				d.JumpVia = ""
			}
			if ss := n.Session; ss != nil {
				ss.JumpVia = ""
				ss.Jump = nil
			}
		}
		for k := range res.Secrets {
			if strings.Contains(k, "/jump") {
				delete(res.Secrets, k)
			}
		}
	}
	if !p.Automation {
		for _, n := range res.Nodes {
			if ss := n.Session; ss != nil && len(ss.Expect) > 0 {
				for _, r := range ss.Expect {
					delete(res.Secrets, store.SecretExpect(n.ID, r.ID))
				}
				ss.Expect = nil
			}
		}
		rep.Automated = 0
	}
	if !p.Profiles && len(res.Credentials) > 0 {
		imported := map[string]*store.Credential{}
		for _, c := range res.Credentials {
			imported[c.ID] = c
		}
		mine := map[string]string{}
		for _, c := range existing {
			mine[strings.ToLower(strings.TrimSpace(c.Name))] = c.ID
		}
		// yours: the profile of the same name you have ("" if none).
		yours := func(id string) string { return mine[strings.ToLower(strings.TrimSpace(imported[id].Name))] }
		// groupCred is the profile a session inherits inside the import.
		groupCred := func(parent string) string {
			for cur, guard := parent, 0; cur != "" && guard < 64; guard++ {
				g := byID[cur]
				if g == nil {
					return ""
				}
				if d := g.Defaults; d != nil && d.CredentialID != "" && d.CredentialID != store.Inherit {
					return d.CredentialID
				}
				cur = g.ParentID
			}
			return ""
		}
		copied := 0
		own := func(nodeID string, c *store.Credential, user *string, auth *store.AuthMethod, key *string, pwKey string) {
			*user, *auth, *key = c.User, c.Auth, c.KeyPath
			if pw, ok := res.Secrets[store.SecretCredPassword(c.ID)]; ok && pwKey != "" {
				if _, has := res.Secrets[pwKey]; !has {
					res.Secrets[pwKey] = pw
				}
			}
			copied++
		}
		for _, n := range res.Nodes {
			ss := n.Session
			if ss == nil {
				continue
			}
			id := ss.CredentialID
			if id == store.Inherit {
				if g := groupCred(n.ParentID); imported[g] != nil && yours(g) == "" {
					id = g // the group's imported login goes into the session
				}
			}
			if c := imported[id]; c != nil {
				if y := yours(id); y != "" && id == ss.CredentialID {
					ss.CredentialID = y
				} else {
					ss.CredentialID = ""
					own(n.ID, c, &ss.User, &ss.Auth, &ss.KeyPath, store.SecretPassword(n.ID))
					ss.CertPath = c.CertPath
				}
			}
			for i := range ss.Jump {
				j := &ss.Jump[i]
				if c := imported[j.CredentialID]; c != nil {
					if y := yours(j.CredentialID); y != "" {
						j.CredentialID = y
					} else {
						j.CredentialID = ""
						own(n.ID, c, &j.User, &j.Auth, &j.KeyPath, store.SecretJumpPassword(n.ID, i))
					}
				}
			}
			if px := ss.Proxy; px != nil && imported[px.CredentialID] != nil {
				px.CredentialID = yours(px.CredentialID)
			}
		}
		for _, n := range res.Nodes {
			if d := n.Defaults; d != nil {
				if imported[d.CredentialID] != nil {
					d.CredentialID = yours(d.CredentialID)
				}
				if px := d.Proxy; px != nil && imported[px.CredentialID] != nil {
					px.CredentialID = yours(px.CredentialID)
				}
			}
		}
		for k := range res.Secrets {
			if strings.HasPrefix(k, "cred/") {
				delete(res.Secrets, k)
			}
		}
		res.Credentials = nil
		rep.Profiles, rep.ProfilesKept = 0, 0
		if copied > 0 {
			rep.Warnings = append(rep.Warnings, fmt.Sprintf("credential profiles were not imported: %d login(s) went into the sessions themselves", copied))
		}
	}
	rep.Secrets = len(res.Secrets)
	return nil
}
