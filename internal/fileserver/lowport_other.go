// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (C) 2026 The YACM Authors. Additional terms under GPLv3 section 7: see NOTICE.md.

//go:build !linux

package fileserver

import (
	"errors"
	"net"
)

// BindHelperArg is used on Linux only.
const BindHelperArg = "--yacm-bind-port"

// AllowPort is needed on Linux only; elsewhere low ports either work or need
// YACM to run as administrator.
func AllowPort(int) error {
	return errors.New("only needed on Linux")
}

// LowPortsHow says how ports below 1024 are allowed.
func LowPortsHow() string { return "" }

// BindHelper is used on Linux only.
func BindHelper([]string) int { return 2 }

func listenTCP(addr string, _ int) (net.Listener, error) { return net.Listen("tcp", addr) }

func listenUDP(ua *net.UDPAddr) (*net.UDPConn, error) { return net.ListenUDP("udp", ua) }
