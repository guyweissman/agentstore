package server_test

import (
	"os"
	"strings"
	"testing"

	"github.com/guyweissman/agentstore/internal/cli"
	"github.com/guyweissman/agentstore/internal/client"
)

// TestCheckoutRepo_RewindToSeq verifies that `checkout <seq> .` restores the
// entire working tree to an earlier state and stages the changes, and that a
// follow-up commit + push records the restore as a new commit (history preserved).
func TestCheckoutRepo_RewindToSeq(t *testing.T) {
	serverURL := testServer(t)
	repoURL := serverURL + "/rewind"
	id := registerUser(t, serverURL, "alice")

	repoA := t.TempDir()
	cli.RunInit(repoA, repoURL, id)
	sA, idxA := openRepo(t, repoA)

	// seq 1: two files.
	writeFile(t, repoA+"/a.md", "a v1\n")
	writeFile(t, repoA+"/b.md", "b v1\n")
	cli.RunAdd(sA, idxA, repoA, []string{repoA + "/a.md", repoA + "/b.md"})
	cli.RunCommit(sA, idxA, "seq1: add a and b")
	cli.RunPush(sA, repoURL, id)

	// seq 2: modify a, add c.
	writeFile(t, repoA+"/a.md", "a v2\n")
	writeFile(t, repoA+"/c.md", "c v2\n")
	cli.RunAdd(sA, idxA, repoA, []string{repoA + "/a.md", repoA + "/c.md"})
	cli.RunCommit(sA, idxA, "seq2: modify a, add c")
	cli.RunPush(sA, repoURL, id)

	// Sanity: working tree is at seq 2.
	if got := readFile(t, repoA+"/a.md"); !strings.Contains(got, "a v2") {
		t.Fatalf("precondition: a.md should be v2, got %q", got)
	}
	if _, err := os.Stat(repoA + "/c.md"); err != nil {
		t.Fatalf("precondition: c.md should exist")
	}

	// Rewind the whole repo to seq 1.
	if err := cli.RunCheckoutRepo(sA, idxA, repoA, mustCommitIDBySeq(t, sA, 1)); err != nil {
		t.Fatalf("checkout repo: %v", err)
	}

	// a.md back to v1, b.md unchanged, c.md removed.
	if got := readFile(t, repoA+"/a.md"); !strings.Contains(got, "a v1") {
		t.Errorf("a.md should be restored to v1, got %q", got)
	}
	if got := readFile(t, repoA+"/b.md"); !strings.Contains(got, "b v1") {
		t.Errorf("b.md should remain v1, got %q", got)
	}
	if _, err := os.Stat(repoA + "/c.md"); !os.IsNotExist(err) {
		t.Errorf("c.md (added after seq 1) should be removed by rewind")
	}

	// Changes are staged: a.md modified + c.md deleted = 2 staged entries.
	staged, _ := idxA.Entries()
	if len(staged) != 2 {
		t.Errorf("expected 2 staged changes after rewind, got %d", len(staged))
	}

	// Commit and push the rewind — history is preserved (becomes seq 3).
	if _, err := cli.RunCommit(sA, idxA, "Rewind to seq 1"); err != nil {
		t.Fatalf("commit rewind: %v", err)
	}
	res, err := cli.RunPush(sA, repoURL, id)
	if err != nil {
		t.Fatalf("push rewind: %v", err)
	}
	if res.Seq != 3 {
		t.Errorf("rewind commit should be seq 3, got %d", res.Seq)
	}
}

// TestCheckoutRepo_StagedNewFileBecomesUntracked verifies Option C: a file
// staged but never committed, which does not exist at the target point, is
// unstaged (left on disk as untracked) rather than swept into the rewind commit.
func TestCheckoutRepo_StagedNewFileBecomesUntracked(t *testing.T) {
	serverURL := testServer(t)
	repoURL := serverURL + "/staged-new"
	id := registerUser(t, serverURL, "alice")

	repoA := t.TempDir()
	cli.RunInit(repoA, repoURL, id)
	sA, idxA := openRepo(t, repoA)

	// seq 1: one committed file. Modify it locally too so the rewind has work to do.
	writeFile(t, repoA+"/a.md", "a v1\n")
	cli.RunAdd(sA, idxA, repoA, []string{repoA + "/a.md"})
	cli.RunCommit(sA, idxA, "seq1")
	cli.RunPush(sA, repoURL, id)

	// seq 2: modify a.md and push, so seq 1 differs from current head.
	writeFile(t, repoA+"/a.md", "a v2\n")
	cli.RunAdd(sA, idxA, repoA, []string{repoA + "/a.md"})
	cli.RunCommit(sA, idxA, "seq2")
	cli.RunPush(sA, repoURL, id)

	// Stage a brand-new file that was never committed.
	writeFile(t, repoA+"/new.md", "new work\n")
	cli.RunAdd(sA, idxA, repoA, []string{repoA + "/new.md"})

	// Rewind the whole repo to seq 1.
	if err := cli.RunCheckoutRepo(sA, idxA, repoA, mustCommitIDBySeq(t, sA, 1)); err != nil {
		t.Fatalf("checkout repo: %v", err)
	}

	// new.md should still exist on disk (not destroyed)...
	if got := readFile(t, repoA+"/new.md"); !strings.Contains(got, "new work") {
		t.Errorf("staged-new file should survive on disk, got %q", got)
	}
	// ...but must NOT be staged.
	entries, err := idxA.Entries()
	if err != nil {
		t.Fatalf("entries: %v", err)
	}
	for _, e := range entries {
		if e.Path == "/new.md" {
			t.Errorf("new.md should have been unstaged, still staged as %q", e.ChangeType)
		}
	}

	// Commit + push the rewind: server head must equal seq 1 exactly (no new.md).
	if _, err := cli.RunCommit(sA, idxA, "Rewind to seq 1"); err != nil {
		t.Fatalf("commit rewind: %v", err)
	}
	cli.RunPush(sA, repoURL, id)

	cl, _ := client.New(repoURL, id)
	heads, _ := cl.GetHeads()
	for _, h := range heads {
		if h.Path == "/new.md" && h.ChangeType != "deleted" {
			t.Error("new.md must not have been pushed to the server")
		}
		if h.Path == "/a.md" && h.ObjectHash != "" {
			// Verify a.md content is back to v1 by downloading it.
			data, _ := cl.DownloadObject(h.ObjectHash)
			if !strings.Contains(string(data), "a v1") {
				t.Errorf("a.md on server should be v1 after rewind, got %q", data)
			}
		}
	}
}

func mustCommitIDBySeq(t *testing.T, s interface {
	CommitIDBySeq(int64) (string, error)
}, seq int64) string {
	t.Helper()
	id, err := s.CommitIDBySeq(seq)
	if err != nil {
		t.Fatalf("CommitIDBySeq(%d): %v", seq, err)
	}
	return id
}
