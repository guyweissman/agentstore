// Package testutil provides helpers shared across test packages.
package testutil

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/guyweissman/agentstore/internal/index"
	"github.com/guyweissman/agentstore/internal/store"
)

// Repo is a fully initialized local repo in a temp directory, for use in tests.
type Repo struct {
	Root  string
	Store *store.Store
	Index *index.Index
}

// NewRepo creates a temp directory, initializes the store and index, and registers cleanup.
// Tests that use this must NOT call t.Parallel() if they also change the working directory.
func NewRepo(t *testing.T) *Repo {
	t.Helper()
	root := t.TempDir()
	s, err := store.Init(root)
	if err != nil {
		t.Fatalf("store.Init: %v", err)
	}
	idx, err := index.Init(s.Dir())
	if err != nil {
		s.Close()
		t.Fatalf("index.Init: %v", err)
	}
	t.Cleanup(func() {
		idx.Close()
		s.Close()
	})
	return &Repo{Root: root, Store: s, Index: idx}
}

// WriteFile writes content to path (relative to repo root), creating parent dirs.
func (r *Repo) WriteFile(t *testing.T, relPath, content string) {
	t.Helper()
	abs := filepath.Join(r.Root, filepath.FromSlash(relPath))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatalf("WriteFile mkdir %s: %v", relPath, err)
	}
	if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile %s: %v", relPath, err)
	}
}

// ReadFile reads a file from the repo root. Fails the test if it doesn't exist.
func (r *Repo) ReadFile(t *testing.T, relPath string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(r.Root, filepath.FromSlash(relPath)))
	if err != nil {
		t.Fatalf("ReadFile %s: %v", relPath, err)
	}
	return string(data)
}
