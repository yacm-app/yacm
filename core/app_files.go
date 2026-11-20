// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (C) 2026 The YACM Authors. Additional terms under GPLv3 section 7: see NOTICE.md.

package core

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"

	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"
	"golang.org/x/crypto/ssh"

	"github.com/yacm-app/yacm/internal/applog"
	"github.com/yacm-app/yacm/internal/sshconn"
	"github.com/yacm-app/yacm/internal/store"
	"github.com/yacm-app/yacm/internal/xfer"
)

// ---- file transfer (SFTP / SCP) ------------------------------------------------

// filesConn is an open file system of a device: a Files tab, or a Send /
// Get file started from a terminal tab.
type filesConn struct {
	id     string
	name   string
	nodeID string
	remote xfer.Remote
	link   *xfer.Link // reconnects when the device closed the connection
	mu     sync.Mutex
	own    *sshconn.Client // nil while the connection belongs to a terminal tab
}

// redialFiles gives the file commands a new connection to the device: over
// the same jump hosts when there is one of our own, else a new one (Cisco
// IOS closes the connection when its one channel closes).
func (a *App) redialFiles(f *filesConn, node *store.Node, ownerID string) func(ctx context.Context) (*ssh.Client, error) {
	return func(ctx context.Context) (*ssh.Client, error) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.own != nil {
			// Over the same jump host login first, at most 10 s; then a
			// whole new connection (some jump hosts hold the second one).
			rctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			err := f.own.Reconnect(rctx)
			cancel()
			if err == nil {
				return f.own.SSH(), nil
			}
			applog.Infof("files %s: reconnecting over the same jump host login failed (%v): new login", node.Name, err)
			f.own.Close()
			f.own = nil
		}
		cl, err := a.dialSession(ctx, node, ownerID)
		if err != nil {
			return nil, err
		}
		f.own = cl
		return cl.SSH(), nil
	}
}

// FilesOpenRequest opens the file system of a session. TabID borrows the
// connection of a connected SSH terminal tab (no second login); OwnerID is
// the tab that shows prompts (host key, password).
type FilesOpenRequest struct {
	NodeID  string `json:"nodeId"`
	TabID   string `json:"tabId"`
	OwnerID string `json:"ownerId"`
	// SCP uses SCP and exec commands even when the device offers SFTP.
	SCP bool `json:"scp"`
}

// FilesInfo describes an opened file system.
type FilesInfo struct {
	ID      string      `json:"id"`
	Name    string      `json:"name"`
	Kind    string      `json:"kind"` // sftp, scp
	Home    string      `json:"home"`
	Roots   []xfer.Root `json:"roots"`
	Summary string      `json:"summary"`
	// Shared is true when the connection of a terminal tab is used.
	Shared bool `json:"shared"`
}

// TransferEvent is emitted as "xfer:progress".
type TransferEvent struct {
	ID      string `json:"id"`
	FilesID string `json:"filesId"`
	Name    string `json:"name"`
	Dir     string `json:"dir"` // up, down, cmd
	Local   string `json:"local"`
	Remote  string `json:"remote"`
	Done    int64  `json:"done"`
	Total   int64  `json:"total"`
	State   string `json:"state"` // running, verifying, done, error, cancelled
	Error   string `json:"error,omitempty"`
	MD5     string `json:"md5,omitempty"`
	// Verified: "" not checked, "ok", "mismatch", "unavailable".
	Verified string `json:"verified,omitempty"`
	// Hint: advice shown while the transfer runs (how to make it faster).
	Hint string `json:"hint,omitempty"`
	// Resume: it stopped part-way over SFTP and FilesResume can go on.
	Resume bool `json:"resume,omitempty"`
	// Denied: the device refused to write there (no permission); the
	// frontend offers to send it to /tmp and move it with sudo.
	Denied bool `json:"denied,omitempty"`
	// Files counts the files of a folder copy (0: a single file).
	Files int `json:"files,omitempty"`
}

// scpWindowHint is shown with large SCP uploads: on network devices their
// speed is set by the device's SSH window and the round trip time.
const scpWindowHint = "Slow? SCP to a network device is as fast as its SSH window allows: on Cisco IOS-XE, a larger \"ip ssh window-size\" (or \"ip ssh bulk-mode\") speeds it up."

var errNoFiles = errors.New("the file connection is closed; open it again")

func (a *App) filesGet(id string) (*filesConn, error) {
	a.filesMu.Lock()
	defer a.filesMu.Unlock()
	f := a.files[id]
	if f == nil {
		return nil, errNoFiles
	}
	return f, nil
}

// borrowSSH returns the device connection of a connected SSH tab.
// spareChannel checks that the connection takes one more session channel
// (Cisco IOS takes one per connection, and a terminal tab holds it).
func spareChannel(c *ssh.Client) error {
	s, err := c.NewSession()
	if err != nil {
		return err
	}
	_ = s.Close()
	return nil
}

// isChannelRefused reports a device that refused to open a channel.
func isChannelRefused(err error) bool {
	var oce *ssh.OpenChannelError
	return errors.As(err, &oce)
}

func (a *App) borrowSSH(tabID string) (*ssh.Client, *tab, error) {
	t, err := a.getTab(tabID)
	if err != nil {
		return nil, nil, err
	}
	t.mu.Lock()
	c, state := t.conn, t.state
	t.mu.Unlock()
	if sc, ok := c.(*sshconn.Conn); ok && state == "connected" {
		if cl := sc.SSHClient(); cl != nil {
			return cl, t, nil
		}
	}
	return nil, t, nil
}

// sshHops builds the SSH hops of a session: jump hosts, then the device.
func (a *App) sshHops(node *store.Node) ([]sshconn.Hop, *store.Effective, error) {
	ss := node.Session
	if ss == nil {
		return nil, nil, errors.New("not a session")
	}
	if !store.KnownProtocol(ss.Protocol) {
		return nil, nil, newerTypeError(node)
	}
	if ss.Protocol != store.ProtoSSH {
		return nil, nil, fmt.Errorf("%s is a %s session: file transfer and tunnels need SSH. For telnet and serial, let the device copy from the file servers (Servers, bottom left)", node.Name, ss.Protocol)
	}
	eff, err := a.store.Effective(node)
	if err != nil {
		return nil, nil, err
	}
	hops := a.jumpHopsQuiet(node, eff)
	hops = append(hops, sshconn.Hop{
		Host: ss.Host, Port: ss.Port, User: eff.User, Auth: eff.Auth, KeyPath: eff.KeyPath, CertPath: eff.CertPath,
		Password:      a.secret(eff.PasswordKey),
		Passphrase:    a.secret(eff.PassphraseKey),
		PasswordKey:   a.savableKey(node, eff.PasswordKey),
		PassphraseKey: a.savableKey(node, eff.PassphraseKey),
		UserKey:       userKey(node, -1),
	})
	return hops, eff, nil
}

// dialSession opens a new SSH connection (no shell) to a session.
func (a *App) dialSession(ctx context.Context, node *store.Node, ownerID string) (*sshconn.Client, error) {
	hops, eff, err := a.sshHops(node)
	if err != nil {
		return nil, err
	}
	ss := node.Session
	return sshconn.DialClient(ctx, sshconn.Config{
		Hops: hops, Profile: eff.Algorithms, ConnectTimeout: time.Duration(ss.ConnectTimeout) * time.Second,
		KeepAlive:  30 * time.Second,
		KnownHosts: a.kh, Prompter: tabPrompter{a, ownerID, node.Name},
		SaveSecret: func(name, v string) error { return a.vault.Set(name, v) },
		SaveUser:   a.saveUser,
		Status:     func(m string) { applog.Infof("files %s: %s", node.Name, m) },
		Dial:       a.proxyDial(eff),
	})
}

// FilesOpen connects to the file system of a session.
func (a *App) FilesOpen(req FilesOpenRequest) (FilesInfo, error) {
	var node *store.Node
	var client *ssh.Client
	var own *sshconn.Client
	summary, note := "", ""
	if req.TabID != "" {
		cl, t, err := a.borrowSSH(req.TabID)
		if err != nil {
			return FilesInfo{}, err
		}
		node = t.node
		if cl != nil {
			switch err := spareChannel(cl); {
			case err == nil:
				client, summary = cl, "using the connection of the open session"
			case isChannelRefused(err):
				// Cisco IOS: one session channel per connection, and the
				// terminal has it.
				note = "the device allows one channel per SSH connection"
				applog.Infof("files %s: %s, own connection", node.Name, note)
			default:
				return FilesInfo{}, err
			}
		}
	} else {
		n, err := a.store.Get(req.NodeID)
		if err != nil {
			return FilesInfo{}, err
		}
		node = n
	}
	dial := func() error {
		if node.IsGroup {
			return errors.New("choose a session, not a group")
		}
		cl, err := a.dialSession(context.Background(), node, req.OwnerID)
		if err != nil {
			return err
		}
		own, client, summary = cl, cl.SSH(), cl.Summary()
		if note != "" {
			summary = "own connection: " + note
		}
		return nil
	}
	if client == nil {
		if err := dial(); err != nil {
			return FilesInfo{}, err
		}
	}
	f := &filesConn{id: store.NewID(), name: node.Name, nodeID: node.ID, own: own}
	f.link = xfer.NewLink(client, a.redialFiles(f, node, req.OwnerID))
	f.link.Logf = func(format string, args ...any) {
		applog.Infof("files %s: "+format, append([]any{node.Name}, args...)...)
	}
	f.link.OnSerial = func() {
		a.filesMu.Lock()
		if a.serialOnly == nil {
			a.serialOnly = map[string]bool{}
		}
		a.serialOnly[node.ID] = true
		a.filesMu.Unlock()
	}
	a.filesMu.Lock()
	scp := req.SCP || a.scpOnly[node.ID]
	if a.serialOnly[node.ID] {
		f.link.SetSerial()
	}
	a.filesMu.Unlock()
	if scp {
		f.remote = xfer.OpenSCP(f.link)
	} else {
		start := time.Now()
		r, why := xfer.OpenWhy(f.link)
		f.remote = r
		if why == "" {
			applog.Infof("files %s: SFTP (started in %v)", node.Name, time.Since(start).Round(time.Millisecond))
		} else {
			applog.Infof("files %s: no SFTP (%s, after %v): SCP and device commands", node.Name, why, time.Since(start).Round(time.Millisecond))
		}
		if r.Kind() == "scp" {
			// The device refused SFTP: no second attempt for this session.
			// Cisco IOS XE also closes the connection after it; the next
			// command reconnects.
			a.filesMu.Lock()
			if a.scpOnly == nil {
				a.scpOnly = map[string]bool{}
			}
			a.scpOnly[node.ID] = true
			a.filesMu.Unlock()
		}
	}
	remote := f.remote
	a.filesMu.Lock()
	a.files[f.id] = f
	a.filesMu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	info := FilesInfo{ID: f.id, Name: node.Name, Kind: remote.Kind(), Home: remote.Home(ctx), Summary: summary, Shared: own == nil}
	info.Roots = remote.Roots(ctx)
	applog.Infof("files %s: opened (%s)", node.Name, info.Kind)
	return info, nil
}

// FilesClose closes a file system (and its connection unless it belongs
// to a terminal tab). Running transfers of it are cancelled.
func (a *App) FilesClose(id string) {
	a.filesMu.Lock()
	f := a.files[id]
	delete(a.files, id)
	var cancels []context.CancelCauseFunc
	for _, x := range a.xfers {
		if x.filesID == id {
			cancels = append(cancels, x.cancel)
		}
	}
	a.filesMu.Unlock()
	for _, c := range cancels {
		c(errFilesClosed)
	}
	if f != nil {
		_ = f.remote.Close()
		f.mu.Lock()
		own := f.own
		f.own = nil
		f.mu.Unlock()
		if own != nil {
			_ = own.Close()
		}
	}
}

func (a *App) closeAllFiles() {
	a.filesMu.Lock()
	ids := make([]string, 0, len(a.files))
	for id := range a.files {
		ids = append(ids, id)
	}
	a.filesMu.Unlock()
	for _, id := range ids {
		a.FilesClose(id)
	}
}

func opCtx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 2*time.Minute)
}

// FilesList lists a folder on the device.
func (a *App) FilesList(id, dir string) ([]xfer.Entry, error) {
	f, err := a.filesGet(id)
	if err != nil {
		return nil, err
	}
	ctx, cancel := opCtx()
	defer cancel()
	es, err := f.remote.List(ctx, dir)
	if es == nil && err == nil {
		es = []xfer.Entry{}
	}
	return es, err
}

// FilesFree reports the free space where path lives (-1: unknown).
func (a *App) FilesFree(id, path string) int64 {
	f, err := a.filesGet(id)
	if err != nil {
		return -1
	}
	ctx, cancel := opCtx()
	defer cancel()
	return f.remote.Free(ctx, path)
}

// FilesRemove deletes a file (or an empty folder) on the device.
func (a *App) FilesRemove(id, path string) error {
	f, err := a.filesGet(id)
	if err != nil {
		return err
	}
	ctx, cancel := opCtx()
	defer cancel()
	err = f.remote.Remove(ctx, path)
	if err == nil {
		applog.Infof("files %s: deleted %s", f.name, path)
	}
	return err
}

// FilesMkdir creates a folder on the device.
func (a *App) FilesMkdir(id, path string) error {
	f, err := a.filesGet(id)
	if err != nil {
		return err
	}
	ctx, cancel := opCtx()
	defer cancel()
	return f.remote.Mkdir(ctx, path)
}

// FilesRename renames or moves a file on the device.
func (a *App) FilesRename(id, from, to string) error {
	f, err := a.filesGet(id)
	if err != nil {
		return err
	}
	ctx, cancel := opCtx()
	defer cancel()
	return f.remote.Rename(ctx, from, to)
}

// FilesMD5 asks the device for the MD5 checksum of a file.
func (a *App) FilesMD5(id, path string) (string, error) {
	f, err := a.filesGet(id)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	return f.link.MD5(ctx, path)
}

// ---- transfers ------------------------------------------------------------------

type transferJob struct {
	filesID string
	cancel  context.CancelCauseFunc
}

// Why a transfer was cancelled, for the log.
var (
	errCancelledByUser = errors.New("by the user")
	errFilesClosed     = errors.New("because its file connection was closed")
)

func (a *App) startTransfer(f *filesConn, ev TransferEvent, work func(ctx context.Context, ev *TransferEvent, tick func(int64)) error) string {
	return a.startJob(f.id, f.name, ev, work)
}

// startJob runs work as a transfer the frontend lists (filesID may be empty
// for a job that uses no file connection, such as command output through a
// session); name is the device for the log.
func (a *App) startJob(filesID, name string, ev TransferEvent, work func(ctx context.Context, ev *TransferEvent, tick func(int64)) error) string {
	ev.ID, ev.FilesID, ev.State = store.NewID(), filesID, "running"
	ctx, cancel := context.WithCancelCause(context.Background())
	a.filesMu.Lock()
	a.xfers[ev.ID] = &transferJob{filesID: filesID, cancel: cancel}
	a.filesMu.Unlock()
	go func() {
		defer applog.Recover("transfer "+ev.Name, nil)
		defer cancel(nil)
		var mu sync.Mutex
		last := time.Time{}
		// A device that takes one channel at a time: the transfer waits
		// for the others ("queued").
		ctx := xfer.WithWaitNotice(ctx, func(waiting bool) {
			mu.Lock()
			defer mu.Unlock()
			switch {
			case waiting && ev.State == "running":
				ev.State = "queued"
			case !waiting && ev.State == "queued":
				ev.State = "running"
			default:
				return
			}
			a.emit("xfer:progress", ev)
		})
		tick := func(n int64) {
			mu.Lock()
			defer mu.Unlock()
			ev.Done = n
			if time.Since(last) > 200*time.Millisecond {
				last = time.Now()
				a.emit("xfer:progress", ev)
			}
		}
		a.emit("xfer:progress", ev)
		started := time.Now()
		err := work(ctx, &ev, tick)
		mu.Lock()
		switch {
		case ctx.Err() != nil:
			ev.State, ev.Error = "cancelled", ""
		case err != nil:
			ev.State, ev.Error = "error", err.Error()
		default:
			ev.State = "done"
		}
		final := ev
		mu.Unlock()
		a.filesMu.Lock()
		delete(a.xfers, ev.ID)
		a.filesMu.Unlock()
		what := map[string]string{"up": "upload", "down": "download", "cmd": "command output"}[ev.Dir]
		took := time.Since(started)
		speed := ""
		if s := took.Seconds(); s >= 0.1 && final.Done > 0 {
			speed = ", " + humanBytes(int64(float64(final.Done)/s)) + "/s"
		}
		if final.State == "done" {
			applog.Infof("files %s: %s %s ↔ %s finished (%d bytes in %v%s%s)", name, what, final.Remote, final.Local, final.Done, roundTook(took), speed, map[string]string{"ok": ", MD5 verified", "mismatch": ", MD5 MISMATCH"}[final.Verified])
		} else {
			reason := final.Error
			if final.State == "cancelled" {
				if c := context.Cause(ctx); c != nil && c != context.Canceled {
					reason = c.Error()
				}
			}
			applog.Warnf("files %s: %s %s ↔ %s %s %s (%d of %d bytes in %v%s)", name, what, final.Remote, final.Local, final.State, reason, final.Done, final.Total, roundTook(took), speed)
		}
		a.emit("xfer:progress", final)
	}()
	return ev.ID
}

// TransferCancel stops a running transfer.
func (a *App) TransferCancel(xferID string) {
	a.filesMu.Lock()
	x := a.xfers[xferID]
	a.filesMu.Unlock()
	if x != nil {
		x.cancel(errCancelledByUser)
	}
}

func localMD5(p string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := md5.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// verify compares the MD5 of a local and a device file.
func verify(ctx context.Context, f *filesConn, ev *TransferEvent) {
	ev.State = "verifying"
	local, err := localMD5(ev.Local)
	if err != nil {
		ev.Verified = "unavailable"
		return
	}
	ev.MD5 = local
	remote, err := f.link.MD5(ctx, ev.Remote)
	switch {
	case err != nil:
		ev.Verified = "unavailable"
	case remote == local:
		ev.Verified = "ok"
	default:
		ev.Verified = "mismatch"
	}
}

// FilesDownload copies a device file to this computer. size is the size
// shown in the listing (0: unknown); verify compares MD5 checksums after.
func (a *App) FilesDownload(id, remotePath, localPath string, size int64, verifyMD5 bool) (string, error) {
	f, err := a.filesGet(id)
	if err != nil {
		return "", err
	}
	if localPath == "" {
		return "", errors.New("no local file")
	}
	ev := TransferEvent{Name: baseName(remotePath), Dir: "down", Local: localPath, Remote: remotePath, Total: size}
	return a.startTransfer(f, ev, a.download(f, verifyMD5, false)), nil
}

// download is the work of a download; with resume it goes on from the
// .part file a stopped SFTP download left. Over SFTP a download that stops
// part-way keeps its .part file so that it can go on (FilesResume).
func (a *App) download(f *filesConn, verifyMD5, resume bool) func(ctx context.Context, ev *TransferEvent, tick func(int64)) error {
	return func(ctx context.Context, ev *TransferEvent, tick func(int64)) error {
		localPath, remotePath := ev.Local, ev.Remote
		tmp := localPath + ".part"
		a.forgetResumes(tmp) // a stopped download of the same file: this one takes its .part
		res, canResume := f.remote.(xfer.Resumer)
		var off int64
		err := func() error {
			if resume && canResume {
				if st, err := os.Stat(tmp); err == nil {
					off = st.Size()
				}
				e, err := f.remote.Stat(ctx, remotePath)
				if err != nil {
					return err // the .part stays for the next try
				}
				if e.Size < off {
					off = 0 // changed on the device: from the start
				}
			}
			flags := os.O_WRONLY | os.O_CREATE | os.O_TRUNC
			if off > 0 {
				flags = os.O_WRONLY | os.O_APPEND
			}
			out, err := os.OpenFile(tmp, flags, 0o644)
			if err != nil {
				return err
			}
			if off > 0 {
				tick(off)
				err = res.GetFrom(ctx, remotePath, out, off, tick)
			} else {
				err = f.remote.Get(ctx, remotePath, out, tick)
			}
			if cerr := out.Close(); err == nil {
				err = cerr
			}
			return err
		}()
		if err == nil {
			_ = os.Remove(localPath)
			err = os.Rename(tmp, localPath)
		}
		if err != nil {
			// Not when its tab closed: nothing could go on with it.
			canResume = canResume && context.Cause(ctx) != errFilesClosed
			if st, serr := os.Stat(tmp); canResume && serr == nil && st.Size() > 0 && a.keepResume(f, *ev, verifyMD5, tmp) {
				ev.Resume = true
			} else {
				_ = os.Remove(tmp)
			}
			return err
		}
		if verifyMD5 && ctx.Err() == nil {
			verify(ctx, f, ev)
			if ev.Verified == "mismatch" {
				return errors.New("the MD5 checksum of the downloaded file differs from the one on the device")
			}
		}
		return nil
	}
}

// resumeJob is a stopped SFTP transfer that FilesResume can go on with,
// over the same file connection or a new one to the same session (after
// Reconnect).
type resumeJob struct {
	nodeID string
	ev     TransferEvent
	verify bool
	part   string // a download's .part file
}

// keepResume remembers a stopped transfer.
func (a *App) keepResume(f *filesConn, ev TransferEvent, verifyMD5 bool, part string) bool {
	a.filesMu.Lock()
	defer a.filesMu.Unlock()
	a.resumes[ev.ID] = &resumeJob{nodeID: f.nodeID, ev: ev, verify: verifyMD5, part: part}
	return true
}

// forgetResumes forgets stopped downloads into part (without removing it).
func (a *App) forgetResumes(part string) {
	a.filesMu.Lock()
	defer a.filesMu.Unlock()
	for id, j := range a.resumes {
		if j.part == part {
			delete(a.resumes, id)
		}
	}
}

// denied reports a device refusing to write (no permission).
func denied(err error) bool {
	return errors.Is(err, os.ErrPermission) || strings.Contains(strings.ToLower(err.Error()), "permission denied")
}

// dropResumes forgets all stopped transfers (when YACM closes), removing
// their .part files.
func (a *App) dropResumes() {
	a.filesMu.Lock()
	defer a.filesMu.Unlock()
	for id, j := range a.resumes {
		if j.part != "" {
			_ = os.Remove(j.part)
		}
		delete(a.resumes, id)
	}
}

// FilesResume goes on with a transfer that stopped part-way over SFTP: a
// download from its .part file, an upload from what the device has. filesID
// is the file connection to use: the one it ran on, or a new one to the same
// session. It returns the new transfer's ID.
func (a *App) FilesResume(xferID, filesID string) (string, error) {
	f, err := a.filesGet(filesID)
	if err != nil {
		return "", err
	}
	a.filesMu.Lock()
	j := a.resumes[xferID]
	if j != nil && j.nodeID == f.nodeID {
		delete(a.resumes, xferID)
	}
	a.filesMu.Unlock()
	switch {
	case j == nil:
		return "", errors.New("this transfer cannot go on any more: start it again")
	case j.nodeID != f.nodeID:
		return "", errors.New("this transfer belongs to another session")
	}
	ev := j.ev
	ev.Error, ev.Resume, ev.Denied, ev.Verified, ev.MD5 = "", false, false, "", ""
	if ev.Dir == "down" {
		return a.startTransfer(f, ev, a.download(f, j.verify, true)), nil
	}
	return a.startTransfer(f, ev, a.upload(f, j.verify, true)), nil
}

// FilesDiscard forgets stopped transfers that will not go on, and removes
// the .part files of their downloads.
func (a *App) FilesDiscard(xferIDs []string) {
	a.filesMu.Lock()
	defer a.filesMu.Unlock()
	for _, id := range xferIDs {
		if j := a.resumes[id]; j != nil {
			if j.part != "" {
				_ = os.Remove(j.part)
			}
			delete(a.resumes, id)
		}
	}
}

// FilesUpload copies a file from this computer to the device and, with
// verify, compares MD5 checksums after (verify /md5 on Cisco).
func (a *App) FilesUpload(id, localPath, remotePath string, verifyMD5 bool) (string, error) {
	f, err := a.filesGet(id)
	if err != nil {
		return "", err
	}
	st, err := os.Stat(localPath)
	if err != nil {
		return "", err
	}
	if st.IsDir() {
		return "", fmt.Errorf("%s is a folder; send single files", localPath)
	}
	if xfer.IsDevicePath(remotePath) {
		if name := baseName(remotePath); name != xfer.DeviceName(name) {
			return "", fmt.Errorf("network devices cut file names at a space: send it as %s", xfer.DeviceName(name))
		}
	}
	ev := TransferEvent{Name: filepath.Base(localPath), Dir: "up", Local: localPath, Remote: remotePath, Total: st.Size()}
	if f.remote.Kind() == "scp" && st.Size() >= 64<<20 {
		ev.Hint = scpWindowHint
	}
	return a.startTransfer(f, ev, a.upload(f, verifyMD5, false)), nil
}

// upload is the work of an upload; with resume it goes on from the size the
// file already has on the device (SFTP).
func (a *App) upload(f *filesConn, verifyMD5, resume bool) func(ctx context.Context, ev *TransferEvent, tick func(int64)) error {
	return func(ctx context.Context, ev *TransferEvent, tick func(int64)) error {
		localPath, remotePath := ev.Local, ev.Remote
		st, err := os.Stat(localPath)
		if err != nil {
			return err
		}
		ev.Total = st.Size()
		res, canResume := f.remote.(xfer.Resumer)
		var off int64
		if resume && canResume {
			if e, err := f.remote.Stat(ctx, remotePath); err == nil && e.Size > 0 && e.Size <= st.Size() {
				off = e.Size
			}
		}
		if free := f.remote.Free(ctx, remotePath); free >= 0 && free < st.Size()-off {
			return fmt.Errorf("not enough space on the device: %s free, the file needs %s", humanBytes(free), humanBytes(st.Size()-off))
		}
		in, err := os.Open(localPath)
		if err != nil {
			return err
		}
		defer in.Close()
		if off > 0 {
			if _, err = in.Seek(off, io.SeekStart); err == nil {
				tick(off)
				err = res.PutFrom(ctx, remotePath, in, off, st.Size(), tick)
			}
		} else {
			err = f.remote.Put(ctx, remotePath, in, st.Size(), tick)
		}
		if err != nil {
			if canResume && !denied(err) && context.Cause(ctx) != errFilesClosed {
				if e, serr := f.remote.Stat(context.Background(), remotePath); serr == nil && e.Size > 0 && e.Size < st.Size() {
					ev.Resume = a.keepResume(f, *ev, verifyMD5, "")
				}
			}
			ev.Denied = canResume && denied(err) && strings.HasPrefix(remotePath, "/")
			return err
		}
		if verifyMD5 && ctx.Err() == nil {
			verify(ctx, f, ev)
			if ev.Verified == "mismatch" {
				return errors.New("the MD5 checksum on the device differs from the file sent: delete it and send it again")
			}
		}
		// Without a checksum, check that the device kept the file under
		// this name and size (a device can save it under another name).
		if ev.Verified != "ok" && xfer.IsDevicePath(remotePath) && ctx.Err() == nil {
			if e, err := f.remote.Stat(ctx, remotePath); errors.Is(err, xfer.ErrNoSuchFile) {
				return fmt.Errorf("the device did not save the file as %s: check its file list", baseName(remotePath))
			} else if err == nil && e.Size >= 0 && e.Size != st.Size() {
				return fmt.Errorf("the device has %s with %s instead of %s: delete it and send it again", baseName(remotePath), humanBytes(e.Size), humanBytes(st.Size()))
			}
		}
		return nil
	}
}

// FilesTempDir makes a new folder in /tmp on a server with SFTP that only
// the user can open, for Send with sudo: a file sent straight into /tmp
// could be one someone else made and can still change.
func (a *App) FilesTempDir(id string) (string, error) {
	f, err := a.filesGet(id)
	if err != nil {
		return "", err
	}
	t, ok := f.remote.(xfer.TempDirer)
	if !ok {
		return "", errors.New("this works over SFTP only")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return t.TempDir(ctx)
}

// Limits of a folder copy.
const (
	folderMaxFiles = 20000
	folderMaxDepth = 32
)

// FilesDownloadFolder copies a device folder with everything in it into
// localDir (created; files already there are replaced). Names from the
// device are checked like single downloads.
func (a *App) FilesDownloadFolder(id, remoteDir, localDir string) (string, error) {
	f, err := a.filesGet(id)
	if err != nil {
		return "", err
	}
	if localDir == "" {
		return "", errors.New("no local folder")
	}
	ev := TransferEvent{Name: baseName(remoteDir) + "/", Dir: "down", Local: localDir, Remote: remoteDir}
	return a.startTransfer(f, ev, func(ctx context.Context, ev *TransferEvent, tick func(int64)) error {
		type item struct{ remote, local string }
		var files []item
		var walk func(rdir, ldir string, depth int) error
		walk = func(rdir, ldir string, depth int) error {
			if depth > folderMaxDepth {
				return fmt.Errorf("%s is nested more than %d folders deep", rdir, folderMaxDepth)
			}
			if err := os.MkdirAll(ldir, 0o755); err != nil {
				return err
			}
			es, err := f.remote.List(ctx, rdir)
			if err != nil {
				return err
			}
			for _, e := range es {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				if err := checkFileName(e.Name); err != nil {
					return err
				}
				lp := filepath.Join(ldir, e.Name)
				if e.Dir {
					if err := walk(e.Path, lp, depth+1); err != nil {
						return err
					}
					continue
				}
				if len(files) >= folderMaxFiles {
					return fmt.Errorf("more than %d files: copy smaller folders", folderMaxFiles)
				}
				files = append(files, item{e.Path, lp})
				ev.Total += max(e.Size, 0)
			}
			return nil
		}
		if err := walk(remoteDir, localDir, 0); err != nil {
			return err
		}
		ev.Files = len(files)
		var done int64
		for i, it := range files {
			base := done
			tmp := it.local + ".part"
			out, err := os.Create(tmp)
			if err != nil {
				return err
			}
			err = f.remote.Get(ctx, it.remote, out, func(n int64) { tick(base + n); done = base + n })
			if cerr := out.Close(); err == nil {
				err = cerr
			}
			if err == nil {
				_ = os.Remove(it.local)
				err = os.Rename(tmp, it.local)
			}
			if err != nil {
				_ = os.Remove(tmp)
				return fmt.Errorf("%s (%d of %d files copied): %w", it.remote, i, len(files), err)
			}
		}
		tick(done)
		return nil
	}), nil
}

// FilesUploadFolder copies a folder from this computer with everything in
// it into remoteDir on the device (created). Over SFTP; network devices that
// copy with SCP take single files. Symbolic links are not followed.
func (a *App) FilesUploadFolder(id, localDir, remoteDir string) (string, error) {
	f, err := a.filesGet(id)
	if err != nil {
		return "", err
	}
	if f.remote.Kind() != "sftp" {
		return "", errors.New("folders are copied over SFTP; this device takes single files")
	}
	if st, err := os.Stat(localDir); err != nil || !st.IsDir() {
		return "", fmt.Errorf("%s is not a folder", localDir)
	}
	ev := TransferEvent{Name: filepath.Base(localDir) + "/", Dir: "up", Local: localDir, Remote: remoteDir}
	return a.startTransfer(f, ev, func(ctx context.Context, ev *TransferEvent, tick func(int64)) error {
		type item struct {
			local, remote string
			size          int64
		}
		var files []item
		var dirs []string
		err := filepath.WalkDir(localDir, func(p string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			rel, _ := filepath.Rel(localDir, p)
			if strings.Count(rel, string(filepath.Separator)) > folderMaxDepth {
				return fmt.Errorf("%s is nested more than %d folders deep", p, folderMaxDepth)
			}
			rp := remoteDir
			if rel != "." {
				rp = xfer.Join(remoteDir, filepath.ToSlash(rel))
			}
			switch {
			case d.IsDir():
				dirs = append(dirs, rp)
			case d.Type().IsRegular():
				if len(files) >= folderMaxFiles {
					return fmt.Errorf("more than %d files: copy smaller folders", folderMaxFiles)
				}
				info, err := d.Info()
				if err != nil {
					return err
				}
				files = append(files, item{p, rp, info.Size()})
				ev.Total += info.Size()
			}
			return nil
		})
		if err != nil {
			return err
		}
		ev.Files = len(files)
		if free := f.remote.Free(ctx, remoteDir); free >= 0 && free < ev.Total {
			return fmt.Errorf("not enough space on the device: %s free, the folder needs %s", humanBytes(free), humanBytes(ev.Total))
		}
		for _, d := range dirs {
			if err := f.remote.Mkdir(ctx, d); err != nil {
				if e, serr := f.remote.Stat(ctx, d); serr != nil || !e.Dir {
					ev.Denied = denied(err) && strings.HasPrefix(remoteDir, "/")
					return fmt.Errorf("cannot create %s: %w", d, err)
				}
			}
		}
		var done int64
		for i, it := range files {
			base := done
			in, err := os.Open(it.local)
			if err != nil {
				return err
			}
			err = f.remote.Put(ctx, it.remote, in, it.size, func(n int64) { tick(base + n) })
			in.Close()
			if err != nil {
				ev.Denied = denied(err) && strings.HasPrefix(remoteDir, "/")
				return fmt.Errorf("%s (%d of %d files copied): %w", it.remote, i, len(files), err)
			}
			done = base + it.size
			tick(done)
		}
		return nil
	}), nil
}

// FilesCommandToFile runs a command on the device (its own exec channel,
// no paging) and saves the output to a file: Get show tech, or any command.
func (a *App) FilesCommandToFile(id, command, localPath string) (string, error) {
	f, err := a.filesGet(id)
	if err != nil {
		return "", err
	}
	command = strings.TrimSpace(command)
	if command == "" || strings.ContainsAny(command, "\r\n") {
		return "", errors.New("enter one command")
	}
	ev := TransferEvent{Name: command, Dir: "cmd", Local: localPath, Remote: command}
	return a.startTransfer(f, ev, func(ctx context.Context, ev *TransferEvent, tick func(int64)) error {
		tmp := localPath + ".part"
		out, err := os.Create(tmp)
		if err != nil {
			return err
		}
		var n int64
		err = f.link.Do(ctx, func(c *ssh.Client) error {
			var err error
			n, err = xfer.ExecTo(ctx, c, command, out, tick, 10*time.Minute)
			return err
		})
		if cerr := out.Close(); err == nil {
			err = cerr
		}
		if err == nil && n == 0 {
			err = errors.New("the device returned no output (does it allow commands over an SSH exec channel?)")
		}
		if err == nil && n < 512 {
			// A short reply may be the device refusing the command.
			if b, rerr := os.ReadFile(tmp); rerr == nil {
				if msg := xfer.DeviceError(string(b)); msg != "" {
					err = fmt.Errorf("the device answered: %s", msg)
				}
			}
		}
		if err == nil {
			_ = os.Remove(localPath)
			err = os.Rename(tmp, localPath)
		} else {
			_ = os.Remove(tmp)
		}
		return err
	}), nil
}

func baseName(p string) string {
	if i := strings.LastIndexAny(p, "/:\\"); i >= 0 {
		return p[i+1:]
	}
	return p
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

// ---- this computer --------------------------------------------------------------

// LocalHome is where the local side of the Files tab starts: Downloads
// when it exists, else the home folder.
func (a *App) LocalHome() string {
	home, _ := os.UserHomeDir()
	if d := filepath.Join(home, "Downloads"); dirExists(d) {
		return d
	}
	return home
}

// FileServerFolder is the folder the file servers share unless another one
// is chosen: its own folder, so they never share Downloads or the home
// folder (with .ssh and YACM's data) to the network.
func (a *App) FileServerFolder() (string, error) {
	d := filepath.Join(a.LocalHome(), "YACM file servers")
	if err := os.MkdirAll(d, 0o700); err != nil {
		return "", err
	}
	return d, nil
}

// LocalRoots lists the drives (Windows) or / and the home folder.
func (a *App) LocalRoots() []xfer.Root {
	home, _ := os.UserHomeDir()
	roots := []xfer.Root{{Path: home, Size: -1, Free: -1}}
	if runtime.GOOS == "windows" {
		for c := 'A'; c <= 'Z'; c++ {
			d := string(c) + `:\`
			if dirExists(d) {
				roots = append(roots, xfer.Root{Path: d, Size: -1, Free: -1})
			}
		}
		return roots
	}
	return append(roots, xfer.Root{Path: "/", Size: -1, Free: -1})
}

// LocalList lists a folder on this computer.
func (a *App) LocalList(dir string) ([]xfer.Entry, error) {
	des, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	out := make([]xfer.Entry, 0, len(des))
	for _, d := range des {
		fi, err := d.Info()
		if err != nil {
			continue
		}
		p := filepath.Join(dir, d.Name())
		isDir := fi.IsDir()
		if fi.Mode()&os.ModeSymlink != 0 {
			if t, err := os.Stat(p); err == nil {
				isDir = t.IsDir()
				fi = t
			}
		}
		out = append(out, xfer.Entry{Name: d.Name(), Path: p, Size: fi.Size(), Dir: isDir, Mod: fi.ModTime()})
	}
	xfer.SortEntries(out)
	return out, nil
}

// LocalParent returns the folder above a local path ("" at the top).
func (a *App) LocalParent(p string) string {
	parent := filepath.Dir(filepath.Clean(p))
	if parent == filepath.Clean(p) {
		return ""
	}
	return parent
}

// LocalJoin joins a local folder and a name.
// The name is one path element: names come from a device, which could
// otherwise send "../../.config/autostart/x" and have a download land
// outside the chosen folder.
func (a *App) LocalJoin(dir, name string) (string, error) {
	if err := checkFileName(name); err != nil {
		return "", err
	}
	return filepath.Join(dir, name), nil
}

var windowsDevice = regexp.MustCompile(`(?i)^(?:CON|PRN|AUX|NUL|COM[0-9¹²³]|LPT[0-9¹²³]|CONIN\$|CONOUT\$)(?:\..*)?$`)

// checkFileName refuses a name that is not a single file name on this
// computer.
func checkFileName(name string) error {
	bad := `/\` + "\x00"
	if runtime.GOOS == "windows" {
		bad += ":"
	}
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, bad) {
		return fmt.Errorf("refused the file name %q: it is not a plain file name", name)
	}
	// Windows: CON, NUL, COM1 … name devices, and trailing dots or spaces
	// are dropped, so such a name would not be the file it looks like.
	if runtime.GOOS == "windows" && (windowsDevice.MatchString(name) || strings.TrimRight(name, ". ") != name) {
		return fmt.Errorf("refused the file name %q: Windows reserves it", name)
	}
	return nil
}

// LocalExists reports whether a local file exists (to ask before replacing).
func (a *App) LocalExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// LocalMkdir creates a folder on this computer.
func (a *App) LocalMkdir(p string) error { return os.Mkdir(p, 0o755) }

// RevealFile shows a file in the system file manager.
func (a *App) RevealFile(p string) error {
	dir := p
	if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
		dir = filepath.Dir(p)
	}
	return openInFileManager(dir)
}

// ChooseSaveAs asks where to save a downloaded file, in dir with name.
func (a *App) ChooseSaveAs(title, dir, name string) (string, error) {
	if dir == "" || !dirExists(dir) {
		dir = a.LocalHome()
	}
	if checkFileName(name) != nil {
		name = baseName(strings.ReplaceAll(name, `\`, "/"))
		if checkFileName(name) != nil {
			name = ""
		}
	}
	return wruntime.SaveFileDialog(a.ctx, wruntime.SaveDialogOptions{Title: title, DefaultDirectory: dir, DefaultFilename: name, ShowHiddenFiles: true})
}

// ChooseFiles picks files to send.
func (a *App) ChooseFiles(title, dir string) ([]string, error) {
	if dir == "" || !dirExists(dir) {
		dir = a.LocalHome()
	}
	return wruntime.OpenMultipleFilesDialog(a.ctx, wruntime.OpenDialogOptions{Title: title, DefaultDirectory: dir, ShowHiddenFiles: true})
}

// ChooseFolder picks a folder, starting in dir.
func (a *App) ChooseFolder(title, dir string) (string, error) {
	if dir == "" || !dirExists(dir) {
		dir = a.LocalHome()
	}
	return wruntime.OpenDirectoryDialog(a.ctx, wruntime.OpenDialogOptions{Title: title, DefaultDirectory: dir, CanCreateDirectories: true})
}

// roundTook rounds how long a transfer took for the log: tenths of a second
// below ten seconds, whole seconds above.
func roundTook(d time.Duration) time.Duration {
	if d < 10*time.Second {
		return d.Round(100 * time.Millisecond)
	}
	return d.Round(time.Second)
}
