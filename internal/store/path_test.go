package store_test

import (
	"testing"

	"github.com/guyweissman/agentstore/internal/store"
)

func TestValidPath(t *testing.T) {
	valid := []string{"/a.md", "/strategy/icp.md", "/a/b/c.txt"}
	for _, p := range valid {
		if !store.ValidPath(p) {
			t.Errorf("ValidPath(%q) = false, want true", p)
		}
	}
	invalid := []string{
		"", "/", "relative.md", "a/b.md",
		"/../etc/passwd", "/a/../../b", "//x", "/a//b", "/a/", "/a/.", "/a/..",
		"/a/*/b", "/a/*", "/*", // "*" is never valid in a concrete path
	}
	for _, p := range invalid {
		if store.ValidPath(p) {
			t.Errorf("ValidPath(%q) = true, want false", p)
		}
	}
}

func TestValidPathPattern(t *testing.T) {
	valid := []string{"/strategy/icp.md", "/strategy/*", "/*", "/a/b/*"}
	for _, p := range valid {
		if !store.ValidPathPattern(p) {
			t.Errorf("ValidPathPattern(%q) = false, want true", p)
		}
	}
	invalid := []string{
		"", "/", "relative", "/../x", "/a//b",
		"/a/*/b", // interior wildcard — not supported
		"/a/**",  // double wildcard
		"/a*/b",  // partial-segment wildcard
	}
	for _, p := range invalid {
		if store.ValidPathPattern(p) {
			t.Errorf("ValidPathPattern(%q) = true, want false", p)
		}
	}
}
