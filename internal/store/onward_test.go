// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (C) 2026 The YACM Authors. Additional terms under GPLv3 section 7: see NOTICE.md.

package store

import (
	"path/filepath"
	"testing"
)

func TestOnwardCommands(t *testing.T) {
	for _, ok := range []string{"", DefaultOnwardSSH, DefaultOnwardTelnet, "ssh -o StrictHostKeyChecking=ask <USER>@<HOST>", "connect <HOST> <PORT>", "telnet <HOST> <PORT> /vrf MGMT"} {
		if err := ValidOnwardCommand(ok); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
	for _, bad := range []string{"ssh <HOST>; rm -rf ~", "ssh $(id) <HOST>", "curl <HOST>", "ssh -l <USER>", "ssh <HOST>\ncat /etc/passwd", "ssh <HOST> > /tmp/x", "ssh <HOST> | tee x"} {
		if err := ValidOnwardCommand(bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}

func TestEffectiveOnward(t *testing.T) {
	s, _ := Open(filepath.Join(t.TempDir(), "sessions.json"))
	j, err := s.Upsert(&Node{Name: "core-rtr", Session: &Session{Protocol: ProtoSSH, Host: "10.0.0.1", Onward: &OnwardOptions{Mode: "command", Telnet: "telnet <HOST> /vrf MGMT"}}})
	if err != nil {
		t.Fatal(err)
	}
	dev, _ := s.Upsert(&Node{Name: "old-sw", Session: &Session{Protocol: ProtoSSH, Host: "10.9.9.9", JumpVia: j.ID}})
	tel, _ := s.Upsert(&Node{Name: "older-sw", Session: &Session{Protocol: ProtoTelnet, Host: "10.9.9.8", JumpVia: j.ID}})
	e, err := s.Effective(dev)
	if err != nil || e.Onward != DefaultOnwardSSH || e.OnwardVia != "core-rtr" || len(e.Hops) != 1 {
		t.Fatalf("%v %+v", err, e)
	}
	e, _ = s.Effective(tel)
	if e.Onward != "telnet <HOST> /vrf MGMT" {
		t.Fatalf("telnet: %q", e.Onward)
	}
	// Forwarding (the default) leaves nothing to type.
	n, _ := s.Get(j.ID)
	n.Session.Onward = &OnwardOptions{}
	s.Upsert(n)
	if n, _ := s.Get(j.ID); n.Session.Onward != nil {
		t.Fatal("an empty mode was kept")
	}
	if e, _ := s.Effective(dev); e.Onward != "" {
		t.Fatalf("onward %q", e.Onward)
	}
}
