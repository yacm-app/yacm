// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (C) 2026 The YACM Authors. Additional terms under GPLv3 section 7: see NOTICE.md.

package core

import (
	"encoding/base64"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yacm-app/yacm/internal/store"
)

// A local session runs a shell in a tab like any other session.
func TestLocalShellSession(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs a POSIX shell")
	}
	a := newTestApp(t)
	var mu sync.Mutex
	var out strings.Builder
	a.emitter = func(name string, data any) {
		if name != "tab:data" {
			return
		}
		b, _ := base64.StdEncoding.DecodeString(data.(map[string]string)["data"])
		mu.Lock()
		out.Write(b)
		mu.Unlock()
	}
	n, err := a.SaveNode(store.Node{Name: "shell", Session: &store.Session{Protocol: store.ProtoLocal, Host: "ignored", Local: &store.LocalOptions{Kind: "shell", Shell: "/bin/sh"}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if n.Session.Host != "" || n.Session.Port != 0 {
		t.Fatalf("a local session kept an address: %+v", n.Session)
	}
	id, err := a.Connect(ConnectRequest{NodeID: n.ID, Cols: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	waitState(t, a, id, "connected")
	if err := a.Write(id, "echo local-$((20+22))\n"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		mu.Lock()
		s := out.String()
		mu.Unlock()
		if strings.Contains(s, "local-42") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no output: %q", s)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := a.Write(id, "exit\n"); err != nil {
		t.Fatal(err)
	}
	waitState(t, a, id, "closed")
}

func TestQuickLocal(t *testing.T) {
	n, err := parseQuick("local://")
	if err != nil || n.Session.Protocol != store.ProtoLocal || n.Session.Local.Kind != "shell" {
		t.Fatalf("%v %+v", err, n)
	}
	if _, err := parseQuick("local://rm"); err == nil {
		t.Fatal("an unknown shell name was accepted")
	}
}
