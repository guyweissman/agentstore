package canonical

import (
	"io"

	"github.com/guyweissman/agentstore/internal/protocol"
)

// RequestContent is the input to RequestPreimageBytes.
// The preimage bytes are passed directly to ed25519.Sign (which hashes internally).
type RequestContent struct {
	PrincipalID   string
	Method        string // uppercase ASCII: "GET", "POST", …
	RequestTarget string // endpoint path + query string, exactly as sent
	Timestamp     int64  // Unix ms
	BodySHA256    []byte // 32 raw bytes; SHA-256 of empty body if none
}

// RequestPreimageBytes returns the canonical bytes to be signed (or verified).
func RequestPreimageBytes(r RequestContent) []byte {
	var buf writeBuffer
	writeRequestPreimage(&buf, r)
	return []byte(buf)
}

func writeRequestPreimage(w io.Writer, r RequestContent) {
	w.Write([]byte(protocol.RequestV1Tag)) //nolint:errcheck
	writeStr(w, r.PrincipalID)
	writeStr(w, r.Method)
	writeStr(w, r.RequestTarget)
	writeU64(w, uint64(r.Timestamp))
	w.Write(r.BodySHA256) //nolint:errcheck
}
