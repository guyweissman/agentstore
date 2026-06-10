package server_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"testing"

	"golang.org/x/crypto/ssh"

	"github.com/guyweissman/agentstore/internal/cli"
	"github.com/guyweissman/agentstore/internal/client"
)

// TestIdentityFlow covers register → whoami → rekey end to end.
func TestIdentityFlow(t *testing.T) {
	serverURL := testServer(t)

	alice := registerUser(t, serverURL, "alice")

	// whoami resolves the signing identity back to the username.
	cl, _ := client.New(serverURL+"/_", alice)
	who, err := cl.WhoAmI()
	if err != nil {
		t.Fatalf("whoami: %v", err)
	}
	if who.Username != "alice" {
		t.Errorf("whoami = %q, want alice", who.Username)
	}
	if who.PrincipalID != alice.PrincipalID {
		t.Errorf("whoami principal = %q, want %q", who.PrincipalID, alice.PrincipalID)
	}

	// Duplicate username must be rejected.
	dupCl, _ := client.New(serverURL+"/_", nil)
	if _, err := dupCl.Register("alice", publicKeyLine(t, alice)); err == nil {
		t.Error("registering a duplicate username should fail")
	}

	// Rekey to a fresh keypair, then sign with the new key.
	newPub, newPriv, _ := ed25519.GenerateKey(rand.Reader)
	sshPub, _ := ssh.NewPublicKey(newPub)
	newPubLine := string(ssh.MarshalAuthorizedKey(sshPub))
	if err := cl.Rekey(newPubLine); err != nil {
		t.Fatalf("rekey: %v", err)
	}

	// The old key must no longer authenticate.
	if _, err := cl.WhoAmI(); err == nil {
		t.Error("whoami with the old key should fail after rekey")
	}

	// The new key authenticates as the same principal.
	rekeyed := &client.Identity{PrincipalID: alice.PrincipalID, PrivateKey: newPriv}
	newCl, _ := client.New(serverURL+"/_", rekeyed)
	who2, err := newCl.WhoAmI()
	if err != nil {
		t.Fatalf("whoami after rekey: %v", err)
	}
	if who2.Username != "alice" {
		t.Errorf("after rekey whoami = %q, want alice", who2.Username)
	}
}

// TestDirectoryBrowse covers `principals list --remote`: the open directory
// browse enumerates every registered principal regardless of repo membership,
// while the repo-scoped roster still returns only the repo's members.
func TestDirectoryBrowse(t *testing.T) {
	serverURL := testServer(t)

	// Register several identities. Only some become members of a repo.
	alice := registerUser(t, serverURL, "alice")
	registerUser(t, serverURL, "bob")
	registerUser(t, serverURL, "carol")

	// Browse the directory unauthenticated (no identity, no repo) — like the CLI
	// `principals list --remote` path, which resolves via serverBase+"/_".
	browseCl, _ := client.New(serverURL+"/_", nil)
	entries, err := browseCl.ListDirectory()
	if err != nil {
		t.Fatalf("list directory: %v", err)
	}
	got := map[string]string{}
	for _, e := range entries {
		got[e.Username] = e.PrincipalID
		if e.PublicKey == "" {
			t.Errorf("directory entry %q missing public_key", e.Username)
		}
	}
	for _, name := range []string{"alice", "bob", "carol"} {
		if _, ok := got[name]; !ok {
			t.Errorf("directory browse missing %q; got %v", name, got)
		}
	}
	if got["alice"] != alice.PrincipalID {
		t.Errorf("alice principal = %q, want %q", got["alice"], alice.PrincipalID)
	}

	// Alice creates a repo (she becomes the sole member). The repo-scoped roster
	// must list only her, not the whole directory.
	repoURL := serverURL + "/team"
	repoDir := t.TempDir()
	if err := cli.RunInit(repoDir, repoURL, alice); err != nil {
		t.Fatalf("init: %v", err)
	}
	aliceClient, _ := client.New(repoURL, alice)
	members, err := aliceClient.ListMembers()
	if err != nil {
		t.Fatalf("list members: %v", err)
	}
	if len(members) != 1 || members[0].Username != "alice" {
		t.Errorf("repo roster = %v, want only alice", members)
	}
}

// TestUnsignedRequestRejected verifies that a signed endpoint rejects an
// unauthenticated request.
func TestUnsignedRequestRejected(t *testing.T) {
	serverURL := testServer(t)
	// No identity → no signature headers.
	cl, _ := client.New(serverURL+"/somerepo", nil)
	if err := cl.CreateRepo(); err == nil {
		t.Error("creating a repo without a signature should be rejected")
	}
}
