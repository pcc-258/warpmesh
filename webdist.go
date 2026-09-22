package devicerelay

import "embed"

// WebFS holds the built web UI so the server can ship as a single binary.
//
//go:embed web/dist
var WebFS embed.FS
