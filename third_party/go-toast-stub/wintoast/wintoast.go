// Package wintoast is a no-op stand-in; see ../README.md.
package wintoast

import "errors"

// IClassFactory mirrors the upstream COM type.
type IClassFactory struct{}

// ClassFactory mirrors the upstream variable.
var ClassFactory = &IClassFactory{}

// registerClassFactory is referenced by Wails via go:linkname.
func registerClassFactory(*IClassFactory) error { return nil }

var _ = registerClassFactory

// ErrUnsupported is returned by Push.
var ErrUnsupported = errors.New("toast notifications are disabled in this build")

// Push does nothing.
func Push(appID, xml string) error { return ErrUnsupported }
