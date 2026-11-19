// Package tmpl is a no-op stand-in; see ../README.md.
package tmpl

import "text/template"

// XMLTemplate renders nothing: notifications are never sent in this build.
var XMLTemplate = template.Must(template.New("toast").Parse(""))
