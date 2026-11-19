// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (C) 2026 The YACM Authors. Additional terms under GPLv3 section 7: see NOTICE.md.

//go:build linux

package fileserver

import (
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Ports below 1024 on Linux: YACM's own program gets the capability
// cap_net_bind_service (setcap, the system asks for the password), so YACM
// and no other program may listen on them. A new YACM version is a new
// file without it, so the question comes again after an update.
//
// The YACM that is running when the capability is set does not have it
// yet: it starts its own program once more, as a helper that opens the
// port and hands it over (BindHelper).

// BindHelperArg starts YACM as the helper that opens a low port.
const BindHelperArg = "--yacm-bind-port"

const unprivPortStart = "/proc/sys/net/ipv4/ip_unprivileged_port_start"

// oldLowPortsConf is the system setting earlier versions wrote.
const oldLowPortsConf = "/etc/sysctl.d/60-yacm-file-servers.conf"

// AllowPort lets YACM listen on port without running as administrator.
func AllowPort(port int) error {
	if port < 1 || port > 1023 {
		return fmt.Errorf("port %d needs no administrator rights", port)
	}
	if lowPortsOpen(port) {
		return nil
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if e, err := filepath.EvalSymlinks(exe); err == nil {
		exe = e
	}
	setcap := findSetcap()
	if setcap == "" {
		return errors.New("setcap is not installed (package libcap2-bin or libcap): install it, or as administrator run setcap cap_net_bind_service=+ep " + exe)
	}
	pk, err := exec.LookPath("pkexec")
	if err != nil {
		return fmt.Errorf("pkexec is not installed: as administrator, run %s cap_net_bind_service=+ep %s", setcap, exe)
	}
	out, err := exec.Command(pk, setcap, "cap_net_bind_service=+ep", exe).CombinedOutput()
	var ee *exec.ExitError
	switch {
	case errors.As(err, &ee) && (ee.ExitCode() == 126 || ee.ExitCode() == 127):
		return errors.New("not allowed: the password dialog was cancelled or refused")
	case err != nil:
		return fmt.Errorf("%s could not give YACM the right (%v): %s", setcap, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// lowPortsOpen: the system lets every program use port (the setting of
// earlier YACM versions, or the system's own).
func lowPortsOpen(port int) bool {
	cur, err := os.ReadFile(unprivPortStart)
	if err != nil {
		return false
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(cur)))
	return err == nil && n <= port
}

func findSetcap() string {
	if p, err := exec.LookPath("setcap"); err == nil {
		return p
	}
	for _, p := range []string{"/usr/sbin/setcap", "/sbin/setcap"} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

// LowPortsHow says how ports below 1024 are allowed.
func LowPortsHow() string {
	if _, err := os.Stat(oldLowPortsConf); err == nil {
		return "cap_net_bind_service on YACM's program; an earlier version also wrote " + oldLowPortsConf
	}
	return "cap_net_bind_service on YACM's program"
}

func lowPortDenied(err error, port int) bool {
	return port > 0 && port < 1024 && errors.Is(err, syscall.EACCES)
}

func listenTCP(addr string, port int) (net.Listener, error) {
	ln, err := net.Listen("tcp", addr)
	if err == nil || !lowPortDenied(err, port) {
		return ln, err
	}
	f, herr := helperBind("tcp", addr)
	if herr != nil {
		return nil, err
	}
	defer f.Close()
	return net.FileListener(f)
}

func listenUDP(ua *net.UDPAddr) (*net.UDPConn, error) {
	c, err := net.ListenUDP("udp", ua)
	if err == nil || !lowPortDenied(err, ua.Port) {
		return c, err
	}
	f, herr := helperBind("udp", ua.String())
	if herr != nil {
		return nil, err
	}
	defer f.Close()
	pc, err2 := net.FilePacketConn(f)
	if err2 != nil {
		return nil, err
	}
	uc, ok := pc.(*net.UDPConn)
	if !ok {
		pc.Close()
		return nil, err
	}
	return uc, nil
}

// helperBind starts YACM's program as the helper and receives the socket
// it opened.
func helperBind(network, addr string) (*os.File, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM|syscall.SOCK_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	mine, theirs := os.NewFile(uintptr(fds[0]), "bind-helper"), os.NewFile(uintptr(fds[1]), "bind-helper")
	defer mine.Close()
	cmd := exec.Command(exe, BindHelperArg, network, addr)
	cmd.ExtraFiles = []*os.File{theirs} // fd 3 in the helper
	err = cmd.Start()
	theirs.Close()
	if err != nil {
		return nil, err
	}
	defer cmd.Wait()
	c, err := net.FileConn(mine)
	if err != nil {
		_ = cmd.Process.Kill()
		return nil, err
	}
	defer c.Close()
	uc, ok := c.(*net.UnixConn)
	if !ok {
		_ = cmd.Process.Kill()
		return nil, errors.New("bind helper: not a unix socket")
	}
	_ = uc.SetReadDeadline(time.Now().Add(10 * time.Second))
	buf, oob := make([]byte, 512), make([]byte, syscall.CmsgSpace(4))
	n, oobn, _, _, err := uc.ReadMsgUnix(buf, oob)
	if err != nil {
		_ = cmd.Process.Kill()
		return nil, err
	}
	if msg := string(buf[:n]); msg != "ok" {
		return nil, errors.New(msg)
	}
	msgs, err := syscall.ParseSocketControlMessage(oob[:oobn])
	if err != nil || len(msgs) != 1 {
		return nil, errors.New("bind helper: no socket")
	}
	got, err := syscall.ParseUnixRights(&msgs[0])
	if err != nil || len(got) != 1 {
		return nil, errors.New("bind helper: no socket")
	}
	syscall.CloseOnExec(got[0])
	return os.NewFile(uintptr(got[0]), network+" "+addr), nil
}

// BindHelper is YACM started with BindHelperArg: it opens a port below
// 1024 (nothing else) and hands it to the YACM that started it on fd 3.
// It returns the exit code.
func BindHelper(args []string) int {
	out := os.NewFile(3, "bind-helper")
	if out == nil {
		return 2
	}
	c, err := net.FileConn(out)
	out.Close()
	if err != nil {
		return 2
	}
	uc, ok := c.(*net.UnixConn)
	if !ok {
		return 2
	}
	defer uc.Close()
	reply := func(f *os.File, err error) int {
		if err != nil {
			_, _, _ = uc.WriteMsgUnix([]byte(err.Error()), nil, nil)
			return 1
		}
		defer f.Close()
		if _, _, err := uc.WriteMsgUnix([]byte("ok"), syscall.UnixRights(int(f.Fd())), nil); err != nil {
			return 1
		}
		return 0
	}
	if len(args) != 2 {
		return reply(nil, errors.New("bind helper: want a network and an address"))
	}
	network, addr := args[0], args[1]
	_, ps, err := net.SplitHostPort(addr)
	if err != nil {
		return reply(nil, err)
	}
	if p, err := strconv.Atoi(ps); err != nil || p < 1 || p > 1023 {
		return reply(nil, errors.New("bind helper: only ports below 1024"))
	}
	switch network {
	case "tcp":
		ln, err := net.Listen("tcp", addr)
		if err != nil {
			return reply(nil, err)
		}
		defer ln.Close()
		return reply(ln.(*net.TCPListener).File())
	case "udp":
		ua, err := net.ResolveUDPAddr("udp", addr)
		if err != nil {
			return reply(nil, err)
		}
		pc, err := net.ListenUDP("udp", ua)
		if err != nil {
			return reply(nil, err)
		}
		defer pc.Close()
		return reply(pc.File())
	}
	return reply(nil, errors.New("bind helper: tcp or udp"))
}
