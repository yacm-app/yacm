// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (C) 2026 The YACM Authors. Additional terms under GPLv3 section 7: see NOTICE.md.

package store

import (
	"errors"
	"fmt"
	"strings"
)

// ValidOnwardCommand checks a command typed on a jump host to go on to a
// device ("" = the default). It is one ssh, telnet or connect command with
// the tokens <USER>, <HOST> and <PORT>, and nothing a shell would run as a
// second command: it may come from a team library.
func ValidOnwardCommand(c string) error {
	if c == "" {
		return nil
	}
	if strings.ContainsAny(c, "\r\n\t") {
		return errors.New("the command on the jump host is one line")
	}
	rest := c
	for _, tok := range []string{"<USER>", "<HOST>", "<PORT>"} {
		rest = strings.ReplaceAll(rest, tok, "x")
	}
	if !strings.Contains(c, "<HOST>") {
		return errors.New("the command on the jump host needs <HOST> where the device's address goes")
	}
	if strings.ContainsAny(rest, ";|&`$()<>{}\\\"'*?!") {
		return fmt.Errorf("the command on the jump host %q may only hold words, options and <USER>, <HOST>, <PORT>", c)
	}
	switch strings.Fields(rest)[0] {
	case "ssh", "telnet", "connect":
	default:
		return fmt.Errorf("the command on the jump host starts with ssh, telnet or connect, not %q", strings.Fields(rest)[0])
	}
	return nil
}

// OnwardCommand is the command typed on a jump host for a session of
// protocol p.
func (o *OnwardOptions) OnwardCommand(p Protocol) string {
	if o == nil || o.Mode != "command" {
		return ""
	}
	if p == ProtoTelnet {
		if o.Telnet != "" {
			return o.Telnet
		}
		return DefaultOnwardTelnet
	}
	if o.SSH != "" {
		return o.SSH
	}
	return DefaultOnwardSSH
}
