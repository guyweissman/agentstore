package server_test

import (
	"os"
	"strings"
	"testing"

	"github.com/guyweissman/agentstore/internal/cli"
	"github.com/guyweissman/agentstore/internal/store"
)

func TestReset_SingleCommit(t *testing.T) {
	serverURL := testServer(t)
	repoURL := serverURL + "/reset-single"
	id := registerUser(t, serverURL, "alice")

	repoA := t.TempDir()
	cli.RunInit(repoA, repoURL, id)
	sA, idxA := openRepo(t, repoA)

	// Push a base state.
	writeFile(t, repoA+"/f.md", "base content\n")
	cli.RunAdd(sA, idxA, repoA, []string{repoA + "/f.md"})
	cli.RunCommit(sA, idxA, "Base")
	cli.RunPush(sA, repoURL, id)

	// Make one unpushed commit.
	writeFile(t, repoA+"/f.md", "modified content\n")
	cli.RunAdd(sA, idxA, repoA, []string{repoA + "/f.md"})
	cli.RunCommit(sA, idxA, "Modify")

	if err := cli.RunReset(sA, idxA, repoA); err != nil {
		t.Fatalf("reset: %v", err)
	}

	// Working tree restored.
	if got := readFile(t, repoA+"/f.md"); !strings.Contains(got, "base content") {
		t.Errorf("reset did not restore working tree: %s", got)
	}
	// No unpushed commits remain.
	if u, _ := sA.UnpushedCommit(); u != nil {
		t.Errorf("unpushed commit remains after reset: %s", u.ID[:8])
	}
}

func TestReset_MultipleCommits(t *testing.T) {
	serverURL := testServer(t)
	repoURL := serverURL + "/reset-multi"
	id := registerUser(t, serverURL, "alice")

	repoA := t.TempDir()
	cli.RunInit(repoA, repoURL, id)
	sA, idxA := openRepo(t, repoA)

	writeFile(t, repoA+"/f.md", "v1\n")
	cli.RunAdd(sA, idxA, repoA, []string{repoA + "/f.md"})
	cli.RunCommit(sA, idxA, "v1")
	cli.RunPush(sA, repoURL, id)

	// Two unpushed commits.
	writeFile(t, repoA+"/f.md", "v2\n")
	cli.RunAdd(sA, idxA, repoA, []string{repoA + "/f.md"})
	cli.RunCommit(sA, idxA, "v2")

	writeFile(t, repoA+"/f.md", "v3\n")
	cli.RunAdd(sA, idxA, repoA, []string{repoA + "/f.md"})
	cli.RunCommit(sA, idxA, "v3")

	chain, _ := sA.UnpushedChain()
	if len(chain) != 2 {
		t.Fatalf("expected 2 unpushed commits, got %d", len(chain))
	}

	if err := cli.RunReset(sA, idxA, repoA); err != nil {
		t.Fatalf("reset: %v", err)
	}

	if got := readFile(t, repoA+"/f.md"); !strings.Contains(got, "v1") {
		t.Errorf("expected v1 after reset, got: %s", got)
	}
	if rem, _ := sA.UnpushedChain(); len(rem) != 0 {
		t.Errorf("expected 0 unpushed commits after reset, got %d", len(rem))
	}
}

// TestReset_NewFile verifies that a file only added in unpushed commits is
// deleted from the working tree after reset.
func TestReset_NewFile(t *testing.T) {
	serverURL := testServer(t)
	repoURL := serverURL + "/reset-newfile"
	id := registerUser(t, serverURL, "alice")

	repoA := t.TempDir()
	cli.RunInit(repoA, repoURL, id)
	sA, idxA := openRepo(t, repoA)

	writeFile(t, repoA+"/new.md", "never pushed\n")
	cli.RunAdd(sA, idxA, repoA, []string{repoA + "/new.md"})
	cli.RunCommit(sA, idxA, "Add new file")

	if err := cli.RunReset(sA, idxA, repoA); err != nil {
		t.Fatalf("reset: %v", err)
	}

	if _, err := os.Stat(repoA + "/new.md"); !os.IsNotExist(err) {
		t.Error("file only added in unpushed commit should be removed after reset")
	}
	if head, _ := sA.FileHead("/new.md"); head != nil {
		t.Error("FileHead should return nil for a file that was never pushed")
	}
}

// TestReset_LogHidesAbsorbed verifies that absorbed commits are invisible in log.
func TestReset_LogHidesAbsorbed(t *testing.T) {
	serverURL := testServer(t)
	repoURL := serverURL + "/reset-log"
	id := registerUser(t, serverURL, "alice")

	repoA := t.TempDir()
	cli.RunInit(repoA, repoURL, id)
	sA, idxA := openRepo(t, repoA)

	writeFile(t, repoA+"/f.md", "v1\n")
	cli.RunAdd(sA, idxA, repoA, []string{repoA + "/f.md"})
	cli.RunCommit(sA, idxA, "Pushed commit")
	cli.RunPush(sA, repoURL, id)

	writeFile(t, repoA+"/f.md", "v2\n")
	cli.RunAdd(sA, idxA, repoA, []string{repoA + "/f.md"})
	cli.RunCommit(sA, idxA, "Unpushed — must vanish from log after reset")

	cli.RunReset(sA, idxA, repoA)

	commits, _ := sA.LogCommits(store.LogFilter{})
	for _, c := range commits {
		if strings.Contains(c.Message, "must vanish") {
			t.Error("absorbed commit must not appear in log")
		}
	}
	if len(commits) != 1 {
		t.Errorf("expected 1 commit in log after reset, got %d", len(commits))
	}
}
