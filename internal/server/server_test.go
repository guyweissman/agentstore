package server_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"

	"github.com/guyweissman/agentstore/internal/cli"
	"github.com/guyweissman/agentstore/internal/client"
	"github.com/guyweissman/agentstore/internal/index"
	"github.com/guyweissman/agentstore/internal/server"
	"github.com/guyweissman/agentstore/internal/store"
)

// registerUser generates an ed25519 keypair, registers it with the server, and
// returns a client.Identity for signing. Mirrors `agentstore register`.
func registerUser(t *testing.T, serverURL, username string) *client.Identity {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatalf("ssh public key: %v", err)
	}
	pubLine := string(ssh.MarshalAuthorizedKey(sshPub))

	cl, err := client.New(serverURL+"/_", nil)
	if err != nil {
		t.Fatalf("client.New: %v", err)
	}
	principalID, err := cl.Register(username, pubLine)
	if err != nil {
		t.Fatalf("register %s: %v", username, err)
	}
	return &client.Identity{PrincipalID: principalID, PrivateKey: priv}
}

// publicKeyLine returns the OpenSSH authorized-key line for an identity's key.
func publicKeyLine(t *testing.T, id *client.Identity) string {
	t.Helper()
	pub := id.PrivateKey.Public().(ed25519.PublicKey)
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatalf("ssh public key: %v", err)
	}
	return string(ssh.MarshalAuthorizedKey(sshPub))
}

// testServer starts an in-process server and returns its base URL.
func testServer(t *testing.T) string {
	t.Helper()
	srv, err := server.New(t.TempDir())
	if err != nil {
		t.Fatalf("server.New: %v", err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts.URL
}

func openRepo(t *testing.T, root string) (*store.Store, *index.Index) {
	t.Helper()
	s, err := store.Open(root)
	if err != nil {
		t.Fatalf("store.Open %s: %v", root, err)
	}
	idx, err := index.Open(s.Dir())
	if err != nil {
		s.Close()
		t.Fatalf("index.Open %s: %v", root, err)
	}
	t.Cleanup(func() { idx.Close(); s.Close() })
	return s, idx
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("writeFile %s: %v", path, err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("readFile %s: %v", path, err)
	}
	return string(data)
}

// TestOCC_NonOverlapping is the write-starvation proof:
// two clients pushing *different* files both succeed with no retry.
func TestOCC_NonOverlapping(t *testing.T) {
	serverURL := testServer(t)
	repoURL := serverURL + "/brand"
	alice := registerUser(t, serverURL, "alice")

	// Init the repo (alice becomes admin + owner of /*). Two clones, same identity.
	repoA := t.TempDir()
	if err := cli.RunInit(repoA, repoURL, alice); err != nil {
		t.Fatalf("init: %v", err)
	}
	sA, idxA := openRepo(t, repoA)

	// Clone A adds strategy/icp.md.
	writeFile(t, filepath.Join(repoA, "strategy", "icp.md"), "# ICP\nContent.\n")
	cli.RunAdd(sA, idxA, repoA, []string{filepath.Join(repoA, "strategy", "icp.md")})
	if _, err := cli.RunCommit(sA, idxA, "Add ICP"); err != nil {
		t.Fatalf("commit A: %v", err)
	}
	resultA, err := cli.RunPush(sA, repoURL, alice)
	if err != nil {
		t.Fatalf("push A: %v", err)
	}
	if len(resultA.Conflicts) > 0 {
		t.Fatalf("push A unexpectedly rejected: %v", resultA.Conflicts)
	}

	// Clone B adds notes/research.md — a completely different file.
	repoB := t.TempDir()
	if err := cli.RunClone(repoURL, repoB, alice); err != nil {
		t.Fatalf("clone B: %v", err)
	}
	sB, idxB := openRepo(t, repoB)

	writeFile(t, filepath.Join(repoB, "notes", "research.md"), "# Research\nNotes.\n")
	cli.RunAdd(sB, idxB, repoB, []string{filepath.Join(repoB, "notes", "research.md")})
	if _, err := cli.RunCommit(sB, idxB, "Add research"); err != nil {
		t.Fatalf("commit B: %v", err)
	}
	resultB, err := cli.RunPush(sB, repoURL, alice)
	if err != nil {
		t.Fatalf("push B: %v", err)
	}
	// Non-overlapping files — B must succeed with NO retry.
	if len(resultB.Conflicts) > 0 {
		t.Fatalf("non-overlapping push B rejected (write-starvation bug): %v", resultB.Conflicts)
	}

	// Verify the server has both commits.
	cl, _ := client.New(repoURL, alice)
	commits, _ := cl.GetCommits(0)
	if len(commits) != 2 {
		t.Errorf("server should have 2 commits, got %d", len(commits))
	}
}

// TestOCC_Conflict is the full reject → pull → merge → re-push cycle.
func TestOCC_Conflict(t *testing.T) {
	serverURL := testServer(t)
	repoURL := serverURL + "/docs"

	alice := registerUser(t, serverURL, "alice")

	// Create initial state: two clones of alice's repo start from a common file.
	repoA := t.TempDir()
	if err := cli.RunInit(repoA, repoURL, alice); err != nil {
		t.Fatalf("init: %v", err)
	}
	sA, idxA := openRepo(t, repoA)

	writeFile(t, filepath.Join(repoA, "readme.md"), "line 1\nline 2\nline 3\n")
	cli.RunAdd(sA, idxA, repoA, []string{filepath.Join(repoA, "readme.md")})
	if _, err := cli.RunCommit(sA, idxA, "Add readme"); err != nil {
		t.Fatalf("commit base: %v", err)
	}
	cli.RunPush(sA, repoURL, alice)

	// Second clone.
	repoB := t.TempDir()
	if err := cli.RunClone(repoURL, repoB, alice); err != nil {
		t.Fatalf("clone B: %v", err)
	}
	sB, idxB := openRepo(t, repoB)

	// Clone A modifies line 1 and pushes first.
	writeFile(t, filepath.Join(repoA, "readme.md"), "line 1 ALICE\nline 2\nline 3\n")
	cli.RunAdd(sA, idxA, repoA, []string{filepath.Join(repoA, "readme.md")})
	if _, err := cli.RunCommit(sA, idxA, "Edit line 1"); err != nil {
		t.Fatalf("commit A2: %v", err)
	}
	resultA, err := cli.RunPush(sA, repoURL, alice)
	if err != nil || len(resultA.Conflicts) > 0 {
		t.Fatalf("push A2: err=%v conflicts=%v", err, resultA.Conflicts)
	}

	// Clone B modifies line 3 (non-overlapping) and tries to push — rejected.
	writeFile(t, filepath.Join(repoB, "readme.md"), "line 1\nline 2\nline 3 BOB\n")
	cli.RunAdd(sB, idxB, repoB, []string{filepath.Join(repoB, "readme.md")})
	if _, err := cli.RunCommit(sB, idxB, "Edit line 3"); err != nil {
		t.Fatalf("commit B: %v", err)
	}

	resultB, err := cli.RunPush(sB, repoURL, alice)
	if err != nil {
		t.Fatalf("push B: %v", err)
	}
	if len(resultB.Conflicts) == 0 {
		t.Fatal("Bob's push should be rejected (OCC conflict)")
	}
	if resultB.Conflicts[0].Path != "/readme.md" {
		t.Errorf("expected conflict on /readme.md, got %q", resultB.Conflicts[0].Path)
	}
	t.Logf("Push correctly rejected. Conflict: %s (server head %s)",
		resultB.Conflicts[0].Path, resultB.Conflicts[0].CurrentHeadCommitID[:8])

	// Clone B pulls: 3-way merge — line 1 and line 3, no overlap → auto-merge.
	if err := cli.RunPull(sB, idxB, repoB, repoURL, alice); err != nil {
		t.Fatalf("pull B: %v", err)
	}

	merged := readFile(t, filepath.Join(repoB, "readme.md"))
	if !strings.Contains(merged, "line 1 ALICE") {
		t.Errorf("merged file missing Alice's change:\n%s", merged)
	}
	if !strings.Contains(merged, "line 3 BOB") {
		t.Errorf("merged file missing Bob's change:\n%s", merged)
	}
	if strings.Contains(merged, "<<<<<<<") {
		t.Errorf("non-overlapping merge should not produce conflict markers:\n%s", merged)
	}
	t.Logf("Auto-merge succeeded:\n%s", merged)

	// Clone B commits the merge and pushes — must succeed.
	if _, err := cli.RunCommit(sB, idxB, "Merge edits"); err != nil {
		t.Fatalf("commit merge: %v", err)
	}
	resultMerge, err := cli.RunPush(sB, repoURL, alice)
	if err != nil {
		t.Fatalf("push merge: %v", err)
	}
	if len(resultMerge.Conflicts) > 0 {
		t.Fatalf("merge push rejected: %v", resultMerge.Conflicts)
	}
	t.Logf("Merge push accepted: seq=%d id=%s", resultMerge.Seq, resultMerge.ID[:8])

	// The ID the server accepted must match what the client stored locally.
	localMerge, err := sB.GetCommit(resultMerge.ID)
	if err != nil || localMerge == nil {
		t.Errorf("local store should contain the pushed commit %s", resultMerge.ID[:8])
	}

	// No commits should remain unpushed (the pre-merge rejected commit is absorbed).
	unpushed, _ := sB.UnpushedCommit()
	if unpushed != nil {
		t.Errorf("no commits should remain unpushed after successful merge push; got %s", unpushed.ID[:8])
	}

	// Server should have 3 commits: add readme, the line-1 edit, merge.
	cl, _ := client.New(repoURL, alice)
	commits, _ := cl.GetCommits(0)
	if len(commits) != 3 {
		t.Errorf("expected 3 commits on server, got %d", len(commits))
	}
}

// TestPullFastForwardModifiedFile is a regression test: a remote MODIFICATION
// to an existing file must fast-forward cleanly into a clone that never touched
// that file. Previously the pre-apply overwrite guard compared the working tree
// against the already-stored incoming version (FileHead after WriteRemoteCommit)
// instead of the local base, and false-tripped with "would overwrite unstaged
// changes" on every edit-elsewhere-then-sync.
func TestPullFastForwardModifiedFile(t *testing.T) {
	serverURL := testServer(t)
	repoURL := serverURL + "/p"

	alice := registerUser(t, serverURL, "alice")

	// Original checkout: create a.md, commit, push.
	repoA := t.TempDir()
	if err := cli.RunInit(repoA, repoURL, alice); err != nil {
		t.Fatalf("init: %v", err)
	}
	sA, idxA := openRepo(t, repoA)
	writeFile(t, filepath.Join(repoA, "a.md"), "hello\n")
	cli.RunAdd(sA, idxA, repoA, []string{filepath.Join(repoA, "a.md")})
	if _, err := cli.RunCommit(sA, idxA, "Add a.md"); err != nil {
		t.Fatalf("commit base: %v", err)
	}
	if _, err := cli.RunPush(sA, repoURL, alice); err != nil {
		t.Fatalf("push base: %v", err)
	}

	// Fresh clone — never touches a.md.
	repoB := t.TempDir()
	if err := cli.RunClone(repoURL, repoB, alice); err != nil {
		t.Fatalf("clone B: %v", err)
	}
	sB, idxB := openRepo(t, repoB)

	// Original checkout modifies a.md and pushes.
	writeFile(t, filepath.Join(repoA, "a.md"), "hello\nworld\n")
	cli.RunAdd(sA, idxA, repoA, []string{filepath.Join(repoA, "a.md")})
	if _, err := cli.RunCommit(sA, idxA, "Append world"); err != nil {
		t.Fatalf("commit edit: %v", err)
	}
	if _, err := cli.RunPush(sA, repoURL, alice); err != nil {
		t.Fatalf("push edit: %v", err)
	}

	// The clone pulls: must fast-forward cleanly, not refuse.
	if err := cli.RunPull(sB, idxB, repoB, repoURL, alice); err != nil {
		t.Fatalf("pull should fast-forward cleanly, got: %v", err)
	}
	if got := readFile(t, filepath.Join(repoB, "a.md")); got != "hello\nworld\n" {
		t.Errorf("a.md not fast-forwarded; got %q", got)
	}
}
