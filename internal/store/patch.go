// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (C) 2026 The YACM Authors. Additional terms under GPLv3 section 7: see NOTICE.md.

package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"
)

// Editing many sessions in the session editor: only the members the user
// changed are applied, each to every session. Lists (automation rules,
// saved commands, tunnels, tags, local commands) are added to each
// session's list, replace it, or have matching entries removed.

// ListMode says what happens to a list member of the patch.
const (
	ListAdd     = "add"
	ListReplace = "replace"
	ListRemove  = "remove"
)

// SessionPatch is a set of changes for many sessions.
type SessionPatch struct {
	// Session holds the new values; Set names the members (JSON names) that
	// apply. Members not in Set are left as they are in each session.
	Session Session  `json:"session"`
	Set     []string `json:"set"`
	// Lists gives the mode of each list member in Set.
	Lists map[string]string `json:"lists"`
	// ParentID moves the sessions into a group; Color sets their colour.
	ParentID *string `json:"parentId"`
	Color    *string `json:"color"`
	// DropLoginRules removes the rules that type a telnet login (<USER>,
	// <PASS>) from sessions that switch to SSH, which logs in itself.
	DropLoginRules bool `json:"dropLoginRules"`
}

// listMembers are the members that take a ListMode.
var listMembers = map[string]bool{"expect": true, "before": true, "after": true, "clusters": true, "macros": true, "tunnels": true}

// Members that are each session's own and never set for many at once.
var patchLocked = map[string]bool{"host": true}

// Members that only apply to some connection types.
var patchFor = map[string][]Protocol{
	"port":           {ProtoSSH, ProtoTelnet},
	"jump":           {ProtoSSH, ProtoTelnet},
	"jumpVia":        {ProtoSSH, ProtoTelnet},
	"proxy":          {ProtoSSH, ProtoTelnet},
	"connectTimeout": {ProtoSSH, ProtoTelnet},
	"auth":           {ProtoSSH},
	"keyPath":        {ProtoSSH},
	"certPath":       {ProtoSSH},
	"algorithms":     {ProtoSSH},
	"keepAlive":      {ProtoSSH},
	"agentForward":   {ProtoSSH},
	"tunnels":        {ProtoSSH},
	"serial":         {ProtoSerial},
}

// PatchApplied reports what a patch did.
type PatchApplied struct {
	Changed []string // session IDs
	// RuleIDs maps, per session, the ID of an automation rule in the patch
	// to its ID in that session (added rules get new IDs), for hidden values.
	RuleIDs map[string]map[string]string
	// Skipped names sessions left alone, with the reason.
	Skipped []string
}

// sessionFields maps JSON member names to struct field indexes.
var sessionFields = func() map[string]int {
	m := map[string]int{}
	t := reflect.TypeOf(Session{})
	for i := 0; i < t.NumField(); i++ {
		name, _, _ := strings.Cut(t.Field(i).Tag.Get("json"), ",")
		if name != "" && name != "-" {
			m[name] = i
		}
	}
	return m
}()

// PatchSessions applies p to the sessions ids in one save.
func (s *Store) PatchSessions(ids []string, p SessionPatch, newID func() string) (*PatchApplied, error) {
	for _, k := range p.Set {
		if _, ok := sessionFields[k]; !ok {
			return nil, fmt.Errorf("unknown session setting %q", k)
		}
		if patchLocked[k] {
			return nil, fmt.Errorf("%s is each session's own and cannot be set for many", k)
		}
		if listMembers[k] {
			switch p.Lists[k] {
			case ListAdd, ListReplace, ListRemove:
			default:
				return nil, fmt.Errorf("choose whether to add, replace or remove %s", k)
			}
		}
	}
	set := map[string]bool{}
	for _, k := range p.Set {
		set[k] = true
	}
	if set["protocol"] && p.Session.Protocol != ProtoSSH && p.Session.Protocol != ProtoTelnet {
		return nil, fmt.Errorf("sessions can only be switched to SSH or telnet, not %q", p.Session.Protocol)
	}
	if p.Color != nil && !ValidColor(*p.Color) {
		return nil, fmt.Errorf("unknown colour %q", *p.Color)
	}
	if newID == nil {
		newID = NewID
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.readOnly {
		return nil, ErrReadOnly
	}
	if p.ParentID != nil && *p.ParentID != "" {
		if g, ok := s.nodes[*p.ParentID]; !ok || !g.IsGroup {
			return nil, errors.New("target group not found")
		}
	}
	res := &PatchApplied{RuleIDs: map[string]map[string]string{}}
	type change struct{ old, new *Node }
	var changes []change
	for _, id := range ids {
		old, ok := s.nodes[id]
		if !ok || old.Session == nil {
			continue // team library sessions are read-only
		}
		if !KnownProtocol(old.Session.Protocol) {
			res.Skipped = append(res.Skipped, old.Name+": a connection type from a newer YACM")
			continue
		}
		n := clone(old)
		rules, err := patchOne(n.Session, p, set, newID)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", old.Name, err)
		}
		if p.Color != nil {
			n.Color = *p.Color
		}
		if p.ParentID != nil && n.ParentID != *p.ParentID {
			n.ParentID = *p.ParentID
		}
		if n.Session.JumpVia == id {
			n.Session.JumpVia = old.Session.JumpVia // a session is never its own jump host
		}
		if err := Validate(n); err != nil {
			return nil, fmt.Errorf("%s: %w", old.Name, err)
		}
		changes = append(changes, change{old, n})
		res.RuleIDs[id] = rules
	}
	now := time.Now().UTC()
	for _, c := range changes {
		if c.new.ParentID != c.old.ParentID {
			c.new.Order = s.nextOrder(c.new.ParentID)
		}
		c.new.Updated = now
		s.nodes[c.new.ID] = c.new
		res.Changed = append(res.Changed, c.new.ID)
	}
	if len(changes) == 0 {
		return res, nil
	}
	return res, s.saveLocked()
}

// patchOne applies p to one session; it returns the rule ID mapping.
func patchOne(ss *Session, p SessionPatch, set map[string]bool, newID func() string) (map[string]string, error) {
	rules := map[string]string{}
	proto := ss.Protocol
	if set["protocol"] && (proto == ProtoSSH || proto == ProtoTelnet) && p.Session.Protocol != proto {
		ss.Protocol = p.Session.Protocol
		if !set["port"] && (ss.Port == 0 || ss.Port == DefaultPort(proto)) {
			ss.Port = DefaultPort(ss.Protocol)
		}
		if ss.Protocol == ProtoSSH && ss.Algorithms == "" && !set["algorithms"] {
			ss.Algorithms = Inherit
		}
		if ss.Protocol == ProtoSSH && p.DropLoginRules {
			ss.Expect = removeWhere(ss.Expect, IsLoginRule)
		}
		proto = ss.Protocol
	}
	dst := reflect.ValueOf(ss).Elem()
	src := reflect.ValueOf(&p.Session).Elem()
	for k := range set {
		if k == "protocol" {
			continue
		}
		if only, ok := patchFor[k]; ok && !hasProto(only, proto) {
			continue
		}
		i := sessionFields[k]
		switch {
		case k == "serial":
			ss.Serial = mergeSerial(ss.Serial, p.Session.Serial)
		case listMembers[k]:
			patchList(ss, k, p, p.Lists[k], newID, rules)
		default:
			dst.Field(i).Set(deepCopy(src.Field(i)))
		}
	}
	return rules, nil
}

func hasProto(list []Protocol, p Protocol) bool {
	for _, x := range list {
		if x == p {
			return true
		}
	}
	return false
}

// mergeSerial sets the line settings and keeps each session's own port.
func mergeSerial(old, to *Serial) *Serial {
	if to == nil {
		return old
	}
	out := *deepCopy(reflect.ValueOf(to)).Interface().(*Serial)
	if old != nil {
		out.Device = old.Device
		out.Extra = old.Extra
	}
	return &out
}

// deepCopy copies a value through JSON (lists, pointers, nested members).
func deepCopy(v reflect.Value) reflect.Value {
	data, _ := json.Marshal(v.Interface())
	out := reflect.New(v.Type())
	_ = json.Unmarshal(data, out.Interface())
	return out.Elem()
}

// patchList adds, replaces or removes list entries of member k.
func patchList(ss *Session, k string, p SessionPatch, mode string, newID func() string, rules map[string]string) {
	switch k {
	case "expect":
		in := deepCopy(reflect.ValueOf(p.Session.Expect)).Interface().([]ExpectRule)
		switch mode {
		case ListRemove:
			ss.Expect = removeWhere(ss.Expect, func(r ExpectRule) bool {
				for _, x := range in {
					if r.Expect == x.Expect && r.Hidden == x.Hidden && (r.Hidden || r.Send == x.Send) {
						return true
					}
				}
				return false
			})
		default:
			if mode == ListAdd {
				// A visible rule the session already has is not added twice.
				in = removeWhere(in, func(x ExpectRule) bool {
					for _, r := range ss.Expect {
						if !x.Hidden && !r.Hidden && r.Expect == x.Expect && r.Send == x.Send {
							return true
						}
					}
					return false
				})
			}
			for i := range in {
				id := newID()
				rules[in[i].ID] = id
				in[i].ID = id
			}
			if mode == ListReplace {
				ss.Expect = in
			} else {
				ss.Expect = append(ss.Expect, in...)
			}
		}
	case "before", "after":
		in := deepCopy(reflect.ValueOf(p.Session.Before)).Interface().([]LocalCommand)
		cur := &ss.Before
		if k == "after" {
			in = deepCopy(reflect.ValueOf(p.Session.After)).Interface().([]LocalCommand)
			cur = &ss.After
		}
		*cur = patchSimple(*cur, in, mode, func(a, b LocalCommand) bool { return strings.TrimSpace(a.Command) == strings.TrimSpace(b.Command) })
	case "macros":
		in := deepCopy(reflect.ValueOf(p.Session.Macros)).Interface().([]Macro)
		ss.Macros = patchSimple(ss.Macros, in, mode, func(a, b Macro) bool { return a.Name == b.Name && a.Text == b.Text })
	case "clusters":
		in := append([]string(nil), p.Session.Clusters...)
		switch mode {
		case ListReplace:
			ss.Clusters = editClusters(nil, in, nil)
		case ListAdd:
			ss.Clusters = editClusters(ss.Clusters, in, nil)
		case ListRemove:
			ss.Clusters = editClusters(ss.Clusters, nil, in)
		}
	case "tunnels":
		in := deepCopy(reflect.ValueOf(p.Session.Tunnels)).Interface().([]Tunnel)
		if mode != ListRemove {
			for i := range in {
				in[i].ID = newID()
			}
		}
		ss.Tunnels = patchSimple(ss.Tunnels, in, mode, func(a, b Tunnel) bool {
			return a.Type == b.Type && a.Port == b.Port && a.Host == b.Host && a.HostPort == b.HostPort
		})
	}
}

func patchSimple[T any](cur, in []T, mode string, same func(a, b T) bool) []T {
	switch mode {
	case ListReplace:
		return in
	case ListAdd:
		// An entry the session already has is not added twice.
		return append(cur, removeWhere(in, func(y T) bool {
			for _, x := range cur {
				if same(x, y) {
					return true
				}
			}
			return false
		})...)
	default:
		return removeWhere(cur, func(x T) bool {
			for _, y := range in {
				if same(x, y) {
					return true
				}
			}
			return false
		})
	}
}

func removeWhere[T any](list []T, drop func(T) bool) []T {
	out := list[:0:0]
	for _, x := range list {
		if !drop(x) {
			out = append(out, x)
		}
	}
	return out
}
