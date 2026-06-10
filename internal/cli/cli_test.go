package cli_test

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/guyweissman/agentstore/internal/cli"
	"github.com/guyweissman/agentstore/internal/store"
	"github.com/guyweissman/agentstore/internal/testutil"
)

// TestAddCommitLogCheckout is the M1 end-to-end workflow:
// stage → commit → log → checkout
func TestAddCommitLogCheckout(t *testing.T) {
	repo := testutil.NewRepo(t)

	// Write two files into the working tree.
	repo.WriteFile(t, "strategy/icp.md", "# ICP\n\nVersion 1.\n")
	repo.WriteFile(t, "notes/research.md", "# Research\n\nSome notes.\n")

	// Stage both files.
	if err := cli.RunAdd(repo.Store, repo.Index, repo.Root, []string{"."}); err != nil {
		t.Fatalf("RunAdd: %v", err)
	}

	entries, _ := repo.Index.Entries()
	if len(entries) != 2 {
		t.Fatalf("expected 2 staged entries, got %d", len(entries))
	}

	// Commit.
	id1, err := cli.RunCommit(repo.Store, repo.Index, "Initial commit")
	if err != nil {
		t.Fatalf("RunCommit: %v", err)
	}
	if len(id1) != 64 {
		t.Fatalf("bad commit id: %s", id1)
	}

	// Index cleared after commit.
	entries, _ = repo.Index.Entries()
	if len(entries) != 0 {
		t.Error("index should be empty after commit")
	}

	// Modify one file and make a second commit.
	repo.WriteFile(t, "strategy/icp.md", "# ICP\n\nVersion 2.\n")
	if err := cli.RunAdd(repo.Store, repo.Index, repo.Root, []string{filepath.Join(repo.Root, "strategy/icp.md")}); err != nil {
		t.Fatalf("RunAdd v2: %v", err)
	}
	id2, err := cli.RunCommit(repo.Store, repo.Index, "Update ICP")
	if err != nil {
		t.Fatalf("RunCommit v2: %v", err)
	}

	// Log: should have 2 commits, newest first.
	commits, err := repo.Store.LogCommits(store.LogFilter{})
	if err != nil {
		t.Fatalf("LogCommits: %v", err)
	}
	if len(commits) != 2 {
		t.Fatalf("expected 2 commits, got %d", len(commits))
	}
	if commits[0].ID != id2 {
		t.Error("first log entry should be newest commit")
	}
	if commits[1].ID != id1 {
		t.Error("second log entry should be initial commit")
	}

	// Path-filtered log.
	filtered, _ := repo.Store.LogCommits(store.LogFilter{Path: "/notes/research.md"})
	if len(filtered) != 1 {
		t.Errorf("path-filtered log: expected 1 commit, got %d", len(filtered))
	}

	// Show the initial commit.
	var buf strings.Builder
	if err := cli.RunShow(&buf, repo.Store, id1[:8]); err != nil {
		t.Fatalf("RunShow: %v", err)
	}
	if !strings.Contains(buf.String(), "Initial commit") {
		t.Errorf("show output missing message: %s", buf.String())
	}

	// Checkout: restore strategy/icp.md to its version from the first commit.
	if err := cli.RunCheckout(repo.Store, repo.Index, repo.Root, id1[:8], "/strategy/icp.md"); err != nil {
		t.Fatalf("RunCheckout: %v", err)
	}
	restored := repo.ReadFile(t, "strategy/icp.md")
	if !strings.Contains(restored, "Version 1") {
		t.Errorf("checkout did not restore v1: %s", restored)
	}
}

func TestStatusOutput(t *testing.T) {
	repo := testutil.NewRepo(t)
	repo.WriteFile(t, "a.md", "hello")

	// Before anything: untracked.
	var buf bytes.Buffer
	if err := cli.RunStatus(&buf, repo.Store, repo.Index, repo.Root); err != nil {
		t.Fatalf("RunStatus: %v", err)
	}
	if !strings.Contains(buf.String(), "Untracked") {
		t.Errorf("expected Untracked section: %s", buf.String())
	}

	// After staging: shows as staged.
	cli.RunAdd(repo.Store, repo.Index, repo.Root, []string{filepath.Join(repo.Root, "a.md")})
	buf.Reset()
	cli.RunStatus(&buf, repo.Store, repo.Index, repo.Root)
	if !strings.Contains(buf.String(), "to be committed") {
		t.Errorf("expected staged section: %s", buf.String())
	}

	// After committing: clean.
	cli.RunCommit(repo.Store, repo.Index, "add a")
	buf.Reset()
	cli.RunStatus(&buf, repo.Store, repo.Index, repo.Root)
	if !strings.Contains(buf.String(), "clean") {
		t.Errorf("expected clean status: %s", buf.String())
	}
}

func TestDiffOutput(t *testing.T) {
	repo := testutil.NewRepo(t)
	repo.WriteFile(t, "f.md", "line 1\nline 2\nline 3\n")
	cli.RunAdd(repo.Store, repo.Index, repo.Root, []string{filepath.Join(repo.Root, "f.md")})
	cli.RunCommit(repo.Store, repo.Index, "add f")

	// Modify the file in the working tree.
	repo.WriteFile(t, "f.md", "line 1\nline 2 modified\nline 3\n")

	// Unstaged diff should show the modification.
	var buf strings.Builder
	if err := cli.RunDiff(&buf, repo.Store, repo.Index, repo.Root, false, ""); err != nil {
		t.Fatalf("RunDiff: %v", err)
	}
	if !strings.Contains(buf.String(), "modified") {
		t.Errorf("diff output missing change: %s", buf.String())
	}

	// Stage the modification.
	cli.RunAdd(repo.Store, repo.Index, repo.Root, []string{filepath.Join(repo.Root, "f.md")})

	// Staged diff should show it; unstaged should be empty.
	buf.Reset()
	cli.RunDiff(&buf, repo.Store, repo.Index, repo.Root, true, "")
	if !strings.Contains(buf.String(), "modified") {
		t.Errorf("staged diff missing change: %s", buf.String())
	}

	buf.Reset()
	cli.RunDiff(&buf, repo.Store, repo.Index, repo.Root, false, "")
	if buf.Len() > 0 {
		t.Errorf("unstaged diff should be empty after staging: %s", buf.String())
	}
}
