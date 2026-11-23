// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (C) 2026 The YACM Authors. Additional terms under GPLv3 section 7: see NOTICE.md.

//go:build windows

package localterm

import (
	"fmt"
	"os"
	"sync"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

// winPTY runs a program in a Windows pseudo console (ConPTY, Windows 10
// 1809 and newer).
type winPTY struct {
	hpc     windows.Handle
	in      *os.File // we write keys here
	out     *os.File // the console's output
	process windows.Handle
	once    sync.Once
}

func startPTY(argv []string, dir string, env []string, cols, rows int) (pty, error) {
	var inR, inW, outR, outW windows.Handle
	if err := windows.CreatePipe(&inR, &inW, nil, 0); err != nil {
		return nil, err
	}
	if err := windows.CreatePipe(&outR, &outW, nil, 0); err != nil {
		windows.CloseHandle(inR)
		windows.CloseHandle(inW)
		return nil, err
	}
	var hpc windows.Handle
	if err := windows.CreatePseudoConsole(windows.Coord{X: int16(cols), Y: int16(rows)}, inR, outW, 0, &hpc); err != nil {
		for _, h := range []windows.Handle{inR, inW, outR, outW} {
			windows.CloseHandle(h)
		}
		return nil, fmt.Errorf("this Windows has no pseudo console (Windows 10 1809 or newer is needed): %w", err)
	}
	// The pseudo console has its own copies now.
	windows.CloseHandle(inR)
	windows.CloseHandle(outW)
	fail := func(err error) (pty, error) {
		windows.ClosePseudoConsole(hpc)
		windows.CloseHandle(inW)
		windows.CloseHandle(outR)
		return nil, err
	}

	attrs, err := windows.NewProcThreadAttributeList(1)
	if err != nil {
		return fail(err)
	}
	defer attrs.Delete()
	// The attribute's value is the console handle itself, not a pointer to it.
	hpcValue := *(*unsafe.Pointer)(unsafe.Pointer(&hpc))
	if err := attrs.Update(windows.PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE, hpcValue, unsafe.Sizeof(hpc)); err != nil {
		return fail(err)
	}
	si := windows.StartupInfoEx{ProcThreadAttributeList: attrs.List()}
	si.Cb = uint32(unsafe.Sizeof(si))
	cmdline, err := windows.UTF16PtrFromString(windows.ComposeCommandLine(argv))
	if err != nil {
		return fail(err)
	}
	var dirp *uint16
	if dir != "" {
		if dirp, err = windows.UTF16PtrFromString(dir); err != nil {
			return fail(err)
		}
	}
	block := envBlock(env)
	var pi windows.ProcessInformation
	err = windows.CreateProcess(nil, cmdline, nil, nil, false,
		windows.EXTENDED_STARTUPINFO_PRESENT|windows.CREATE_UNICODE_ENVIRONMENT, &block[0], dirp, &si.StartupInfo, &pi)
	if err != nil {
		return fail(fmt.Errorf("starting %s: %w", argv[0], err))
	}
	windows.CloseHandle(pi.Thread)
	p := &winPTY{hpc: hpc, in: os.NewFile(uintptr(inW), "conpty-in"), out: os.NewFile(uintptr(outR), "conpty-out"), process: pi.Process}
	// When the program ends, closing the console ends the output with EOF.
	go func() {
		_, _ = windows.WaitForSingleObject(pi.Process, windows.INFINITE)
		p.closeConsole()
	}()
	return p, nil
}

// envBlock is a Windows environment block: NAME=value\0…\0\0.
func envBlock(env []string) []uint16 {
	var b []uint16
	for _, kv := range env {
		b = append(b, utf16.Encode([]rune(kv))...)
		b = append(b, 0)
	}
	return append(b, 0)
}

func (p *winPTY) closeConsole() { p.once.Do(func() { windows.ClosePseudoConsole(p.hpc) }) }

func (p *winPTY) Read(b []byte) (int, error) {
	n, err := p.out.Read(b)
	if err != nil {
		err = errEOF
	}
	return n, err
}

func (p *winPTY) Write(b []byte) (int, error) { return p.in.Write(b) }

func (p *winPTY) Resize(cols, rows int) error {
	return windows.ResizePseudoConsole(p.hpc, windows.Coord{X: int16(cols), Y: int16(rows)})
}

func (p *winPTY) Close() error {
	var code uint32
	if windows.GetExitCodeProcess(p.process, &code) == nil && code == 259 { // STILL_ACTIVE
		_ = windows.TerminateProcess(p.process, 1)
	}
	p.closeConsole()
	_ = p.in.Close()
	err := p.out.Close()
	windows.CloseHandle(p.process)
	return err
}
