package store

import (
	"path"
	"strings"
)

// ValidPath reports whether p is a well-formed repo path: absolute (leading "/"),
// already in canonical form (no ".", "..", "//", or trailing slash), and not the
// bare root. This is the guard that keeps a server-supplied path from escaping a
// client's working tree when materialized (path traversal), and keeps the store
// from ever recording such a path.
func ValidPath(p string) bool {
	if len(p) < 2 || p[0] != '/' {
		return false
	}
	// Reject "*" in a concrete path: it is reserved for the trailing grant
	// wildcard (see ValidPathPattern). Allowing it as a literal would let an
	// interior wildcard like "/a/*/b" pass as a no-op exact-path grant — a
	// footgun for a user who meant a wildcard.
	if strings.ContainsRune(p, '*') {
		return false
	}
	// path.Clean collapses ".", "..", and "//"; if the input differs, it was not
	// canonical (e.g. "/../x" cleans to "/x", "/a/../b" to "/b").
	return path.Clean(p) == p
}

// ValidPathPattern reports whether p is a valid grant path pattern: either a
// valid exact path (see ValidPath) or a valid prefix wildcard ending with "/*"
// (e.g. "/strategy/*" or the root wildcard "/*"). Interior wildcards like
// "/customers/*/sanitized" are not supported in v0.1.
func ValidPathPattern(p string) bool {
	if p == "/*" {
		return true
	}
	if strings.HasSuffix(p, "/*") {
		base := strings.TrimSuffix(p, "/*")
		return ValidPath(base)
	}
	return ValidPath(p)
}
