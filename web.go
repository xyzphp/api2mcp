package web

import "embed"

// Files are embedded in the Go binary; production needs no Node runtime.
//
//go:embed index.html login.html assets examples/*.yaml
var Files embed.FS
