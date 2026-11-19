// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (C) 2026 The YACM Authors. Additional terms under GPLv3 section 7: see NOTICE.md.

package importer

import (
	"crypto/aes"
	"crypto/pbkdf2"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"strings"
	"testing"

	"golang.org/x/crypto/xts"

	"github.com/yacm-app/yacm/internal/store"
)

// The examples published with the description of the scheme.
func TestRoyalDecryptPublishedExamples(t *testing.T) {
	for _, c := range []struct{ enc, pw string }{
		{"FLBmVEbJNRahI5kXsGe95WVJtsG7BbutepAbzrRZH1YcNqBO34TcxYoTvw1hUiuJtJPHFW6vkK/g4Gtgr8wvFIIGu1WupKnWVkQRYbk/Mmg=", ""},
		{"UzXEW845XJpD2ZWRLhrzIo4Ex8iC8nu0ra5SbaBykDHk6lCfi2mvUz1CHLEm/TRL2CGDVgoN4LiygLHG4Cb9i7BohvPpoiG0jFwXCi4mYHY=", "royal_pass"},
	} {
		got, err := royalDecrypt(c.enc, c.pw)
		if err != nil || got != "hypersine123456" {
			t.Fatalf("%q: %q %v", c.pw, got, err)
		}
	}
	if _, err := royalDecrypt("UzXEW845XJpD2ZWRLhrzIo4Ex8iC8nu0ra5SbaBykDHk6lCfi2mvUz1CHLEm/TRL2CGDVgoN4LiygLHG4Cb9i7BohvPpoiG0jFwXCi4mYHY=", "wrong"); err != errRoyalPassword {
		t.Fatalf("wrong password: %v", err)
	}
}

// royalEncrypt writes a value the way Royal TS does (fixed salt: tests only).
func royalEncrypt(plain, docPassword string) string {
	salt := make([]byte, 48)
	for i := range salt {
		salt[i] = byte(i * 7)
	}
	key, _ := pbkdf2.Key(sha1.New, royalKeyPrefix+docPassword, salt[:40], 1000, 32)
	c, _ := xts.NewCipher(aes.NewCipher, key)
	h := make([]byte, 16)
	copy(h, "XTS1")
	binary.LittleEndian.PutUint64(h[4:], uint64(len(plain)))
	binary.LittleEndian.PutUint32(h[12:], 0x10000)
	head := make([]byte, 16)
	c.Encrypt(head, h, ^uint64(0))
	body := []byte(plain)
	for len(body)%16 != 0 {
		body = append(body, 0)
	}
	enc := make([]byte, len(body))
	c.Encrypt(enc, body, 0)
	return base64.StdEncoding.EncodeToString(append(append(salt, head...), enc...))
}

// royalDoc is an invented document in the layout of Royal TS 7: folders
// with logins and a Secure Gateway, SSH, telnet, serial, a key, a trashed
// connection and an RDP connection.
func royalDoc(docPassword string) string {
	e := func(s string) string { return royalEncrypt(s, docPassword) }
	hash := ""
	if docPassword != "" {
		hash = "c29tZWhhc2g="
	}
	return fmt.Sprintf(`<?xml version="1.0" encoding="utf-8"?>
<RTSZDocument>
<RoyalDocument><ID>doc-1</ID><Name>Test-Doc-2026</Name><PasswordHash>%s</PasswordHash></RoyalDocument>
<RoyalFolder><ID>f-conn</ID><ParentID>doc-1</ParentID><Name>Connections</Name><PositionNr>0</PositionNr></RoyalFolder>
<RoyalFolder><ID>f-cred</ID><ParentID>doc-1</ParentID><Name>Credentials</Name><PositionNr>1</PositionNr></RoyalFolder>
<RoyalTrash><ID>trash</ID><ParentID>doc-1</ParentID><Name>Trash</Name></RoyalTrash>
<RoyalCredential><ID>c-1</ID><ParentID>f-cred</ParentID><Name>Company login</Name><UserName>netadmin</UserName><Password>%s</Password></RoyalCredential>
<RoyalSecureGateway><ID>gw-1</ID><ParentID>f-conn</ParentID><Name>Bastion</Name><PositionNr>9</PositionNr><SecureGatewayHost>192.0.2.1</SecureGatewayHost><SecureGatewayPort>2222</SecureGatewayPort><SecureGatewayCredentialMode>1</SecureGatewayCredentialMode><SecureGatewayUsername>jump</SecureGatewayUsername><SecureGatewayPassword>%s</SecureGatewayPassword></RoyalSecureGateway>
<RoyalFolder><ID>f-co</ID><ParentID>f-conn</ParentID><Name>Company</Name><PositionNr>1</PositionNr><CredentialMode>3</CredentialMode><CredentialId>c-1</CredentialId><SecureGatewayUsageMode>2</SecureGatewayUsageMode><SecureGatewayID>gw-1</SecureGatewayID></RoyalFolder>
<RoyalSSHConnection><ID>s-1</ID><ParentID>f-co</ParentID><Name>SSH1</Name><PositionNr>1</PositionNr><URI>192.0.2.11</URI><Port>22</Port><CredentialFromParent>True</CredentialFromParent><SecureGatewayFromParent>True</SecureGatewayFromParent><Description>core switch</Description></RoyalSSHConnection>
<RoyalSSHConnection><ID>s-2</ID><ParentID>f-co</ParentID><Name>Telnet1</Name><PositionNr>0</PositionNr><URI>192.0.2.12</URI><Port>23</Port><IsTelnetConnection>True</IsTelnetConnection><CredentialMode>2</CredentialMode><CredentialUsername>admin</CredentialUsername><CredentialPassword>%s</CredentialPassword></RoyalSSHConnection>
<RoyalSSHConnection><ID>s-3</ID><ParentID>f-conn</ParentID><Name>Console</Name><PositionNr>2</PositionNr><IsSerialPortConnection>True</IsSerialPortConnection><PortName>COM4</PortName><BaudRate>115200</BaudRate><DataBits>8</DataBits><Parity>2</Parity><StopBits>1</StopBits></RoyalSSHConnection>
<RoyalSSHConnection><ID>s-4</ID><ParentID>f-conn</ParentID><Name>KeyHost</Name><PositionNr>3</PositionNr><URI>192.0.2.14</URI><PrivateKeyPath>C:\keys\id_ed25519</PrivateKeyPath><Passphrase>%s</Passphrase></RoyalSSHConnection>
<RoyalSSHConnection><ID>s-5</ID><ParentID>trash</ParentID><Name>Old</Name><URI>192.0.2.99</URI></RoyalSSHConnection>
<RoyalRDSConnection><ID>r-1</ID><ParentID>f-conn</ParentID><Name>Windows server</Name><URI>192.0.2.50</URI></RoyalRDSConnection>
</RTSZDocument>`, hash, e("Cred-Pass-1"), e("Jump-Pass-1"), e("Lozinka-1"), e("Key-Pass-1"))
}

func TestRoyalTSImport(t *testing.T) {
	if k, err := detectData("Test.rtsz", []byte(royalDoc(""))); err != nil || k != KindRoyalTS {
		t.Fatalf("detect: %v %v", k, err)
	}
	for _, docPw := range []string{"", "Doc-Pass-2026"} {
		res, err := importRoyalTS([]byte(royalDoc(docPw)), "", Options{Password: docPw})
		if err != nil {
			t.Fatal(err)
		}
		r := res.Report
		if r.Sessions != 5 || r.SecretsFailed != 0 || len(r.Skipped) != 1 || len(res.Credentials) != 1 {
			t.Fatalf("%q: report %+v", docPw, r)
		}
		by := map[string]*store.Node{}
		for _, n := range res.Nodes {
			by[n.Name] = n
		}
		if by["Old"] != nil || by["Credentials"] != nil || by["Trash"] != nil {
			t.Fatal("trash or empty folders imported")
		}
		cred := res.Credentials[0]
		co, gw := by["Company"], by["Bastion"]
		if co.ParentID != by["Connections"].ID || co.Defaults.CredentialID != cred.ID || co.Defaults.JumpVia != gw.ID {
			t.Fatalf("Company %+v", co.Defaults)
		}
		if s := by["SSH1"].Session; s.CredentialID != store.Inherit || s.JumpVia != store.Inherit || s.Protocol != store.ProtoSSH || by["SSH1"].Description != "core switch" {
			t.Fatalf("SSH1 %+v", s)
		}
		// Order follows PositionNr.
		if by["Telnet1"].Order > by["SSH1"].Order {
			t.Fatal("order")
		}
		tn := by["Telnet1"]
		if tn.Session.Protocol != store.ProtoTelnet || tn.Session.User != "admin" || res.Secrets[store.SecretPassword(tn.ID)] != "Lozinka-1" {
			t.Fatalf("Telnet1 %+v", tn.Session)
		}
		if g := gw.Session; g.Host != "192.0.2.1" || g.Port != 2222 || g.User != "jump" || res.Secrets[store.SecretPassword(gw.ID)] != "Jump-Pass-1" {
			t.Fatalf("gateway %+v", g)
		}
		if res.Secrets[store.SecretCredPassword(cred.ID)] != "Cred-Pass-1" || cred.User != "netadmin" || cred.Name != "Company login" {
			t.Fatalf("credential %+v", cred)
		}
		con := by["Console"].Session
		if con.Protocol != store.ProtoSerial || con.Serial.Device != "COM4" || con.Serial.Baud != 115200 || con.Serial.Parity != "even" {
			t.Fatalf("serial %+v", con.Serial)
		}
		k := by["KeyHost"]
		if k.Session.Auth != store.AuthPublicKey || k.Session.KeyPath != `C:\keys\id_ed25519` || res.Secrets[store.SecretPassphrase(k.ID)] != "Key-Pass-1" {
			t.Fatalf("key %+v", k.Session)
		}
		if !strings.Contains(strings.Join(r.Warnings, "\n"), "on demand") {
			t.Fatalf("warnings %v", r.Warnings)
		}
	}
	// Without the document password, or a wrong one: sessions, no passwords.
	for _, pw := range []string{"", "wrong"} {
		res, err := importRoyalTS([]byte(royalDoc("Doc-Pass-2026")), "", Options{Password: pw})
		if err != nil || res.Report.Sessions != 5 || len(res.Secrets) != 0 || res.Report.SecretsFailed != 4 {
			t.Fatalf("password %q: %v %+v", pw, err, res.Report)
		}
	}
}
