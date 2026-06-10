package canonical

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"sort"

	"github.com/guyweissman/agentstore/internal/protocol"
)

const (
	opSet    byte = 0x01
	opDelete byte = 0x00
)

// CommitFile is one file entry in a commit.
type CommitFile struct {
	Path       string
	ObjectHash []byte // nil for a deletion
}

// CommitContent is the input to CommitID and CommitPreimageBytes.
type CommitContent struct {
	Message   string
	AuthorID  string
	CreatedAt int64    // Unix ms, set by the client at commit time
	Parents   [][]byte // 32-byte raw hashes in order (first parent, second for merge); NOT sorted
	Files     []CommitFile
}

// CommitID returns the lowercase hex SHA-256 of the canonical commit preimage.
func CommitID(c CommitContent) string {
	h := sha256.New()
	writeCommitPreimage(h, c)
	return hex.EncodeToString(h.Sum(nil))
}

// CommitPreimageBytes returns the raw bytes that CommitID hashes.
// Exported so tests can pin the exact byte sequence, not just the hash.
func CommitPreimageBytes(c CommitContent) []byte {
	var buf writeBuffer
	writeCommitPreimage(&buf, c)
	return []byte(buf)
}

func writeCommitPreimage(w io.Writer, c CommitContent) {
	w.Write([]byte(protocol.CommitV1Tag)) //nolint:errcheck

	writeStr(w, c.Message)
	writeStr(w, c.AuthorID)
	writeU64(w, uint64(c.CreatedAt))

	writeU32(w, uint32(len(c.Parents)))
	for _, p := range c.Parents {
		w.Write(p) //nolint:errcheck
	}

	// Files must be sorted by path bytes, ascending.
	sorted := make([]CommitFile, len(c.Files))
	copy(sorted, c.Files)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Path < sorted[j].Path })

	writeU32(w, uint32(len(sorted)))
	for _, f := range sorted {
		writeStr(w, f.Path)
		if f.ObjectHash == nil {
			w.Write([]byte{opDelete}) //nolint:errcheck
		} else {
			w.Write([]byte{opSet}) //nolint:errcheck
			w.Write(f.ObjectHash)  //nolint:errcheck
		}
	}
}
