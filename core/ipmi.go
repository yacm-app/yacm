// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (C) 2026 The YACM Authors. Additional terms under GPLv3 section 7: see NOTICE.md.

package core

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	ipmi "github.com/bougou/go-ipmi/pkg/client"
	"github.com/bougou/go-ipmi/pkg/command/transport"
	"github.com/bougou/go-ipmi/pkg/types"
)

// solConn is a server console through IPMI Serial-over-LAN (RMCP+ over
// UDP 623): what the tab types goes to the server's serial port, what it
// prints comes back.
type solConn struct {
	c      *ipmi.Client
	inR    *io.PipeReader
	inW    *io.PipeWriter
	outR   *io.PipeReader
	outW   *io.PipeWriter
	cancel context.CancelFunc
	done   chan struct{}
	once   sync.Once
}

// dialSOL logs in to the BMC and activates its SOL payload.
func dialSOL(ctx context.Context, host string, port int, user, pass string, cipher int, timeout time.Duration) (*solConn, error) {
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	c, err := ipmi.NewClient(host, port, user, pass)
	if err != nil {
		return nil, err
	}
	c.WithTimeout(timeout / 3)
	if cipher > 0 {
		c.WithCipherSuiteID(types.CipherSuiteID(cipher))
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	err = c.Connect(cctx)
	cancel()
	if err != nil {
		return nil, explainIPMI(err)
	}
	s := &solConn{c: c, done: make(chan struct{})}
	s.inR, s.inW = io.Pipe()
	s.outR, s.outW = io.Pipe()
	runCtx, stop := context.WithCancel(context.Background())
	s.cancel = stop
	activated := make(chan error, 1)
	opts := &ipmi.SOLActivateOptions{
		OnActivated:   func(uint8, io.Reader, io.Writer, *transport.ActivatePayloadResponse) { activated <- nil },
		OnDeactivated: func(uint8, io.Reader, io.Writer, *transport.ActivatePayloadResponse) {},
	}
	go func() {
		defer close(s.done)
		err := c.SOLActivate(runCtx, s.inR, s.outW, opts)
		select {
		case activated <- explainIPMI(err):
		default:
		}
		if err == nil || errors.Is(err, context.Canceled) {
			err = io.EOF
		}
		s.outW.CloseWithError(err)
	}()
	select {
	case err := <-activated:
		if err != nil {
			s.Close()
			return nil, err
		}
	case <-time.After(timeout):
		s.Close()
		return nil, errors.New("the BMC did not start Serial-over-LAN")
	case <-ctx.Done():
		s.Close()
		return nil, ctx.Err()
	}
	return s, nil
}

// explainIPMI adds what to check to the usual BMC errors.
func explainIPMI(err error) error {
	if err == nil {
		return nil
	}
	m := strings.ToLower(err.Error())
	switch {
	case strings.Contains(m, "payload disabled") || strings.Contains(m, "payload type") && strings.Contains(m, "disabled"):
		return fmt.Errorf("%w (SOL is turned off for this user or the BMC: enable it, for example ipmitool sol payload enable)", err)
	case strings.Contains(m, "already active") || strings.Contains(m, "payload already"):
		return fmt.Errorf("%w (another SOL session is open on this server; close it or deactivate it on the BMC)", err)
	case strings.Contains(m, "rakp") || strings.Contains(m, "unauthorized") || strings.Contains(m, "invalid user") || strings.Contains(m, "authentication"):
		return fmt.Errorf("%w (check the BMC user name and password; the user needs IPMI over LAN and SOL rights)", err)
	case strings.Contains(m, "timeout") || strings.Contains(m, "i/o timeout") || strings.Contains(m, "deadline"):
		return fmt.Errorf("%w (no answer on UDP 623: is IPMI over LAN enabled on the BMC, and does a firewall let UDP 623 through?)", err)
	}
	return err
}

func (s *solConn) Read(b []byte) (int, error)  { return s.outR.Read(b) }
func (s *solConn) Write(b []byte) (int, error) { return s.inW.Write(b) }

// Resize: a serial console has no size.
func (s *solConn) Resize(cols, rows int) error { return nil }

// SendBreak is not offered for SOL here.
func (s *solConn) SendBreak() error {
	return errors.New("break is not available on an IPMI Serial-over-LAN console")
}

// Close deactivates SOL and ends the BMC session. The end of input lets
// the stream deactivate the payload itself (so the BMC does not keep it
// busy for the next one); the context stops it if the BMC does not answer.
func (s *solConn) Close() error {
	s.once.Do(func() {
		_ = s.inW.Close()
		select {
		case <-s.done:
		case <-time.After(3 * time.Second):
		}
		s.cancel()
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		_ = s.c.Close(ctx)
		cancel()
		_ = s.outR.Close()
	})
	return nil
}
