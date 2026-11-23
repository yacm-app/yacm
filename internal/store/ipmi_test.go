// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (C) 2026 The YACM Authors. Additional terms under GPLv3 section 7: see NOTICE.md.

package store

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestIPMISessions(t *testing.T) {
	n := &Node{Name: "esx01", Session: &Session{Protocol: ProtoIPMI, Host: "10.1.1.20", IPMI: &IPMIOptions{CipherSuite: 17},
		Tunnels: []Tunnel{{ID: "t1", Type: "local"}}, Proxy: &Proxy{Type: "socks5", Host: "p", Port: 1080}}}
	if err := Validate(n); err != nil {
		t.Fatal(err)
	}
	if ss := n.Session; ss.Port != 623 || len(ss.Tunnels) != 0 || ss.Proxy != nil || ss.IPMI.CipherSuite != 17 {
		t.Fatalf("%+v", ss)
	}
	if !KnownProtocol(ProtoIPMI) {
		t.Fatal("ipmi unknown")
	}
	for _, ss := range []*Session{{Protocol: ProtoIPMI}, {Protocol: ProtoIPMI, Host: "h", IPMI: &IPMIOptions{CipherSuite: 40}}} {
		if Validate(&Node{Name: "x", Session: ss}) == nil {
			t.Errorf("accepted %+v", ss)
		}
	}
	// Leaving the type drops its settings.
	n.Session.Protocol = ProtoSSH
	if err := Validate(n); err != nil || n.Session.IPMI != nil {
		t.Fatalf("%v %+v", err, n.Session.IPMI)
	}
	// Members of a newer YACM survive a round trip.
	var o IPMIOptions
	if err := json.Unmarshal([]byte(`{"cipherSuite":3,"privilege":"operator"}`), &o); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(o)
	if !strings.Contains(string(b), `"privilege":"operator"`) {
		t.Fatalf("lost: %s", b)
	}
}
