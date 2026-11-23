// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (C) 2026 The YACM Authors. Additional terms under GPLv3 section 7: see NOTICE.md.

package core

import (
	"bufio"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/yacm-app/yacm/internal/fakedevice"
	"github.com/yacm-app/yacm/internal/store"
)

func TestOnwardWatch(t *testing.T) {
	now := time.Now()
	feed := func(w *onwardWatch, s string) (string, onwardEvent) { return w.feed([]byte(s), now) }

	// Linux jump host, SSH with a password.
	w := newOnward("ssh -l admin -p 22 10.1.1.1", "admin", "pw", "bastion", false)
	if send, _ := feed(w, "Last login: today\r\n\x1b[1mjsmith@bastion:~$ \x1b[0m"); send != "ssh -l admin -p 22 10.1.1.1\r" {
		t.Fatalf("command %q", send)
	}
	feed(w, "ssh -l admin -p 22 10.1.1.1\r\n")
	if send, _ := feed(w, "(admin@10.1.1.1) Password: "); send != "pw\r" {
		t.Fatalf("password %q", send)
	}
	if _, ev := feed(w, "\r\n\r\ncore-sw1#"); ev != onwardReached {
		t.Fatalf("not reached: %v", ev)
	}
	if _, ev := feed(w, "exit\r\nConnection to 10.1.1.1 closed.\r\njsmith@bastion:~$ "); ev != onwardClosed {
		t.Fatalf("close not seen: %v", ev)
	}

	// Cisco router as jump host, telnet with user name and password.
	w = newOnward("telnet 10.2.2.2 23", "netops", "s3cret", "core-rtr", true)
	if send, _ := feed(w, "\r\ncore-rtr>"); send == "" {
		t.Fatal("no command at a > prompt")
	}
	feed(w, "Trying 10.2.2.2 ... Open\r\n\r\nUser Access Verification\r\n\r\n")
	if send, _ := feed(w, "Username: "); send != "netops\r" {
		t.Fatalf("user %q", send)
	}
	if send, _ := feed(w, "Password: "); send != "s3cret\r" {
		t.Fatalf("password %q", send)
	}
	if _, ev := feed(w, "\r\nold-sw>"); ev != onwardReached {
		t.Fatal("not reached")
	}

	// The jump host cannot reach the device.
	w = newOnward("ssh -l admin -p 22 10.3.3.3", "admin", "pw", "core-rtr", false)
	feed(w, "core-rtr#")
	if _, ev := feed(w, "\r\n% Connection refused by remote host\r\ncore-rtr#"); ev != onwardFailed || !strings.Contains(w.failLine, "refused") {
		t.Fatalf("%v %q", ev, w.failLine)
	}
	// No known message, but the jump host's prompt came back.
	w = newOnward("ssh 10.4.4.4", "", "", "j", false)
	feed(w, "j$ ")
	if _, ev := feed(w, "\r\nj$ "); ev != onwardFailed {
		t.Fatalf("back at the prompt: %v", ev)
	}
	// A wrong password.
	w = newOnward("ssh 10.5.5.5", "admin", "bad", "j", false)
	feed(w, "j$ ")
	feed(w, "admin@10.5.5.5's password: ")
	if _, ev := feed(w, "\r\nPermission denied, please try again.\r\n"); ev != onwardFailed {
		t.Fatalf("wrong password: %v", ev)
	}

	if got := expandOnward(store.DefaultOnwardSSH, "", "10.0.0.1", 0, 22); got != "ssh -p 22 10.0.0.1" {
		t.Fatalf("no user: %q", got)
	}
}

// jumpRouter is a jump host shell that cannot forward but knows "ssh".
func jumpRouter(t *testing.T) int {
	t.Helper()
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	signer, _ := ssh.NewSignerFromKey(priv)
	srv := &fakedevice.Server{Config: fakedevice.PasswordConfig("jsmith", "jumppw", signer), Shell: func(rw io.ReadWriter) {
		r := bufio.NewReader(rw)
		line := func() string { s, _ := r.ReadString('\r'); return strings.TrimSpace(s) }
		for {
			io.WriteString(rw, "\r\ncore-rtr#")
			cmd := line()
			switch {
			case cmd == "":
				return
			case cmd == "ssh -l admin -p 22 10.9.9.9":
				io.WriteString(rw, "\r\nPassword: ")
				if line() != "devpw" {
					io.WriteString(rw, "\r\n% Authentication failed")
					continue
				}
				io.WriteString(rw, "\r\n\r\nsw9#")
				for line() != "exit" {
					io.WriteString(rw, "\r\nsw9#")
				}
				io.WriteString(rw, "\r\n[Connection to 10.9.9.9 closed by foreign host]")
			default:
				io.WriteString(rw, "\r\n% Connection refused by remote host")
			}
		}
	}}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go srv.Serve(ln)
	return ln.Addr().(*net.TCPAddr).Port
}

func TestOnwardThroughJumpRouter(t *testing.T) {
	a := newTestApp(t)
	var mu sync.Mutex
	var out strings.Builder
	a.emitter = func(name string, data any) {
		switch name {
		case "prompt":
			p := data.(map[string]any)
			go a.AnswerPrompt(p["id"].(string), PromptAnswer{Accept: true})
		case "tab:data":
			b, _ := base64.StdEncoding.DecodeString(data.(map[string]string)["data"])
			mu.Lock()
			out.Write(b)
			mu.Unlock()
		}
	}
	port := jumpRouter(t)
	jump, err := a.SaveNode(store.Node{Name: "core-rtr", Session: &store.Session{Protocol: store.ProtoSSH, Host: "127.0.0.1", Port: port, User: "jsmith", Auth: store.AuthPassword,
		Onward: &store.OnwardOptions{Mode: "command"}}}, &SecretInput{Password: sp("jumppw")})
	if err != nil {
		t.Fatal(err)
	}
	dev, _ := a.SaveNode(store.Node{Name: "sw9", Session: &store.Session{Protocol: store.ProtoSSH, Host: "10.9.9.9", User: "admin", JumpVia: jump.ID,
		Expect: []store.ExpectRule{{ID: "r", Active: true, Expect: `sw9#\s*$`, Send: "show clock", Return: true, OnFail: "stop"}}}}, &SecretInput{Password: sp("devpw")})
	id, err := a.Connect(ConnectRequest{NodeID: dev.ID, Cols: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	waitState(t, a, id, "connected")
	wait := func(s string) {
		t.Helper()
		for i := 0; i < 400; i++ {
			mu.Lock()
			ok := strings.Contains(out.String(), s)
			mu.Unlock()
			if ok {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		mu.Lock()
		defer mu.Unlock()
		t.Fatalf("no %q in %q", s, out.String())
	}
	wait("sw9#")
	// The automation ran on the device, not on the jump host.
	tb, _ := a.getTab(id)
	tb.mu.Lock()
	eng := tb.engine
	tb.mu.Unlock()
	if eng == nil {
		t.Fatal("automation never started")
	}
	if err := a.Write(id, "exit\r"); err != nil {
		t.Fatal(err)
	}
	waitState(t, a, id, "closed")

	// A device the router cannot reach: the tab ends with an error.
	bad, _ := a.SaveNode(store.Node{Name: "gone", Session: &store.Session{Protocol: store.ProtoSSH, Host: "10.9.9.1", User: "admin", JumpVia: jump.ID}}, nil)
	id, err = a.Connect(ConnectRequest{NodeID: bad.ID, Cols: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	waitState(t, a, id, "error")
}
