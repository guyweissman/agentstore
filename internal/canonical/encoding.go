package canonical

import (
	"encoding/binary"
	"io"
)

// writeBuffer is an append-only byte slice that satisfies io.Writer.
// Used to accumulate preimage bytes without error handling noise.
type writeBuffer []byte

func (b *writeBuffer) Write(p []byte) (int, error) {
	*b = append(*b, p...)
	return len(p), nil
}

func writeStr(w io.Writer, s string) {
	b := []byte(s)
	writeU32(w, uint32(len(b)))
	w.Write(b) //nolint:errcheck
}

func writeU32(w io.Writer, v uint32) {
	var buf [4]byte
	binary.BigEndian.PutUint32(buf[:], v)
	w.Write(buf[:]) //nolint:errcheck
}

func writeU64(w io.Writer, v uint64) {
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], v)
	w.Write(buf[:]) //nolint:errcheck
}
