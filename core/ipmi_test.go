// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (C) 2026 The YACM Authors. Additional terms under GPLv3 section 7: see NOTICE.md.

package core

import (
	"context"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/bougou/go-ipmi/pkg/bmc"
	"github.com/bougou/go-ipmi/pkg/clock"
	"github.com/bougou/go-ipmi/pkg/hal/mock"
	"github.com/bougou/go-ipmi/pkg/server"
	"github.com/bougou/go-ipmi/pkg/transport/udp"
)

// fakeBMC starts a simulated BMC with a console on a loopback UDP port.
func fakeBMC(t *testing.T) (*bmc.BMC, *mock.FakeConsoleConn, int) {
	t.Helper()
	fake := &mock.FakeConsoleConn{}
	m := mock.New()
	m.SetConsole(&mock.Console{Conn: fake})
	b := bmc.New(bmc.DeviceInfo{IPMIVersion: 0x20}, [16]byte{}, m, bmc.WithClock(clock.Real))
	u, err := b.Users.Add(2, "jsmith")
	if err != nil {
		t.Fatal(err)
	}
	u.SetPassword([]byte("s3cret"))
	u.Enabled = true
	u.ChannelAccess[1] = bmc.UserChannelAccess{MaxPrivilege: bmc.PrivilegeLevelAdministrator, Enabled: true}
	pc, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	srv := server.NewServer(b, udp.Wrap(pc))
	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = srv.Serve(ctx) }()
	t.Cleanup(func() { cancel(); _ = srv.Close(); _ = pc.Close() })
	return b, fake, pc.LocalAddr().(*net.UDPAddr).Port
}

func TestSOLConsole(t *testing.T) {
	b, fake, port := fakeBMC(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	s, err := dialSOL(ctx, "127.0.0.1", port, "jsmith", "s3cret", 0, 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	fake.FeedRX([]byte("server01 login: "))
	got := make(chan string, 1)
	go func() {
		var all []byte
		buf := make([]byte, 256)
		for !strings.Contains(string(all), "login: ") {
			n, err := s.Read(buf)
			all = append(all, buf[:n]...)
			if err != nil {
				break
			}
		}
		got <- string(all)
	}()
	select {
	case out := <-got:
		if !strings.Contains(out, "server01 login: ") {
			t.Fatalf("console output %q", out)
		}
	case <-ctx.Done():
		t.Fatal("no console output")
	}
	if _, err := s.Write([]byte("root\r")); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, func() bool { return fake.TXString() == "root\r" })
	if s.SendBreak() == nil {
		t.Fatal("break should be refused")
	}
	_ = s.Close()
	waitUntil(t, func() bool { return b.SOL.ActiveSessionID(1) == 0 })
	if _, err := s.Read(make([]byte, 8)); err == nil {
		t.Fatal("read after close")
	} else if err != io.EOF && err != io.ErrClosedPipe {
		t.Logf("read after close: %v", err)
	}
}

func TestSOLWrongPassword(t *testing.T) {
	_, _, port := fakeBMC(t)
	_, err := dialSOL(context.Background(), "127.0.0.1", port, "jsmith", "wrong", 0, 6*time.Second)
	if err == nil {
		t.Fatal("wrong password accepted")
	}
	if !strings.Contains(err.Error(), "password") {
		t.Fatalf("error does not say what to check: %v", err)
	}
}

func waitUntil(t *testing.T, ok func() bool) {
	t.Helper()
	for end := time.Now().Add(5 * time.Second); time.Now().Before(end); time.Sleep(20 * time.Millisecond) {
		if ok() {
			return
		}
	}
	t.Fatal("timed out")
}
