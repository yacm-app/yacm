// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (C) 2026 The YACM Authors. Additional terms under GPLv3 section 7: see NOTICE.md.

package core

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/yacm-app/yacm/internal/applog"
	"github.com/yacm-app/yacm/internal/pwsource"
	"github.com/yacm-app/yacm/internal/sshconn"
	"github.com/yacm-app/yacm/internal/store"
)

// Credential profiles can take their password from KeePass or Bitwarden
// (store.SecretSource). The databases are unlocked when a session needs
// them (YACM asks for their master password) or in Settings, kept open
// while YACM runs, and locked with the vault.

type pwManagers struct {
	mu sync.Mutex
	kp *pwsource.KeePass
	bw pwsource.Bitwarden
}

func (p *pwManagers) lock() {
	p.mu.Lock()
	p.kp = nil
	p.mu.Unlock()
	p.bw.Lock()
}

// PasswordManagers says which password managers are set up and unlocked.
type PasswordManagers struct {
	KeePassFile       string `json:"keepassFile"`
	KeePassUnlocked   bool   `json:"keepassUnlocked"`
	BitwardenUnlocked bool   `json:"bitwardenUnlocked"`
}

// PasswordManagerStatus reports the password managers.
func (a *App) PasswordManagerStatus() PasswordManagers {
	st := a.store.Settings()
	a.pm.mu.Lock()
	kpOpen := a.pm.kp != nil && a.pm.kp.Path() == st.KeePassFile
	a.pm.mu.Unlock()
	return PasswordManagers{KeePassFile: st.KeePassFile, KeePassUnlocked: kpOpen, BitwardenUnlocked: a.pm.bw.Unlocked()}
}

// UnlockKeePass opens the KeePass database of Settings.
func (a *App) UnlockKeePass(password string) error {
	st := a.store.Settings()
	if st.KeePassFile == "" {
		return errors.New("choose the KeePass database in Settings > Integrations first")
	}
	kp, err := pwsource.OpenKeePass(st.KeePassFile, password, st.KeePassKeyFile)
	if err != nil {
		return err
	}
	a.pm.mu.Lock()
	a.pm.kp = kp
	a.pm.mu.Unlock()
	applog.Infof("KeePass database unlocked")
	return nil
}

// UnlockBitwarden unlocks Bitwarden through its bw program.
func (a *App) UnlockBitwarden(password string) error {
	a.pm.bw.Program = a.store.Settings().BitwardenProgram
	if err := a.pm.bw.Unlock(context.Background(), password); err != nil {
		return err
	}
	applog.Infof("Bitwarden unlocked")
	return nil
}

// LockPasswordManagers forgets the unlocked KeePass database and the
// Bitwarden session.
func (a *App) LockPasswordManagers() { a.pm.lock() }

// CheckSecretSource looks an entry up (for the profile editor). It never
// returns the password.
func (a *App) CheckSecretSource(src store.SecretSource) error {
	_, err := a.sourcePassword(context.Background(), &src)
	return err
}

func (a *App) sourcePassword(ctx context.Context, src *store.SecretSource) (string, error) {
	switch src.Kind {
	case "keepass":
		a.pm.mu.Lock()
		kp := a.pm.kp
		a.pm.mu.Unlock()
		if kp == nil || kp.Path() != a.store.Settings().KeePassFile {
			return "", errors.New("KeePass is locked")
		}
		return kp.Password(src.Ref)
	case "bitwarden":
		a.pm.bw.Program = a.store.Settings().BitwardenProgram
		return a.pm.bw.Password(ctx, src.Ref)
	}
	return "", errors.New("unknown password manager")
}

// managedSecret answers a vault name for the password of a credential
// profile whose password comes from a password manager.
func (a *App) managedSecret(name string) (string, bool) {
	id, ok := strings.CutPrefix(name, "cred/")
	if !ok {
		return "", false
	}
	id, ok = strings.CutSuffix(id, "/password")
	if !ok {
		return "", false
	}
	c, ok := a.store.Credential(id)
	if !ok || c.Source == nil {
		return "", false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	v, err := a.sourcePassword(ctx, c.Source)
	if err != nil {
		applog.Warnf("credential profile %s: %v", c.Name, err)
		return "", true
	}
	return v, true
}

// unlockForSession asks for the master password of each locked password
// manager the session's logins (its own and its jump hosts') come from.
func (a *App) unlockForSession(ctx context.Context, t *tab, eff *store.Effective) {
	keys := []string{eff.PasswordKey}
	for _, h := range eff.Hops {
		keys = append(keys, h.PasswordKey)
	}
	need := map[string]bool{}
	for _, k := range keys {
		id, ok := strings.CutPrefix(k, "cred/")
		if !ok {
			continue
		}
		if c, ok := a.store.Credential(strings.TrimSuffix(id, "/password")); ok && c.Source != nil {
			need[c.Source.Kind] = true
		}
	}
	st := a.PasswordManagerStatus()
	for _, kind := range []string{"keepass", "bitwarden"} {
		if !need[kind] || (kind == "keepass" && st.KeePassUnlocked) || (kind == "bitwarden" && st.BitwardenUnlocked) {
			continue
		}
		title, unlock := "Unlock KeePass", a.UnlockKeePass
		msg := "Master password of the KeePass database " + st.KeePassFile
		if kind == "bitwarden" {
			title, unlock, msg = "Unlock Bitwarden", a.UnlockBitwarden, "Bitwarden master password"
		}
		for retry := false; ; retry = true {
			ans, err := a.ask(ctx, map[string]any{"kind": "secret", "tabId": t.id, "session": t.node.Name,
				"secret": sshconn.SecretRequest{Kind: "password", Title: title, Message: msg, Retry: retry}})
			if err != nil || !ans.Accept {
				a.tabLog(t, "%s: not unlocked, so its passwords are asked for instead", strings.TrimPrefix(title, "Unlock "))
				break
			}
			if err := unlock(ans.Value); err != nil {
				a.tabLog(t, "%v", err)
				continue
			}
			break
		}
	}
}
