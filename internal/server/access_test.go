package server_test

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/guyweissman/agentstore/internal/cli"
	"github.com/guyweissman/agentstore/internal/client"
	"github.com/guyweissman/agentstore/internal/server"
	"github.com/guyweissman/agentstore/internal/store"
)

// TestM3_PermissionsAndRevoke is the M3 acceptance scenario:
// alice and bob exist as principals; grants control who can read/write which
// paths; a permission-filtered clone only pulls authorized files; and a revoke
// immediately and retroactively cuts access.
func TestM3_PermissionsAndRevoke(t *testing.T) {
	serverURL := testServer(t)
	repoURL := serverURL + "/acme"

	alice := registerUser(t, serverURL, "alice")
	bob := registerUser(t, serverURL, "bob")

	// Alice creates the repo (admin + owner of /*).
	repoA := t.TempDir()
	if err := cli.RunInit(repoA, repoURL, alice); err != nil {
		t.Fatalf("alice init: %v", err)
	}
	sA, idxA := openRepo(t, repoA)

	// Alice commits two files in different folders.
	writeFile(t, filepath.Join(repoA, "strategy", "plan.md"), "the plan\n")
	writeFile(t, filepath.Join(repoA, "finance", "salaries.md"), "secret numbers\n")
	cli.RunAdd(sA, idxA, repoA, []string{
		filepath.Join(repoA, "strategy", "plan.md"),
		filepath.Join(repoA, "finance", "salaries.md"),
	})
	if _, err := cli.RunCommit(sA, idxA, "Add strategy and finance"); err != nil {
		t.Fatalf("alice commit: %v", err)
	}
	if _, err := cli.RunPush(sA, repoURL, alice); err != nil {
		t.Fatalf("alice push: %v", err)
	}

	aliceClient, _ := client.New(repoURL, alice)
	bobClient, _ := client.New(repoURL, bob)

	// Bob is registered but not a member — repos are private by default, so he
	// cannot read anything, not even redacted stubs or the roster.
	if _, err := bobClient.GetCommits(0); err == nil {
		t.Error("non-member bob should be denied repo metadata")
	}
	if _, err := bobClient.GetPrincipals(); err == nil {
		t.Error("non-member bob should be denied the roster")
	}

	// Alice adds bob to the repo and grants him read on /strategy only.
	if err := aliceClient.AddMember("bob"); err != nil {
		t.Fatalf("add member bob: %v", err)
	}
	if err := aliceClient.Grant("bob", store.PermRead, "/strategy/*"); err != nil {
		t.Fatalf("grant bob read /strategy/*: %v", err)
	}

	// Bob clones — permission-filtered: he gets /strategy but NOT /finance.
	repoB := t.TempDir()
	if err := cli.RunClone(repoURL, repoB, bob); err != nil {
		t.Fatalf("bob clone: %v", err)
	}
	if !fileExists(filepath.Join(repoB, "strategy", "plan.md")) {
		t.Error("bob's clone should contain /strategy/plan.md")
	}
	if fileExists(filepath.Join(repoB, "finance", "salaries.md")) {
		t.Error("bob's clone must NOT contain /finance/salaries.md")
	}

	// Bob cannot write to /strategy (only read) — a push must be forbidden.
	sB, idxB := openRepo(t, repoB)
	writeFile(t, filepath.Join(repoB, "strategy", "plan.md"), "bob's edit\n")
	cli.RunAdd(sB, idxB, repoB, []string{filepath.Join(repoB, "strategy", "plan.md")})
	if _, err := cli.RunCommit(sB, idxB, "bob edits plan"); err != nil {
		t.Fatalf("bob commit: %v", err)
	}
	_, err := cli.RunPush(sB, repoURL, bob)
	if err == nil {
		t.Error("bob's push should be forbidden (read-only on /strategy)")
	}

	// Alice revokes bob's read. It is immediate and retroactive: a fresh clone
	// (or any read) now returns nothing for bob.
	if err := aliceClient.Revoke("bob", "/strategy/*"); err != nil {
		t.Fatalf("revoke bob: %v", err)
	}
	commits, err := bobClient.GetCommits(0)
	if err != nil {
		t.Fatalf("bob get commits after revoke: %v", err)
	}
	for _, c := range commits {
		if !c.Redacted {
			t.Errorf("after revoke, bob must see only redacted stubs; saw commit %s", c.ID)
		}
	}
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// TestFilteredCloneWritePush is a regression test for a file-level OCC bug.
// Commit IDs are content-addressed over a commit's full (path, object_hash) set.
// A permission-filtered clone receives only a subset of that set, so re-deriving
// the ID locally produced a DIFFERENT id than the server's. A member with a
// legitimate write grant then based edits on that divergent id, and the push was
// rejected as a false OCC conflict — breaking the core segmented multi-agent write
// path. The clone must keep the server's canonical commit id verbatim.
func TestFilteredCloneWritePush(t *testing.T) {
	serverURL := testServer(t)
	repoURL := serverURL + "/acme"

	alice := registerUser(t, serverURL, "alice")
	bob := registerUser(t, serverURL, "bob")

	repoA := t.TempDir()
	if err := cli.RunInit(repoA, repoURL, alice); err != nil {
		t.Fatalf("alice init: %v", err)
	}
	sA, idxA := openRepo(t, repoA)

	// Two files in different folders; bob will only ever see /strategy.
	writeFile(t, filepath.Join(repoA, "strategy", "plan.md"), "the plan\n")
	writeFile(t, filepath.Join(repoA, "finance", "salaries.md"), "secret numbers\n")
	cli.RunAdd(sA, idxA, repoA, []string{
		filepath.Join(repoA, "strategy", "plan.md"),
		filepath.Join(repoA, "finance", "salaries.md"),
	})
	if _, err := cli.RunCommit(sA, idxA, "seed strategy and finance"); err != nil {
		t.Fatalf("alice commit: %v", err)
	}
	if _, err := cli.RunPush(sA, repoURL, alice); err != nil {
		t.Fatalf("alice push: %v", err)
	}

	aliceClient, _ := client.New(repoURL, alice)
	if err := aliceClient.AddMember("bob"); err != nil {
		t.Fatalf("add member bob: %v", err)
	}
	// Bob gets WRITE on /strategy (write implies read) — and nothing on /finance.
	if err := aliceClient.Grant("bob", store.PermWrite, "/strategy/*"); err != nil {
		t.Fatalf("grant bob write /strategy/*: %v", err)
	}

	// Bob clones — permission-filtered to /strategy only.
	repoB := t.TempDir()
	if err := cli.RunClone(repoURL, repoB, bob); err != nil {
		t.Fatalf("bob clone: %v", err)
	}
	if fileExists(filepath.Join(repoB, "finance", "salaries.md")) {
		t.Fatal("bob's filtered clone must NOT contain /finance/salaries.md")
	}
	sB, idxB := openRepo(t, repoB)

	// Root cause: the filtered clone must preserve the server's canonical commit id,
	// so the head bob bases his edit on equals the server head.
	headA, err := sA.FileHead("/strategy/plan.md")
	if err != nil || headA == nil {
		t.Fatalf("alice head: %v", err)
	}
	headB, err := sB.FileHead("/strategy/plan.md")
	if err != nil || headB == nil {
		t.Fatalf("bob head: %v", err)
	}
	if headB.CommitID != headA.CommitID {
		t.Fatalf("filtered clone changed the commit id: alice %s, bob %s", headA.CommitID, headB.CommitID)
	}

	// Bob modifies the file he is allowed to write and pushes — must succeed on the
	// first try, with no false OCC conflict.
	writeFile(t, filepath.Join(repoB, "strategy", "plan.md"), "the plan\nbob's addition\n")
	cli.RunAdd(sB, idxB, repoB, []string{filepath.Join(repoB, "strategy", "plan.md")})
	if _, err := cli.RunCommit(sB, idxB, "bob extends the plan"); err != nil {
		t.Fatalf("bob commit: %v", err)
	}
	if _, err := cli.RunPush(sB, repoURL, bob); err != nil {
		t.Fatalf("bob's push of a writable file was rejected as a false OCC conflict: %v", err)
	}
}

// TestNonMemberDenied verifies repos are private by default: a registered
// identity that is not a repo member is denied all repo-scoped reads.
func TestNonMemberDenied(t *testing.T) {
	serverURL := testServer(t)
	repoURL := serverURL + "/private"

	alice := registerUser(t, serverURL, "alice")
	bob := registerUser(t, serverURL, "bob") // registered, never added

	repoA := t.TempDir()
	if err := cli.RunInit(repoA, repoURL, alice); err != nil {
		t.Fatalf("init: %v", err)
	}

	bobCl, _ := client.New(repoURL, bob)
	if _, err := bobCl.GetCommits(0); err == nil {
		t.Error("non-member must be denied GetCommits")
	}
	if _, err := bobCl.GetHeads(); err == nil {
		t.Error("non-member must be denied GetHeads")
	}
	if _, err := bobCl.GetPrincipals(); err == nil {
		t.Error("non-member must be denied the roster")
	}
	// A non-member cannot clone at all.
	repoB := t.TempDir()
	if err := cli.RunClone(repoURL, repoB, bob); err == nil {
		t.Error("non-member must not be able to clone a private repo")
	}
}

// TestGrantRejectsInvalidPattern verifies grant path patterns are validated.
func TestGrantRejectsInvalidPattern(t *testing.T) {
	serverURL := testServer(t)
	repoURL := serverURL + "/gp"
	alice := registerUser(t, serverURL, "alice")
	bob := registerUser(t, serverURL, "bob")
	_ = bob

	repoA := t.TempDir()
	if err := cli.RunInit(repoA, repoURL, alice); err != nil {
		t.Fatalf("init: %v", err)
	}
	aliceCl, _ := client.New(repoURL, alice)
	aliceCl.AddMember("bob")

	for _, bad := range []string{"/../x", "relative", "/a/*/b", "/a//b", "//"} {
		if err := aliceCl.Grant("bob", "read", bad); err == nil {
			t.Errorf("grant with invalid pattern %q should be rejected", bad)
		}
	}
	// A valid prefix pattern is accepted.
	if err := aliceCl.Grant("bob", "read", "/strategy/*"); err != nil {
		t.Errorf("valid prefix grant should be accepted: %v", err)
	}
}

// TestPushRejectsMalformedDelete verifies the server refuses a "deleted" entry
// that still carries an object hash (structural invariant).
func TestPushRejectsMalformedDelete(t *testing.T) {
	serverURL := testServer(t)
	repoURL := serverURL + "/maldel"
	alice := registerUser(t, serverURL, "alice")

	repoA := t.TempDir()
	if err := cli.RunInit(repoA, repoURL, alice); err != nil {
		t.Fatalf("init: %v", err)
	}
	cl, _ := client.New(repoURL, alice)
	content := []byte("x\n")
	if err := cl.UploadObject(store.HashContent(content), content); err != nil {
		t.Fatalf("upload: %v", err)
	}
	// change_type=deleted with a non-empty object_hash must be rejected.
	res, err := cl.Push(server.PushRequest{
		Message:   "bad delete",
		CreatedAt: 1,
		Parents:   []string{},
		Files:     []server.PushFile{{Path: "/f.md", ChangeType: "deleted", ObjectHash: store.HashContent(content)}},
	})
	if err == nil && len(res.Conflicts) == 0 {
		t.Error("delete entry with an object hash should be rejected")
	}
}

// TestCreateRepoRejectsBadName verifies repo names are validated, not trusted.
func TestCreateRepoRejectsBadName(t *testing.T) {
	serverURL := testServer(t)
	alice := registerUser(t, serverURL, "alice")
	// "." is a path-unsafe name; the client URL parse + server validation must reject it.
	cl, err := client.New(serverURL+"/.", alice)
	if err != nil {
		return // client rejected it — acceptable
	}
	if err := cl.CreateRepo(); err == nil {
		t.Error(`repo name "." should be rejected`)
	}
}

// TestReadOnlyMemberCannotUpload verifies a read-only member cannot store objects.
func TestReadOnlyMemberCannotUpload(t *testing.T) {
	serverURL := testServer(t)
	repoURL := serverURL + "/ro"
	alice := registerUser(t, serverURL, "alice")
	bob := registerUser(t, serverURL, "bob")

	repoA := t.TempDir()
	if err := cli.RunInit(repoA, repoURL, alice); err != nil {
		t.Fatalf("init: %v", err)
	}
	aliceCl, _ := client.New(repoURL, alice)
	aliceCl.AddMember("bob")
	aliceCl.Grant("bob", "read", "/strategy/*")

	bobCl, _ := client.New(repoURL, bob)
	content := []byte("blob\n")
	if err := bobCl.UploadObject(store.HashContent(content), content); err == nil {
		t.Error("read-only member should not be able to upload objects")
	}
}

// TestRemovePrincipalWhoAuthoredAndGranted verifies a member who authored commits
// and granted roles can be removed (historical attributions are not FKs).
func TestRemovePrincipalWhoAuthoredAndGranted(t *testing.T) {
	serverURL := testServer(t)
	repoURL := serverURL + "/rm"
	alice := registerUser(t, serverURL, "alice")
	bob := registerUser(t, serverURL, "bob")
	carol := registerUser(t, serverURL, "carol")

	repoA := t.TempDir()
	if err := cli.RunInit(repoA, repoURL, alice); err != nil {
		t.Fatalf("init: %v", err)
	}
	sA, idxA := openRepo(t, repoA)
	commitAndPush(t, sA, idxA, repoA, repoURL, alice, "a.md", "a\n", "c1")

	aliceCl, _ := client.New(repoURL, alice)
	aliceCl.AddMember("bob")
	aliceCl.AddMember("carol")
	aliceCl.AddAdmin("bob") // bob is now also an admin (so alice isn't the last)

	// Bob (as admin) grants carol — bob is now a grantor and an admin.
	bobCl, _ := client.New(repoURL, bob)
	if err := bobCl.Grant("carol", "write", "/a.md"); err != nil {
		t.Fatalf("bob grants carol: %v", err)
	}
	// Bob authors a commit too.
	repoB := t.TempDir()
	cli.RunClone(repoURL, repoB, bob)
	sB, idxB := openRepo(t, repoB)
	commitAndPush(t, sB, idxB, repoB, repoURL, bob, "b.md", "b\n", "c2")

	// Now remove bob: he authored c2 and granted carol. Must succeed.
	if err := aliceCl.RemoveMember("bob"); err != nil {
		t.Fatalf("removing a principal who authored/granted should succeed: %v", err)
	}
	// Bob is no longer a member: his reads are denied.
	if _, err := bobCl.GetCommits(0); err == nil {
		t.Error("removed member should be denied")
	}
	// Carol's grant (granted_by bob) survived the removal.
	carolCl, _ := client.New(repoURL, carol)
	if _, err := carolCl.GetCommits(0); err != nil {
		t.Errorf("carol should still have access after her grantor was removed: %v", err)
	}
}

// TestPushRejectsTraversalPath verifies the server refuses a commit whose file
// path would escape a working tree when materialized (path traversal).
func TestPushRejectsTraversalPath(t *testing.T) {
	serverURL := testServer(t)
	repoURL := serverURL + "/trav"
	alice := registerUser(t, serverURL, "alice")

	repoA := t.TempDir()
	if err := cli.RunInit(repoA, repoURL, alice); err != nil {
		t.Fatalf("init: %v", err)
	}
	cl, _ := client.New(repoURL, alice)

	// Upload a benign object, then try to commit it under a traversal path.
	content := []byte("pwned\n")
	if err := cl.UploadObject(store.HashContent(content), content); err != nil {
		t.Fatalf("upload: %v", err)
	}
	for _, bad := range []string{"/../../etc/passwd", "/a/../../b", "//x", "/a/", "relative"} {
		res, err := cl.Push(server.PushRequest{
			Message:   "evil",
			CreatedAt: 1,
			Parents:   []string{},
			Files:     []server.PushFile{{Path: bad, ChangeType: "added", ObjectHash: store.HashContent(content)}},
		})
		if err == nil && len(res.Conflicts) == 0 {
			t.Errorf("push with invalid path %q should be rejected", bad)
		}
	}
}

// TestPushRejectsMissingObject verifies the server refuses commit metadata that
// references an object never uploaded (object-before-metadata invariant).
func TestPushRejectsMissingObject(t *testing.T) {
	serverURL := testServer(t)
	repoURL := serverURL + "/objcheck"
	alice := registerUser(t, serverURL, "alice")

	repoA := t.TempDir()
	if err := cli.RunInit(repoA, repoURL, alice); err != nil {
		t.Fatalf("init: %v", err)
	}

	// Craft a push that references an object hash that was never uploaded.
	cl, _ := client.New(repoURL, alice)
	bogusHash := "0000000000000000000000000000000000000000000000000000000000000000"
	res, err := cl.Push(server.PushRequest{
		Message:   "dangling",
		CreatedAt: 1,
		Parents:   []string{},
		Files: []server.PushFile{
			{Path: "/x.md", ChangeType: "added", ObjectHash: bogusHash},
		},
	})
	if err == nil && len(res.Conflicts) == 0 {
		t.Error("push referencing a missing object must be rejected")
	}
}

// TestPushRejectsMalformedObjectHash verifies a malformed (non-64-hex) object
// hash is rejected with a clean error rather than panicking the handler (the
// hash would otherwise be sliced as hash[:2] in objectPath).
func TestPushRejectsMalformedObjectHash(t *testing.T) {
	serverURL := testServer(t)
	repoURL := serverURL + "/badhash"
	alice := registerUser(t, serverURL, "alice")
	repoA := t.TempDir()
	if err := cli.RunInit(repoA, repoURL, alice); err != nil {
		t.Fatalf("init: %v", err)
	}
	cl, _ := client.New(repoURL, alice)
	for _, bad := range []string{"a", "xyz", "ZZZ", "abc123" /* too short */} {
		res, err := cl.Push(server.PushRequest{
			Message: "x", CreatedAt: 1, Parents: []string{},
			Files: []server.PushFile{{Path: "/f.md", ChangeType: "added", ObjectHash: bad}},
		})
		if err == nil && len(res.Conflicts) == 0 {
			t.Errorf("push with malformed object hash %q should be rejected", bad)
		}
	}
}

// TestMirrorFailureLeavesNoRepo verifies a failed mirror import removes its
// partial repo directory, so it can't be picked up on restart or block a retry.
func TestMirrorFailureLeavesNoRepo(t *testing.T) {
	dataDir := t.TempDir()
	srv, err := server.New(dataDir)
	if err != nil {
		t.Fatalf("server.New: %v", err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	alice := registerUser(t, ts.URL, "alice")

	content := []byte("x\n")
	hash := store.HashContent(content)
	// A mirror that passes every pre-check but carries a commit whose ID does not
	// match its content — applyMirror fails at the commit-id verification.
	req := server.MirrorRequest{
		Principals: []server.PrincipalJSON{{ID: alice.PrincipalID, Username: "alice", PublicKey: publicKeyLine(t, alice)}},
		Roles:      []server.MirrorRole{{PrincipalID: alice.PrincipalID, Role: "admin", GrantedBy: alice.PrincipalID}},
		Objects:    []server.MirrorObject{{Hash: hash, Content: content}},
		Commits: []server.CommitJSON{{
			ID:  "0000000000000000000000000000000000000000000000000000000000000000",
			Seq: 1, Message: "c", AuthorID: alice.PrincipalID, CreatedAt: 1, Parents: []string{},
			Files: []server.CommitFileJSON{{Path: "/f.md", ObjectHash: hash, Size: int64(len(content)), ChangeType: "added"}},
		}},
	}
	cl, _ := client.New(ts.URL+"/corrupt", alice)
	if _, err := cl.Mirror(req); err == nil {
		t.Fatal("mirror with a mismatched commit id should fail")
	}

	// The partial repo directory must have been cleaned up.
	if _, err := os.Stat(filepath.Join(dataDir, "corrupt")); !os.IsNotExist(err) {
		t.Errorf("failed mirror left a partial repo dir behind (err=%v)", err)
	}
}
