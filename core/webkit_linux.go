// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (C) 2026 The YACM Authors. Additional terms under GPLv3 section 7: see NOTICE.md.

//go:build linux

package core

import (
	"os"

	"github.com/yacm-app/yacm/internal/applog"
)

// With the NVIDIA driver, WebKitGTK's DMA-BUF renderer often fails and the
// window stays blank or black. YACM then turns that renderer off, unless the
// user set WEBKIT_DISABLE_DMABUF_RENDERER themselves (to 0 to keep it).
// The web process WebKit starts inherits the variable.
const nvidiaDriver = "/proc/driver/nvidia/version"

func webkitWorkarounds() {
	if needDMABUFOff(os.LookupEnv, fileExists) {
		_ = os.Setenv("WEBKIT_DISABLE_DMABUF_RENDERER", "1")
		applog.Infof("NVIDIA driver: WebKit DMA-BUF renderer turned off (WEBKIT_DISABLE_DMABUF_RENDERER=0 keeps it)")
	}
}

func needDMABUFOff(lookup func(string) (string, bool), exists func(string) bool) bool {
	if _, set := lookup("WEBKIT_DISABLE_DMABUF_RENDERER"); set {
		return false
	}
	return exists(nvidiaDriver)
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
