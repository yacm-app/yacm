// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (C) 2026 The YACM Authors. Additional terms under GPLv3 section 7: see NOTICE.md.

package sshconn

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"io"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"golang.org/x/crypto/ssh"

	"github.com/yacm-app/yacm/internal/store"
)

// An old device with a 768-bit RSA host key: refused (with a hint) unless
// the session uses the Legacy algorithm set; then accepted, with its size
// shown in the host key question.
func TestShortRSAHostKeyNeedsLegacy(t *testing.T) {
	t.Setenv("GODEBUG", "rsa1024min=0")
	k, err := rsa.GenerateKey(rand.Reader, 768)
	if err != nil {
		t.Fatal(err)
	}
	os.Setenv("GODEBUG", "")   // Go's default again: keys under 1024 bits refused
	shortRSAOnce = sync.Once{} // an earlier Legacy test may have switched it on already
	pub, err := ssh.NewPublicKey(&k.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	hk := &oldDeviceSigner{k: k, pub: pub}
	srv := startServer(t, passwordServer(t, hk, "s3cret"), false)
	kh := NewKnownHosts(filepath.Join(t.TempDir(), "known_hosts"), false)
	dial := func(p store.AlgoProfile) (*fakePrompter, error) {
		pr := &fakePrompter{acceptKey: true, answers: []string{"s3cret"}}
		c, err := Dial(context.Background(), Config{
			Hops:       []Hop{{Host: srv.host, Port: srv.port, User: "admin", Auth: store.AuthPassword, PasswordKey: "pw"}},
			KnownHosts: kh, Prompter: pr, Profile: p,
		})
		if err == nil {
			c.Close()
		}
		return pr, err
	}
	if _, err := dial(store.AlgoSecure); err == nil || !strings.Contains(err.Error(), `set Algorithms to "Legacy"`) {
		t.Fatalf("secure set: %v", err)
	}
	pr, err := dial(store.AlgoLegacy)
	if err != nil {
		t.Fatalf("legacy set: %v", err)
	}
	if len(pr.hostKeys) != 1 || pr.hostKeys[0].KeyType != "ssh-rsa (768-bit, weak)" {
		t.Fatalf("host key question %+v", pr.hostKeys)
	}
	// Go accepts such keys now, YACM still only with the Legacy set.
	if _, err := dial(store.AlgoCompatible); err == nil || !strings.Contains(err.Error(), "too short (768 bits)") {
		t.Fatalf("compatible set after a legacy session: %v", err)
	}
}

// oldDeviceSigner signs like an old device with a short RSA key, which Go's
// crypto/rsa refuses to do: PKCS #1 v1.5 by hand.
type oldDeviceSigner struct {
	k   *rsa.PrivateKey
	pub ssh.PublicKey
}

func (s *oldDeviceSigner) PublicKey() ssh.PublicKey { return s.pub }

func (s *oldDeviceSigner) Sign(r io.Reader, data []byte) (*ssh.Signature, error) {
	return s.SignWithAlgorithm(r, data, ssh.KeyAlgoRSA)
}

func (s *oldDeviceSigner) SignWithAlgorithm(_ io.Reader, data []byte, algo string) (*ssh.Signature, error) {
	var h crypto.Hash
	var prefix []byte
	switch algo {
	case ssh.KeyAlgoRSASHA512:
		h, prefix = crypto.SHA512, []byte{0x30, 0x51, 0x30, 0x0d, 0x06, 0x09, 0x60, 0x86, 0x48, 0x01, 0x65, 0x03, 0x04, 0x02, 0x03, 0x05, 0x00, 0x04, 0x40}
	case ssh.KeyAlgoRSASHA256:
		h, prefix = crypto.SHA256, []byte{0x30, 0x31, 0x30, 0x0d, 0x06, 0x09, 0x60, 0x86, 0x48, 0x01, 0x65, 0x03, 0x04, 0x02, 0x01, 0x05, 0x00, 0x04, 0x20}
	default:
		algo = ssh.KeyAlgoRSA
		h, prefix = crypto.SHA1, []byte{0x30, 0x21, 0x30, 0x09, 0x06, 0x05, 0x2b, 0x0e, 0x03, 0x02, 0x1a, 0x05, 0x00, 0x04, 0x14}
	}
	var sum []byte
	switch h {
	case crypto.SHA512:
		x := sha512.Sum512(data)
		sum = x[:]
	case crypto.SHA256:
		x := sha256.Sum256(data)
		sum = x[:]
	default:
		x := sha1.Sum(data)
		sum = x[:]
	}
	size := (s.k.N.BitLen() + 7) / 8
	t := append(prefix, sum...)
	em := append([]byte{0, 1}, bytes.Repeat([]byte{0xff}, size-len(t)-3)...)
	em = append(append(em, 0), t...)
	c := new(big.Int).Exp(new(big.Int).SetBytes(em), s.k.D, s.k.N)
	sig := c.FillBytes(make([]byte, size))
	return &ssh.Signature{Format: algo, Blob: sig}, nil
}
