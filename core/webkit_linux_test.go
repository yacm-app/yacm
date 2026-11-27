// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (C) 2026 The YACM Authors. Additional terms under GPLv3 section 7: see NOTICE.md.

//go:build linux

package core

import "testing"

func TestNeedDMABUFOff(t *testing.T) {
	env := func(vars map[string]string) func(string) (string, bool) {
		return func(k string) (string, bool) { v, ok := vars[k]; return v, ok }
	}
	nvidia := func(p string) bool { return p == nvidiaDriver }
	none := func(string) bool { return false }
	for _, c := range []struct {
		name   string
		vars   map[string]string
		exists func(string) bool
		want   bool
	}{
		{"NVIDIA driver", nil, nvidia, true},
		{"other driver", nil, none, false},
		{"user keeps it", map[string]string{"WEBKIT_DISABLE_DMABUF_RENDERER": "0"}, nvidia, false},
		{"user set it", map[string]string{"WEBKIT_DISABLE_DMABUF_RENDERER": "1"}, none, false},
	} {
		if got := needDMABUFOff(env(c.vars), c.exists); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}
