package node

import _ "embed"

// AIGuideMD is the operating contract embedded into the node
// binary and served to local HTTP and MCP clients.
//
//go:embed ai-guide.md
var AIGuideMD string
