// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (C) 2026 The YACM Authors. Additional terms under GPLv3 section 7: see NOTICE.md.

//go:build linux

package fileserver

import (
	"net"
	"os"
	"strconv"
	"strings"
	"testing"
)

// The test program plays YACM's program for the bind helper.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == BindHelperArg {
		os.Exit(BindHelper(os.Args[2:]))
	}
	os.Exit(m.Run())
}

func TestBindHelper(t *testing.T) {
	if os.Geteuid() != 0 && !lowPortsOpen(99) {
		t.Skip("needs root or open low ports to open a port below 1024")
	}
	// UDP: the socket that comes back receives.
	f, err := helperBind("udp", "127.0.0.1:99")
	if err != nil {
		t.Fatal(err)
	}
	pc, err := net.FilePacketConn(f)
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	defer pc.Close()
	if a := pc.LocalAddr().String(); a != "127.0.0.1:99" {
		t.Fatalf("bound %s", a)
	}
	c, _ := net.Dial("udp", "127.0.0.1:99")
	c.Write([]byte("hi"))
	c.Close()
	buf := make([]byte, 8)
	if n, _, err := pc.ReadFrom(buf); err != nil || string(buf[:n]) != "hi" {
		t.Fatalf("read %q %v", buf[:n], err)
	}
	// TCP.
	f, err = helperBind("tcp", "127.0.0.1:98")
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.FileListener(f)
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	ln.Close()
	// Only ports below 1024.
	if _, err := helperBind("tcp", "127.0.0.1:8080"); err == nil || !strings.Contains(err.Error(), "below 1024") {
		t.Fatalf("high port: %v", err)
	}
	// A port in use: the helper's error comes back.
	if _, err := helperBind("udp", "127.0.0.1:"+strconv.Itoa(99)); err == nil {
		t.Fatal("bound a port in use")
	}
}
