// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (C) 2026 The YACM Authors. Additional terms under GPLv3 section 7: see NOTICE.md.

package importer

import (
	"bytes"
	"crypto/aes"
	"crypto/pbkdf2"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"golang.org/x/crypto/xts"

	"github.com/yacm-app/yacm/internal/store"
)

// Royal TS / Royal TSX documents (.rtsz) are XML: a flat list of objects
// (RoyalFolder, RoyalSSHConnection, RoyalCredential, RoyalSecureGateway …),
// each with ID, ParentID and PositionNr, from which the tree is built.
// Properties Royal leaves at their default are not written.
//
// Passwords are AES-XTS encrypted with a key derived from a constant built
// into Royal TS and the document password (empty when the document has
// none), as described publicly by HyperSine
// (github.com/HyperSine/how-does-RoyalTS-encrypt-password).

// royalKeyPrefix is the constant Royal TS puts in front of the document
// password before deriving the key.
const royalKeyPrefix = "jtWcgJq<MKE]@M#uH3yKZi]CznpP}?}VKr3r]h{<wkp%+FMwUz"

var errRoyalPassword = errors.New("wrong Royal TS document password")

// royalDecrypt reveals one encrypted Royal TS value.
func royalDecrypt(enc, docPassword string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(enc))
	if err != nil || len(raw) < 48+16 {
		return "", errors.New("not a Royal TS encrypted value")
	}
	salt, header, body := raw[:48], raw[48:64], raw[64:]
	key, err := pbkdf2.Key(sha1.New, royalKeyPrefix+docPassword, salt[:40], 1000, 32)
	if err != nil {
		return "", err
	}
	c, err := xts.NewCipher(aes.NewCipher, key)
	if err != nil {
		return "", err
	}
	h := make([]byte, 16)
	c.Decrypt(h, header, ^uint64(0)) // tweak FF×8 00×8
	if string(h[:4]) != "XTS1" {
		return "", errRoyalPassword
	}
	length := binary.LittleEndian.Uint64(h[4:12])
	block := int(binary.LittleEndian.Uint32(h[12:16]))
	padded := (length + 15) / 16 * 16
	if uint64(len(body)) != padded || block <= 0 || block > 1<<20 || block%16 != 0 {
		return "", errors.New("damaged Royal TS encrypted value")
	}
	out := make([]byte, 0, len(body))
	for i := 0; i*block < len(body); i++ {
		part := body[i*block : min((i+1)*block, len(body))]
		plain := make([]byte, len(part))
		c.Decrypt(plain, part, uint64(i))
		out = append(out, plain...)
	}
	out = out[:length]
	if !utf8.Valid(out) {
		return "", errRoyalPassword
	}
	return string(out), nil
}

// royalObj is one object of the document with its properties.
type royalObj struct {
	kind  string // element name: RoyalFolder, RoyalSSHConnection …
	props map[string]string
	xmlUp string // ID of the enclosing object, for documents that nest
}

func (o *royalObj) get(k string) string { return strings.TrimSpace(o.props[k]) }
func (o *royalObj) is(k string) bool    { return strings.EqualFold(o.get(k), "true") }
func (o *royalObj) num(k string, def int) int {
	if n, err := strconv.Atoi(o.get(k)); err == nil {
		return n
	}
	return def
}

// parseRoyal reads every Royal* element that has an ID, wherever it is.
func parseRoyal(data []byte) ([]*royalObj, error) {
	d := xml.NewDecoder(bytes.NewReader(data))
	d.Strict = false
	type frame struct {
		obj  *royalObj // the object this element is (nil: a property or other)
		name string
		text strings.Builder
	}
	var stack []*frame
	var objs []*royalObj
	found := false
	for {
		tok, err := d.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("damaged Royal TS document: %w", err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			f := &frame{name: t.Name.Local}
			if strings.HasPrefix(t.Name.Local, "Royal") {
				found = true
				f.obj = &royalObj{kind: t.Name.Local, props: map[string]string{}}
				for _, a := range t.Attr {
					f.obj.props[a.Name.Local] = a.Value
				}
				for i := len(stack) - 1; i >= 0; i-- {
					if o := stack[i].obj; o != nil {
						f.obj.xmlUp = o.get("ID")
						break
					}
				}
			}
			stack = append(stack, f)
		case xml.CharData:
			if len(stack) > 0 {
				stack[len(stack)-1].text.Write(t)
			}
		case xml.EndElement:
			if len(stack) == 0 {
				continue
			}
			f := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if f.obj != nil {
				if f.obj.get("ID") != "" {
					objs = append(objs, f.obj)
				}
				continue
			}
			// A property: the text of a leaf element directly inside an object.
			if len(stack) > 0 {
				if o := stack[len(stack)-1].obj; o != nil {
					if _, set := o.props[f.name]; !set {
						o.props[f.name] = f.text.String()
					}
				}
			}
		}
	}
	if !found {
		return nil, errors.New("not a Royal TS document")
	}
	return objs, nil
}

// importRoyalTS builds the YACM tree from a Royal TS document. A password
// protected document needs its password for the saved passwords.
func importRoyalTS(data []byte, parentID string, opts Options) (*Result, error) {
	objs, err := parseRoyal(data)
	if err != nil {
		return nil, err
	}
	b := newBuilder("Royal TS", parentID)
	byID := map[string]*royalObj{}
	kids := map[string][]*royalObj{}
	docID, needsPassword := "", false
	for _, o := range objs {
		byID[o.get("ID")] = o
		if o.kind == "RoyalDocument" {
			docID = o.get("ID")
			needsPassword = o.get("PasswordHash") != ""
		}
	}
	parentOf := func(o *royalObj) string {
		if p := o.get("ParentID"); p != "" {
			return p
		}
		return o.xmlUp
	}
	for _, o := range objs {
		if o.kind != "RoyalDocument" {
			kids[parentOf(o)] = append(kids[parentOf(o)], o)
		}
	}
	for _, list := range kids {
		sort.SliceStable(list, func(i, j int) bool { return list[i].num("PositionNr", 0) < list[j].num("PositionNr", 0) })
	}
	inTrash := func(o *royalObj) bool {
		for cur, guard := o, 0; cur != nil && guard < 64; guard++ {
			if cur.kind == "RoyalTrash" {
				return true
			}
			cur = byID[parentOf(cur)]
		}
		return false
	}
	pwFailed := false
	decrypt := func(enc string) string {
		if strings.TrimSpace(enc) == "" {
			return ""
		}
		if needsPassword && opts.Password == "" {
			b.failed()
			return ""
		}
		p, err := royalDecrypt(enc, opts.Password)
		if err != nil {
			if errors.Is(err, errRoyalPassword) {
				pwFailed = true
			}
			b.failed()
			return ""
		}
		return p
	}

	// Credentials become credential profiles.
	creds := map[string]string{} // Royal ID -> profile ID
	credByName := map[string]string{}
	for _, o := range objs {
		if o.kind != "RoyalCredential" || inTrash(o) {
			continue
		}
		c := &store.Credential{ID: store.NewID(), Name: o.get("Name"), Description: "From Royal TS", User: o.get("UserName"), Auth: store.AuthPassword}
		if c.Name == "" {
			c.Name = c.User
		}
		creds[o.get("ID")] = c.ID
		credByName[strings.ToLower(c.Name)] = c.ID
		b.res.Credentials = append(b.res.Credentials, c)
		b.secret(store.SecretCredPassword(c.ID), decrypt(o.get("Password")))
	}
	// Secure Gateways become SSH sessions used as jump hosts.
	gateways := map[string]string{} // Royal ID -> node ID
	gwByName := map[string]string{}
	for _, o := range objs {
		if o.kind == "RoyalSecureGateway" && !inTrash(o) {
			gateways[o.get("ID")] = store.NewID()
			gwByName[strings.ToLower(o.get("Name"))] = gateways[o.get("ID")]
		}
	}
	profile := func(o *royalObj) string {
		if id := creds[o.get("CredentialId")]; id != "" {
			return id
		}
		if id := creds[o.get("CredentialID")]; id != "" {
			return id
		}
		return credByName[strings.ToLower(o.get("CredentialName"))]
	}
	// jump is the jump host an object asks for ("" none, inherit, a node).
	jump := func(o *royalObj, name string) string {
		if o.is("SecureGatewayFromParent") {
			return store.Inherit
		}
		mode := o.num("SecureGatewayUsageMode", 0)
		if mode == 0 {
			return ""
		}
		id := gateways[o.get("SecureGatewayID")]
		if id == "" {
			id = gwByName[strings.ToLower(o.get("SecureGatewayName"))]
		}
		if id == "" {
			b.warn(name, "its Secure Gateway is not in the document; it connects directly")
			return ""
		}
		if mode == 2 {
			b.warn(name, "the Secure Gateway is used \"on demand\" in Royal TS; in YACM it is always used")
		}
		return id
	}

	var walk func(parent, nodeParent string, order *int)
	addNode := func(n *store.Node, nodeParent string, order *int) bool {
		if err := store.Validate(n); err != nil {
			b.skip(n.Name, "", err.Error())
			return false
		}
		n.ParentID, n.Order = nodeParent, *order
		*order++
		b.res.Nodes = append(b.res.Nodes, n)
		if n.IsGroup {
			b.res.Report.Groups++
		} else {
			b.res.Report.Sessions++
		}
		return true
	}
	walk = func(parent, nodeParent string, order *int) {
		for _, o := range kids[parent] {
			name := o.get("Name")
			switch o.kind {
			case "RoyalTrash", "RoyalCredential", "RoyalDocument":
				continue
			case "RoyalFolder":
				g := &store.Node{ID: store.NewID(), IsGroup: true, Expanded: true, Name: name, Description: o.get("Description")}
				d := &store.GroupDefaults{}
				if o.is("CredentialFromParent") {
					d.CredentialID = store.Inherit
				} else if o.num("CredentialMode", 0) == 3 {
					d.CredentialID = profile(o)
				} else if o.num("CredentialMode", 0) == 2 && o.get("CredentialUsername") != "" {
					b.warn(name, "the folder's own login is not taken over (YACM groups use credential profiles); its sessions ask")
				}
				d.JumpVia = jump(o, name)
				if d.CredentialID == store.Inherit {
					d.CredentialID = ""
				}
				if d.JumpVia == store.Inherit {
					d.JumpVia = ""
				}
				if !d.IsEmpty() {
					g.Defaults = d
				}
				if !addNode(g, nodeParent, order) {
					continue
				}
				sub := 0
				walk(o.get("ID"), g.ID, &sub)
			case "RoyalSecureGateway":
				n := &store.Node{ID: gateways[o.get("ID")], Name: name, Description: o.get("Description")}
				ss := &store.Session{Protocol: store.ProtoSSH, Algorithms: store.AlgoCompatible, Host: o.get("SecureGatewayHost"), Port: o.num("SecureGatewayPort", 22)}
				pw := ""
				if c := creds[o.get("SecureGatewayCredentialID")]; c != "" {
					ss.CredentialID = c
				} else if c := credByName[strings.ToLower(o.get("SecureGatewayCredentialName"))]; c != "" {
					ss.CredentialID = c
				} else if u := o.get("SecureGatewayUsername"); u != "" {
					ss.User = u
					pw = decrypt(o.get("SecureGatewayPassword"))
				}
				ss.Auth = store.AuthAsk
				if pw != "" || ss.CredentialID != "" {
					ss.Auth = store.AuthPassword
				}
				if dep := gateways[o.get("DependsOnSecureGatewayID")]; dep != "" && dep != n.ID {
					ss.JumpVia = dep
				}
				n.Session = ss
				if addNode(n, nodeParent, order) {
					b.secret(store.SecretPassword(n.ID), pw)
				}
			case "RoyalSSHConnection":
				royalSession(b, o, nodeParent, order, addNode, profile, jump, decrypt)
			default:
				if strings.HasSuffix(o.kind, "Connection") || strings.HasPrefix(o.kind, "RoyalRDS") {
					b.skip(name, strings.TrimPrefix(o.kind, "Royal"), "only SSH, telnet and serial connections are imported")
				} else if len(kids[o.get("ID")]) > 0 {
					walk(o.get("ID"), nodeParent, order)
				}
			}
		}
	}
	top := 0
	if docID != "" {
		walk(docID, parentID, &top)
	}
	// Objects whose parent is not in the document (a partial export).
	for p := range kids {
		if p != docID && byID[p] == nil {
			walk(p, parentID, &top)
		}
	}
	res := b.done()
	if pwFailed {
		res.Report.Warnings = append(res.Report.Warnings, "the Royal TS document password is wrong: saved passwords were not imported")
	}
	return res, nil
}

func royalSession(b *builder, o *royalObj, nodeParent string, order *int, addNode func(*store.Node, string, *int) bool,
	profile func(*royalObj) string, jump func(*royalObj, string) string, decrypt func(string) string) {
	name := o.get("Name")
	n := &store.Node{ID: store.NewID(), Name: name, Description: o.get("Description")}
	ss := &store.Session{Host: o.get("URI")}
	switch {
	case o.is("IsSerialPortConnection"):
		ss.Protocol = store.ProtoSerial
		parity := map[int]string{0: "none", 1: "odd", 2: "even", 3: "mark", 4: "space"}[o.num("Parity", 0)]
		stop := map[int]string{1: "1", 2: "2", 3: "1.5"}[o.num("StopBits", 1)]
		ss.Serial = &store.Serial{Device: o.get("PortName"), Baud: o.num("BaudRate", 9600), DataBits: o.num("DataBits", 8), Parity: parity, StopBits: stop}
		ss.Host = ""
		if h := o.num("Handshake", 0); h != 0 {
			b.warn(name, "Royal TS uses flow control (handshake) on this port; YACM opens it without")
		}
	case o.is("IsTelnetConnection"):
		ss.Protocol, ss.Port = store.ProtoTelnet, o.num("Port", 23)
	default:
		ss.Protocol, ss.Algorithms, ss.Port = store.ProtoSSH, store.AlgoCompatible, o.num("Port", 22)
	}
	pw := ""
	if ss.Protocol != store.ProtoSerial {
		switch {
		case o.is("CredentialFromParent"):
			ss.CredentialID = store.Inherit
		case o.num("CredentialMode", 0) == 3:
			ss.CredentialID = profile(o)
		case o.num("CredentialMode", 0) == 2:
			ss.User = o.get("CredentialUsername")
			pw = decrypt(o.get("CredentialPassword"))
		}
		if j := jump(o, name); j != "" {
			ss.JumpVia = j
		}
	}
	key := o.get("PrivateKeyPath")
	if key == "" {
		key = o.get("CredentialKeyFile")
	}
	passphrase := ""
	if ss.Protocol == store.ProtoSSH {
		ss.Auth = store.AuthAsk
		switch {
		case key != "":
			ss.Auth, ss.KeyPath = store.AuthPublicKey, key
			passphrase = decrypt(firstNonEmpty(o.get("Passphrase"), o.get("CredentialPassphrase")))
			if store.IsNetworkPath(key) {
				b.warn(name, "its key file is on another computer and was left out")
				ss.Auth, ss.KeyPath = store.AuthAsk, ""
			}
		case pw != "" || ss.CredentialID != "":
			ss.Auth = store.AuthPassword
		}
	}
	n.Session = ss
	if addNode(n, nodeParent, order) {
		b.secret(store.SecretPassword(n.ID), pw)
		b.secret(store.SecretPassphrase(n.ID), passphrase)
	}
}

func firstNonEmpty(s ...string) string {
	for _, x := range s {
		if x != "" {
			return x
		}
	}
	return ""
}
