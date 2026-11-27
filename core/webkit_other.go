// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (C) 2026 The YACM Authors. Additional terms under GPLv3 section 7: see NOTICE.md.

//go:build !linux

package core

// webkitWorkarounds is only needed on Linux (WebKitGTK).
func webkitWorkarounds() {}
