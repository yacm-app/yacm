// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (C) 2026 The YACM Authors. Additional terms under GPLv3 section 7: see NOTICE.md.

package sshconn

import (
	"context"
	"crypto/dsa" //nolint:staticcheck // very old devices still use DSA
	"crypto/rand"
	"encoding/asn1"
	"encoding/pem"
	"errors"
	"math/big"
	"os"
	"path/filepath"
	"testing"

	"github.com/yacm-app/yacm/internal/store"
	"golang.org/x/crypto/ssh"
)

func dsaKey(t *testing.T) *dsa.PrivateKey {
	t.Helper()
	k := &dsa.PrivateKey{}
	if err := dsa.GenerateParameters(&k.Parameters, rand.Reader, dsa.L1024N160); err != nil {
		t.Fatal(err)
	}
	if err := dsa.GenerateKey(k, rand.Reader); err != nil {
		t.Fatal(err)
	}
	return k
}

// Very old devices have only a DSA (ssh-dss) host key, and some people log
// in with DSA keys: both keep working with the Legacy set, and the host key
// is refused by the others. This guards the x/crypto build YACM uses (DSA
// was removed upstream in x/crypto 0.59).
func TestDSAHostKeyAndLogin(t *testing.T) {
	hostKey, err := ssh.NewSignerFromKey(dsaKey(t))
	if err != nil {
		t.Fatalf("DSA host key: %v", err)
	}
	user := dsaKey(t)
	userPub, err := ssh.NewPublicKey(&user.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &ssh.ServerConfig{PublicKeyCallback: func(c ssh.ConnMetadata, k ssh.PublicKey) (*ssh.Permissions, error) {
		if string(k.Marshal()) != string(userPub.Marshal()) {
			return nil, errors.New("unknown key")
		}
		return &ssh.Permissions{}, nil
	}}
	cfg.AddHostKey(hostKey)
	srv := startServer(t, cfg, false)

	// The OpenSSL "DSA PRIVATE KEY" file ssh-keygen used to write.
	der, err := asn1.Marshal(struct {
		Version       int
		P, Q, G, Y, X *big.Int
	}{0, user.P, user.Q, user.G, user.Y, user.X})
	if err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(t.TempDir(), "id_dsa")
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "DSA PRIVATE KEY", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	kh := NewKnownHosts(filepath.Join(t.TempDir(), "known_hosts"), false)
	dial := func(p store.AlgoProfile) (*fakePrompter, error) {
		pr := &fakePrompter{acceptKey: true}
		c, err := Dial(context.Background(), Config{
			Hops:       []Hop{{Host: srv.host, Port: srv.port, User: "admin", Auth: store.AuthPublicKey, KeyPath: keyPath}},
			KnownHosts: kh, Prompter: pr, Profile: p,
		})
		if err == nil {
			c.Close()
		}
		return pr, err
	}
	if _, err := dial(store.AlgoSecure); err == nil {
		t.Fatal("Secure set accepted a DSA host key")
	}
	pr, err := dial(store.AlgoLegacy)
	if err != nil {
		t.Fatalf("Legacy set: %v", err)
	}
	if len(pr.hostKeys) != 1 || pr.hostKeys[0].Fingerprint != ssh.FingerprintSHA256(hostKey.PublicKey()) {
		t.Fatalf("host key question %+v", pr.hostKeys)
	}
}
