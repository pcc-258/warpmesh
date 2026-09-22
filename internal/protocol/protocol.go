// Package protocol defines the JSON messages exchanged between the browser,
// the relay server and the device agents.
package protocol

import "encoding/base64"

// Message is the shared envelope for all WebSocket traffic.
type Message struct {
	Type string `json:"type"`

	// Agent hello metadata.
	DeviceID   string   `json:"deviceId,omitempty"`
	Name       string   `json:"name,omitempty"`
	Hostname   string   `json:"hostname,omitempty"`
	OS         string   `json:"os,omitempty"`
	Arch       string   `json:"arch,omitempty"`
	LANIPs     []string `json:"lanIPs,omitempty"`
	DirectPort int      `json:"directPort,omitempty"`

	// Terminal and file sessions.
	SessionID string `json:"sessionId,omitempty"`
	Data      string `json:"data,omitempty"`
	Cols      int    `json:"cols,omitempty"`
	Rows      int    `json:"rows,omitempty"`
	Code      int    `json:"code,omitempty"`
	Error     string `json:"error,omitempty"`

	// File transfer.
	Size   int64  `json:"size,omitempty"`
	Path   string `json:"path,omitempty"`
	Target string `json:"target,omitempty"`
	Port   int    `json:"port,omitempty"`
	IP     string `json:"ip,omitempty"`
}

// Message types used by agents, the server and the web UI.
const (
	TypeHello           = "hello"
	TypePing            = "ping"
	TypePong            = "pong"
	TypeTermStart       = "terminal:start"
	TypeTermOutput      = "terminal:output"
	TypeTermInput       = "terminal:input"
	TypeTermResize      = "terminal:resize"
	TypeTermStop        = "terminal:stop"
	TypeTermExit        = "terminal:exit"
	TypeTermError       = "terminal:error"
	TypeFileUpload      = "file:upload:start"
	TypeFileChunk       = "file:chunk"
	TypeFileUploadEnd   = "file:upload:done"
	TypeFileDownload    = "file:download"
	TypeFileDone        = "file:done"
	TypeFileError       = "file:error"
	TypeForwardConnect  = "forward:connect"
	TypeForwardOpen     = "forward:open"
	TypeForwardData     = "forward:data"
	TypeForwardClose    = "forward:close"
	TypeForwardError    = "forward:error"
	TypeForwardOffer    = "forward:offer"
	TypeForwardAnswer   = "forward:answer"
	TypeForwardICE      = "forward:ice"
	TypeForwardDirectOK = "forward:direct-ok"
)

// EncodeData base64-encodes raw bytes for JSON transport.
func EncodeData(raw []byte) string {
	return base64.StdEncoding.EncodeToString(raw)
}

// DecodeData decodes base64 data from a message.
func DecodeData(data string) ([]byte, error) {
	return base64.StdEncoding.DecodeString(data)
}
