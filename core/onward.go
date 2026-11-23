// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (C) 2026 The YACM Authors. Additional terms under GPLv3 section 7: see NOTICE.md.

package core

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/yacm-app/yacm/internal/expect"
	"github.com/yacm-app/yacm/internal/sshconn"
	"github.com/yacm-app/yacm/internal/store"
)

// Going on from a jump host by typing a command (store.OnwardOptions mode
// "command"): the tab is an SSH shell on the jump host. onwardWatch reads
// its output, types the command at the jump host's prompt, answers the
// device's password prompt once, and tells when the device was reached,
// when that failed (the tab then disconnects instead of leaving you on the
// jump host under the device's name), and when the device closed the
// connection later (back on the jump host: the tab disconnects too).

type onwardEvent int

const (
	onwardNone    onwardEvent = iota
	onwardReached             // the device answered; automation may start
	onwardFailed              // the jump host could not reach the device
	onwardClosed              // the device closed the connection
)

var (
	// A shell or CLI prompt at the end of the output.
	onwardPromptRe = regexp.MustCompile(`(?m)^[^\r\n]{0,80}[$#>%]\s*$`)
	onwardPassRe   = regexp.MustCompile(`(?i)pass(word|code|phrase)[^\r\n]{0,40}:\s*$`)
	onwardUserRe   = regexp.MustCompile(`(?i)(username|login|user name)\s*:\s*$`)
	onwardFailRe   = regexp.MustCompile(`(?im)^.*(connection refused|connection timed out|timed out|no route to host|network is unreachable|could not resolve|name or service not known|unknown host|host key verification failed|permission denied|authentication failed|access denied|destination unreachable|remote host not responding|bad ip address|invalid input detected|unknown command|command not found|no such host).*$`)
	onwardCloseRe  = regexp.MustCompile(`(?im)(connection to \S+ closed|closed by (foreign|remote) host|connection closed by|^connection closed)`)
)

// onwardTimeout: without a failure or a prompt by then, the device is
// taken as reached (a slow login banner, a host key question).
const onwardTimeout = 60 * time.Second

type onwardWatch struct {
	cmd, pass, user, via string
	telnet               bool
	phase                int // 0 at the jump host, 1 command typed, 2 at the device
	jumpPrompt           string
	sentAt               time.Time
	passSent, userSent   bool
	strip                expect.Stripper
	buf                  []byte
	failLine             string
}

func newOnward(cmd, user, pass, via string, telnet bool) *onwardWatch {
	return &onwardWatch{cmd: cmd, user: user, pass: pass, via: via, telnet: telnet}
}

// feed takes output; it returns text to type (or "") and what happened.
func (w *onwardWatch) feed(chunk []byte, now time.Time) (string, onwardEvent) {
	if w.phase > 2 {
		return "", onwardNone
	}
	w.buf = append(w.buf, w.strip.Strip(chunk)...)
	if len(w.buf) > 4096 {
		w.buf = append([]byte(nil), w.buf[len(w.buf)-2048:]...)
	}
	text := string(w.buf)
	switch w.phase {
	case 0:
		if loc := onwardPromptRe.FindStringIndex(text); loc != nil && strings.TrimSpace(text[loc[1]:]) == "" {
			w.jumpPrompt = strings.TrimSpace(text[loc[0]:loc[1]])
			w.phase, w.sentAt, w.buf = 1, now, nil
			return w.cmd + "\r", onwardNone
		}
	case 1:
		if m := onwardFailRe.FindString(text); m != "" && !strings.Contains(m, w.cmd) {
			w.failLine = strings.TrimSpace(m)
			w.phase = 3
			return "", onwardFailed
		}
		if onwardCloseRe.MatchString(text) {
			w.failLine = "the device closed the connection"
			w.phase = 3
			return "", onwardFailed
		}
		if !w.passSent && onwardPassRe.MatchString(text) {
			w.passSent, w.buf = true, nil
			if w.pass == "" {
				return "", onwardNone // the user types it
			}
			return w.pass + "\r", onwardNone
		}
		if w.telnet && !w.userSent && w.user != "" && onwardUserRe.MatchString(text) {
			w.userSent, w.buf = true, nil
			return w.user + "\r", onwardNone
		}
		if loc := onwardPromptRe.FindStringIndex(text); loc != nil && strings.TrimSpace(text[loc[1]:]) == "" {
			p := strings.TrimSpace(text[loc[0]:loc[1]])
			// The jump host's own prompt back again means the command did
			// not get anywhere (and printed no known error).
			if p == w.jumpPrompt && !w.passSent && !w.userSent {
				w.failLine = "back at the jump host's prompt"
				w.phase = 3
				return "", onwardFailed
			}
			w.phase, w.buf = 2, nil
			return "", onwardReached
		}
		if now.Sub(w.sentAt) > onwardTimeout {
			w.phase, w.buf = 2, nil
			return "", onwardReached
		}
	case 2:
		if onwardCloseRe.MatchString(text) {
			w.phase = 3
			return "", onwardClosed
		}
	}
	return "", onwardNone
}

// expandOnward fills the tokens of an onward command.
func expandOnward(cmd, user, host string, port int, def int) string {
	if port == 0 {
		port = def
	}
	if user == "" {
		// No user: leave "-l <USER>" out rather than type an empty name.
		cmd = strings.ReplaceAll(cmd, "-l <USER> ", "")
		cmd = strings.ReplaceAll(cmd, "<USER>@", "")
	}
	r := strings.NewReplacer("<USER>", user, "<HOST>", host, "<PORT>", strconv.Itoa(port))
	return r.Replace(cmd)
}

// dialOnward opens an SSH shell on the session's last jump host; the read
// loop then types the command that goes on to the device (feedOnward).
func (a *App) dialOnward(ctx context.Context, t *tab, node *store.Node, eff *store.Effective, timeout time.Duration) (conn, string, error) {
	ss := node.Session
	hops := a.jumpHops(t, node, eff)
	if len(hops) == 0 {
		return nil, "", errors.New("going on from the jump host by a command needs a jump host session")
	}
	last := hops[len(hops)-1]
	profile := last.Profile
	if profile == "" {
		profile = store.AlgoSecure
	}
	if p := proxyText(eff); p != "" {
		a.tabLog(t, "connecting %s", p)
	}
	sc, err := sshconn.Dial(ctx, sshconn.Config{
		Hops: hops, Profile: profile, ConnectTimeout: timeout,
		KeepAlive: 30 * time.Second, Cols: t.cols, Rows: t.rows,
		KnownHosts: a.kh, Prompter: tabPrompter{a, t.id, node.Name},
		SaveSecret: func(name, v string) error { return a.vault.Set(name, v) },
		SaveUser:   a.saveUser,
		Status:     func(m string) { a.tabLog(t, "%s", m) },
		Dial:       a.proxyDial(eff),
	})
	if err != nil {
		return nil, "", err
	}
	cmd := expandOnward(eff.Onward, eff.User, ss.Host, ss.Port, store.DefaultPort(ss.Protocol))
	a.tabLog(t, "on %s, going on to the device with: %s", eff.OnwardVia, cmd)
	t.mu.Lock()
	t.onward = newOnward(cmd, eff.User, a.secret(eff.PasswordKey), eff.OnwardVia, ss.Protocol == store.ProtoTelnet)
	t.mu.Unlock()
	if ss.Protocol == store.ProtoTelnet {
		return sc, "telnet · through jump host " + last.Host, nil
	}
	return sc, sc.Summary(), nil
}

// feedOnward lets the onward watch see output; it returns a message when
// the tab is to end with an error.
func (a *App) feedOnward(t *tab, c conn, chunk []byte) string {
	t.mu.Lock()
	w := t.onward
	t.mu.Unlock()
	if w == nil {
		return ""
	}
	send, ev := w.feed(chunk, time.Now())
	if send != "" {
		t.touchInput()
		if _, err := c.Write([]byte(send)); err != nil {
			return ""
		}
	}
	host := t.node.Session.Host
	switch ev {
	case onwardReached:
		t.mu.Lock()
		eng := t.pending
		t.engine, t.pending = eng, nil
		t.mu.Unlock()
		if eng != nil {
			eng.Start()
		}
	case onwardFailed:
		msg := fmt.Sprintf("%s could not reach %s (%s), so the tab was disconnected: it was still on the jump host", w.via, host, w.failLine)
		a.tabLog(t, "%s", msg)
		_ = c.Close()
		return msg
	case onwardClosed:
		a.tabLog(t, "%s closed the connection; the tab was disconnected instead of staying on the jump host %s", host, w.via)
		_ = c.Close()
	}
	return ""
}
