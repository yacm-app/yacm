// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (C) 2026 The YACM Authors. Additional terms under GPLv3 section 7: see NOTICE.md.

package localterm

import (
	"bytes"
	"io"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestArgv(t *testing.T) {
	got, err := Argv(Options{Kind: KindKubectl, Target: "web-7f9c", Namespace: "prod", Container: "app", Context: "eu-1", Command: "bash -l"})
	want := []string{"kubectl", "--context", "eu-1", "exec", "-it", "-n", "prod", "web-7f9c", "-c", "app", "--", "bash", "-l"}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("%v %q", err, got)
	}
	got, _ = Argv(Options{Kind: KindDocker, Target: "netbox"})
	if !reflect.DeepEqual(got, []string{"docker", "exec", "-it", "netbox", "sh"}) {
		t.Fatalf("%q", got)
	}
	for _, bad := range []Options{
		{Kind: KindDocker, Target: "--privileged"},
		{Kind: KindDocker, Target: "a b"},
		{Kind: KindKubectl, Target: "pod", Namespace: "x;rm"},
		{Kind: KindDocker, Target: "c", Command: "sh\nrm -rf /"},
		{Kind: KindShell, Shell: "rm -rf"},
		{Kind: "ftp"},
	} {
		if _, err := Argv(bad); err == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
}

func TestShellRuns(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs a POSIX shell")
	}
	c, err := Start(Options{Kind: KindShell, Shell: "/bin/sh"}, 100, 30)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := c.Resize(120, 40); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Write([]byte("stty size; echo marker-$((40+2)); exit\n")); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	done := make(chan error, 1)
	go func() { _, err := io.Copy(&out, c); done <- err }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("no end of output: %q", out.String())
	}
	s := out.String()
	if !strings.Contains(s, "marker-42") || !strings.Contains(s, "40 120") {
		t.Fatalf("output %q", s)
	}
}
