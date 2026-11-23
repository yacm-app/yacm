// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (C) 2026 The YACM Authors. Additional terms under GPLv3 section 7: see NOTICE.md.

package core

import (
	"github.com/yacm-app/yacm/internal/localterm"
	"github.com/yacm-app/yacm/internal/store"
)

// localOptions are the settings of a local session for localterm.
func localOptions(ss *store.Session) localterm.Options {
	lo := ss.Local
	if lo == nil {
		return localterm.Options{Kind: localterm.KindShell}
	}
	return localterm.Options{Kind: lo.Kind, Shell: lo.Shell, Dir: lo.Dir, Target: lo.Target, Namespace: lo.Namespace, Container: lo.Container, Context: lo.Context, Command: lo.Command}
}

// localTarget names what a local session runs, for log file names, the
// address of a session and the team library target check: the container
// or pod, or the shell.
func localTarget(lo *store.LocalOptions) string {
	switch lo.Kind {
	case localterm.KindDocker, localterm.KindKubectl:
		t := lo.Target
		if lo.Namespace != "" {
			t = lo.Namespace + "/" + t
		}
		return t
	}
	if lo.Shell == "" {
		return "shell"
	}
	return lo.Shell
}
