package server_test

import (
	"fmt"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/guyweissman/agentstore/internal/cli"
	"github.com/guyweissman/agentstore/internal/client"
	"github.com/guyweissman/agentstore/internal/config"
	"github.com/guyweissman/agentstore/internal/server"
)

// TestMirrorMoveRepo is the M5 portability scenario: an admin relocates a repo to
// a fresh, empty server with history, grants, roles, and roster intact — and a
// member re-clones from the new home and reads their authorized files.
func TestMirrorMoveRepo(t *testing.T) {
	// Two independent servers (old home + new home).
	oldURL := testServer(t)
	newSrv, err := server.New(t.TempDir())
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	ts := httptest.NewServer(newSrv.Handler())
	t.Cleanup(ts.Close)
	newURL := ts.URL

	oldRepoURL := oldURL + "/strategy"
	alice := registerUser(t, oldURL, "alice")
	bob := registerUser(t, oldURL, "bob")

	// Alice creates the repo on the old server, commits, adds bob with read.
	repoA := t.TempDir()
	if err := cli.RunInit(repoA, oldRepoURL, alice); err != nil {
		t.Fatalf("init: %v", err)
	}
	sA, idxA := openRepo(t, repoA)
	commitAndPush(t, sA, idxA, repoA, oldRepoURL, alice, "strategy/plan.md", "the plan\n", "c1")
	commitAndPush(t, sA, idxA, repoA, oldRepoURL, alice, "finance/secret.md", "secret\n", "c2")

	aliceOld, _ := client.New(oldRepoURL, alice)
	if err := aliceOld.AddMember("bob"); err != nil {
		t.Fatalf("add bob: %v", err)
	}
	if err := aliceOld.Grant("bob", "read", "/strategy/*"); err != nil {
		t.Fatalf("grant bob: %v", err)
	}

	// Mirror to the new, empty home — no pre-registration on the target: the
	// mirror self-authenticates against the admin in the payload roster.
	if _, err := cli.RunMirror(oldRepoURL, newURL+"/strategy", alice); err != nil {
		t.Fatalf("mirror: %v", err)
	}

	// History intact: the new server has both commits with the same ids and seqs.
	aliceNew, _ := client.New(newURL+"/strategy", alice)
	oldCommits, _ := aliceOld.GetCommits(0)
	newCommits, _ := aliceNew.GetCommits(0)
	if len(newCommits) != len(oldCommits) {
		t.Fatalf("commit count differs: old %d, new %d", len(oldCommits), len(newCommits))
	}
	for i := range oldCommits {
		if oldCommits[i].ID != newCommits[i].ID {
			t.Errorf("commit %d id differs: %s vs %s", i, oldCommits[i].ID, newCommits[i].ID)
		}
		if oldCommits[i].Seq != newCommits[i].Seq {
			t.Errorf("commit %d seq differs: %d vs %d", i, oldCommits[i].Seq, newCommits[i].Seq)
		}
	}

	// Identity + grants intact: bob re-clones from the new home and gets only
	// /strategy (his grant travelled), not /finance.
	repoB := t.TempDir()
	bobNewID := &client.Identity{PrincipalID: bob.PrincipalID, PrivateKey: bob.PrivateKey}
	if err := cli.RunClone(newURL+"/strategy", repoB, bobNewID); err != nil {
		t.Fatalf("bob re-clone from new home: %v", err)
	}
	if !fileExists(filepath.Join(repoB, "strategy", "plan.md")) {
		t.Error("bob should see /strategy/plan.md on the new home")
	}
	if fileExists(filepath.Join(repoB, "finance", "secret.md")) {
		t.Error("bob must NOT see /finance/secret.md on the new home")
	}

	// Mirroring onto a non-empty target must be refused.
	if _, err := cli.RunMirror(oldRepoURL, newURL+"/strategy", alice); err == nil {
		t.Error("mirror onto a non-empty target should be refused")
	}
}

// TestMirrorResponseAndDirectoryLookup covers the post-move re-clone path that
// `bind` relies on: the mirror needs no pre-registration on the target, it
// reports the signer's preserved identity, the open directory then resolves that
// username to the same principal_id + public key, and a client asserting that
// principal_id authenticates as the repo admin.
func TestMirrorResponseAndDirectoryLookup(t *testing.T) {
	oldURL := testServer(t)
	newSrv, _ := server.New(t.TempDir())
	ts := httptest.NewServer(newSrv.Handler())
	t.Cleanup(ts.Close)
	newURL := ts.URL

	oldRepoURL := oldURL + "/strategy"
	alice := registerUser(t, oldURL, "alice")
	repoA := t.TempDir()
	if err := cli.RunInit(repoA, oldRepoURL, alice); err != nil {
		t.Fatalf("init: %v", err)
	}
	sA, idxA := openRepo(t, repoA)
	commitAndPush(t, sA, idxA, repoA, oldRepoURL, alice, "strategy/plan.md", "the plan\n", "c1")

	// Mirror with NO prior registration on the target.
	resp, err := cli.RunMirror(oldRepoURL, newURL+"/strategy", alice)
	if err != nil {
		t.Fatalf("mirror without pre-registration should succeed: %v", err)
	}
	if resp.PrincipalID != alice.PrincipalID {
		t.Errorf("mirror response principal_id = %s, want preserved %s", resp.PrincipalID, alice.PrincipalID)
	}
	if resp.Username != "alice" {
		t.Errorf("mirror response username = %q, want \"alice\"", resp.Username)
	}

	// The open directory now resolves "alice" to the preserved id + her key.
	dirCl, _ := client.New(newURL+"/_", nil)
	entry, err := dirCl.LookupDirectory("alice")
	if err != nil {
		t.Fatalf("directory lookup on new home: %v", err)
	}
	if entry.PrincipalID != alice.PrincipalID {
		t.Errorf("directory principal_id = %s, want %s", entry.PrincipalID, alice.PrincipalID)
	}
	if entry.PublicKey != publicKeyLine(t, alice) {
		t.Error("directory public key does not match alice's key")
	}

	// Asserting the preserved principal_id authenticates as the admin on the new home.
	aliceNew, _ := client.New(newURL+"/strategy", alice)
	who, err := aliceNew.WhoAmI()
	if err != nil {
		t.Fatalf("whoami on new home: %v", err)
	}
	if who.Username != "alice" {
		t.Errorf("whoami username = %q, want \"alice\"", who.Username)
	}

	// An unknown username is a clean 404.
	if _, err := dirCl.LookupDirectory("nobody"); err == nil {
		t.Error("directory lookup of an unknown username should fail")
	}
}

// TestMirrorPaginatesHistory verifies the full history transfers even when it
// spans multiple commit pages (the GetCommits page limit).
func TestMirrorPaginatesHistory(t *testing.T) {
	client.SetCommitPageLimitForTest(2)
	defer client.SetCommitPageLimitForTest(1000)

	oldURL := testServer(t)
	newSrv, _ := server.New(t.TempDir())
	ts := httptest.NewServer(newSrv.Handler())
	t.Cleanup(ts.Close)
	newURL := ts.URL

	oldRepoURL := oldURL + "/multi"
	alice := registerUser(t, oldURL, "alice")
	repoA := t.TempDir()
	if err := cli.RunInit(repoA, oldRepoURL, alice); err != nil {
		t.Fatalf("init: %v", err)
	}
	sA, idxA := openRepo(t, repoA)

	// 5 commits — more than two pages at page size 2.
	for i := 0; i < 5; i++ {
		commitAndPush(t, sA, idxA, repoA, oldRepoURL, alice,
			fmt.Sprintf("f%d.md", i), fmt.Sprintf("v%d\n", i), fmt.Sprintf("c%d", i))
	}

	if _, err := cli.RunMirror(oldRepoURL, newURL+"/multi", alice); err != nil {
		t.Fatalf("mirror: %v", err)
	}

	aliceNew, _ := client.New(newURL+"/multi", alice)
	newCommits, _ := aliceNew.GetAllCommits(0)
	if len(newCommits) != 5 {
		t.Errorf("expected all 5 commits mirrored across pages, got %d", len(newCommits))
	}
}

// TestMirrorUsernameCollisionAutoRename verifies that when the target directory
// already holds a DIFFERENT principal with the same username, the incoming
// roster principal is auto-renamed during directory seeding.
func TestMirrorUsernameCollisionAutoRename(t *testing.T) {
	oldURL := testServer(t)
	newURL := testServer(t)

	// alice on the source (principal Y).
	aliceSrc := registerUser(t, oldURL, "alice")
	// A DIFFERENT principal already named "alice" on the target. When the mirror
	// seeds aliceSrc's roster entry (also "alice"), the username collides and is
	// auto-renamed — the behavior this test validates.
	_ = registerUser(t, newURL, "alice")

	oldRepoURL := oldURL + "/coll"
	repoA := t.TempDir()
	if err := cli.RunInit(repoA, oldRepoURL, aliceSrc); err != nil {
		t.Fatalf("init: %v", err)
	}
	sA, idxA := openRepo(t, repoA)
	commitAndPush(t, sA, idxA, repoA, oldRepoURL, aliceSrc, "f.md", "x\n", "c1")

	if _, err := cli.RunMirror(oldRepoURL, newURL+"/coll", aliceSrc); err != nil {
		t.Fatalf("mirror: %v", err)
	}

	// The source alice (principal Y) now exists in the target directory under a
	// renamed username, since "alice" was taken by a different principal there.
	cl, _ := client.New(newURL+"/coll", aliceSrc)
	who, err := cl.WhoAmI()
	if err != nil {
		t.Fatalf("whoami on new home: %v", err)
	}
	if who.Username == "alice" {
		t.Error("source principal should have been auto-renamed on username collision")
	}
	if who.PrincipalID != aliceSrc.PrincipalID {
		t.Errorf("principal_id should be preserved across the move: got %s", who.PrincipalID)
	}
}

// TestMirrorRejectedByTargetLimits verifies the target server enforces its own
// limits on a mirror import.
func TestMirrorRejectedByTargetLimits(t *testing.T) {
	oldURL := testServer(t)
	// New home with a tiny file-size limit.
	newURL := testServerWithConfig(t, config.ServerConfig{
		Limits: config.LimitsSection{MaxFileSizeBytes: 8, AllowedFileTypes: []string{"text/*"}},
	})

	oldRepoURL := oldURL + "/big"
	alice := registerUser(t, oldURL, "alice")
	repoA := t.TempDir()
	if err := cli.RunInit(repoA, oldRepoURL, alice); err != nil {
		t.Fatalf("init: %v", err)
	}
	sA, idxA := openRepo(t, repoA)
	commitAndPush(t, sA, idxA, repoA, oldRepoURL, alice, "f.md", "this content is well over eight bytes\n", "c1")

	if _, err := cli.RunMirror(oldRepoURL, newURL+"/big", alice); err == nil {
		t.Error("mirror should be rejected by the target's file-size limit")
	}
}
