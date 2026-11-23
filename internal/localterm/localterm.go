// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (C) 2026 The YACM Authors. Additional terms under GPLv3 section 7: see NOTICE.md.

// Package localterm runs a program on this computer in a pseudo terminal
// for a tab: a local shell (PowerShell, cmd, WSL, bash, zsh…) or a shell in
// a container (docker exec, kubectl exec).
package localterm

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

// Kinds of local sessions.
const (
	KindShell   = "shell"
	KindDocker  = "docker"
	KindKubectl = "kubectl"
)

// Options say what to run. They mirror store.LocalOptions.
type Options struct {
	Kind      string
	Shell     string // KindShell: "" (the system's), a name from Shells, or a path
	Dir       string // KindShell: starting folder; "" = home
	Target    string // docker: container; kubectl: pod
	Namespace string // kubectl
	Container string // kubectl: container in the pod
	Context   string // docker or kubectl context
	Command   string // docker/kubectl: program in the container, "" = sh
}

// Shells are the shells offered by name; a path to another one works too.
var Shells = map[string]string{
	"powershell": "PowerShell", "pwsh": "PowerShell 7", "cmd": "Command Prompt", "wsl": "WSL",
	"bash": "bash", "zsh": "zsh", "fish": "fish", "sh": "sh",
}

// safeArg: names that go on a command line (container, pod, namespace,
// context) are plain words.
func safeArg(s string) bool {
	if s == "" || strings.HasPrefix(s, "-") {
		return false
	}
	for _, r := range s {
		if !(r == '-' || r == '_' || r == '.' || r == ':' || r == '/' || r == '@' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z') {
			return false
		}
	}
	return true
}

// Validate checks the options without starting anything.
func Validate(o Options) error {
	switch o.Kind {
	case KindShell:
		if o.Shell != "" && Shells[o.Shell] == "" && !filepath.IsAbs(o.Shell) {
			return fmt.Errorf("shell %q: choose one from the list or give the full path to the program", o.Shell)
		}
	case KindDocker, KindKubectl:
		if !safeArg(o.Target) {
			if o.Kind == KindDocker {
				return errors.New("enter the container's name or ID")
			}
			return errors.New("enter the pod's name")
		}
		for _, v := range []string{o.Namespace, o.Container, o.Context} {
			if v != "" && !safeArg(v) {
				return fmt.Errorf("%q is not a valid name", v)
			}
		}
		if strings.ContainsAny(o.Command, "\r\n") {
			return errors.New("the command in the container is one line")
		}
	default:
		return fmt.Errorf("unknown kind of local session %q", o.Kind)
	}
	return nil
}

// Argv returns the program and arguments that run o.
func Argv(o Options) ([]string, error) {
	if err := Validate(o); err != nil {
		return nil, err
	}
	inner := strings.Fields(o.Command)
	if len(inner) == 0 {
		inner = []string{"sh"}
	}
	switch o.Kind {
	case KindDocker:
		argv := []string{"docker"}
		if o.Context != "" {
			argv = append(argv, "--context", o.Context)
		}
		return append(append(argv, "exec", "-it", o.Target), inner...), nil
	case KindKubectl:
		argv := []string{"kubectl"}
		if o.Context != "" {
			argv = append(argv, "--context", o.Context)
		}
		argv = append(argv, "exec", "-it")
		if o.Namespace != "" {
			argv = append(argv, "-n", o.Namespace)
		}
		argv = append(argv, o.Target)
		if o.Container != "" {
			argv = append(argv, "-c", o.Container)
		}
		return append(append(argv, "--"), inner...), nil
	}
	return shellArgv(o.Shell), nil
}

// shellArgv is the command line of a local shell.
func shellArgv(shell string) []string {
	if runtime.GOOS == "windows" {
		switch shell {
		case "", "powershell":
			return []string{"powershell.exe", "-NoLogo"}
		case "pwsh":
			return []string{"pwsh.exe", "-NoLogo"}
		case "cmd":
			return []string{"cmd.exe"}
		case "wsl":
			return []string{"wsl.exe"}
		case "bash", "zsh", "fish", "sh":
			return []string{shell + ".exe"}
		}
		return []string{shell}
	}
	switch shell {
	case "":
		if sh := os.Getenv("SHELL"); sh != "" {
			return []string{sh, "-l"}
		}
		return []string{"/bin/sh", "-l"}
	case "bash", "zsh", "fish", "sh":
		return []string{shell, "-l"}
	case "powershell", "pwsh":
		return []string{"pwsh", "-NoLogo"}
	case "cmd", "wsl":
		return []string{shell} // not on this system; the error says so
	}
	return []string{shell}
}

// Label describes o for the tab's status line.
func Label(o Options) string {
	switch o.Kind {
	case KindDocker:
		return "docker exec " + o.Target
	case KindKubectl:
		if o.Namespace != "" {
			return "kubectl exec " + o.Namespace + "/" + o.Target
		}
		return "kubectl exec " + o.Target
	}
	if name := Shells[o.Shell]; name != "" {
		return "local " + name
	}
	if o.Shell == "" {
		return "local shell"
	}
	return "local " + filepath.Base(o.Shell)
}

// Conn is a running program in a pseudo terminal.
type Conn struct {
	p      pty
	mu     sync.Mutex
	closed bool
}

// pty is the platform part.
type pty interface {
	Read([]byte) (int, error)
	Write([]byte) (int, error)
	Resize(cols, rows int) error
	Close() error // ends the program too
}

// Start runs o in a pseudo terminal of cols × rows.
func Start(o Options, cols, rows int) (*Conn, error) {
	argv, err := Argv(o)
	if err != nil {
		return nil, err
	}
	path, err := exec.LookPath(argv[0])
	if err != nil {
		return nil, fmt.Errorf("%s was not found on this computer (%w)", argv[0], err)
	}
	argv[0] = path
	dir := o.Dir
	if o.Kind != KindShell || dir == "" {
		dir, _ = os.UserHomeDir()
	} else if strings.HasPrefix(dir, "~") {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, strings.TrimPrefix(strings.TrimPrefix(dir, "~"), string(filepath.Separator)))
	}
	if cols <= 0 || rows <= 0 {
		cols, rows = 80, 24
	}
	env := append(os.Environ(), "TERM=xterm-256color", "COLORTERM=truecolor")
	p, err := startPTY(argv, dir, env, cols, rows)
	if err != nil {
		return nil, err
	}
	return &Conn{p: p}, nil
}

func (c *Conn) Read(b []byte) (int, error)  { return c.p.Read(b) }
func (c *Conn) Write(b []byte) (int, error) { return c.p.Write(b) }

// Resize changes the terminal size.
func (c *Conn) Resize(cols, rows int) error {
	if cols <= 0 || rows <= 0 {
		return nil
	}
	return c.p.Resize(cols, rows)
}

// SendBreak is Ctrl+C for a local program.
func (c *Conn) SendBreak() error {
	_, err := c.p.Write([]byte{3})
	return err
}

// Close ends the program.
func (c *Conn) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil
	}
	c.closed = true
	return c.p.Close()
}

// errEOF ends a tab's read loop when the program has ended.
var errEOF = io.EOF
