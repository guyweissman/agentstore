package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/guyweissman/agentstore/internal/config"
)

// TestConfigSetGet round-trips a value through the local config file via the
// dotted-key set/get path, and confirms the typed loader reads it back.
func TestConfigSetGet(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".agentstore"), 0o755); err != nil {
		t.Fatal(err)
	}
	path := config.RepoConfigPath(root)

	if err := configSet(path, "remotes.origin.url", "http://127.0.0.1:8080/repo"); err != nil {
		t.Fatalf("set: %v", err)
	}
	if err := configSet(path, "identity.principal_id", "principal_xyz"); err != nil {
		t.Fatalf("set: %v", err)
	}

	// Generic get returns what we set.
	if err := configGet(path, "remotes.origin.url"); err != nil {
		t.Errorf("get url: %v", err)
	}
	if err := configGet(path, "identity.principal_id"); err != nil {
		t.Errorf("get principal: %v", err)
	}
	// A missing key errors.
	if err := configGet(path, "remotes.origin.nope"); err == nil {
		t.Error("get of a missing key should error")
	}

	// The typed loader must read the same values (round-trip consistency with the
	// structured config the app uses).
	rc, err := config.LoadRepo(root)
	if err != nil {
		t.Fatalf("LoadRepo: %v", err)
	}
	if rc.Remotes["origin"].URL != "http://127.0.0.1:8080/repo" {
		t.Errorf("typed loader URL = %q", rc.Remotes["origin"].URL)
	}
	if rc.Identity.PrincipalID != "principal_xyz" {
		t.Errorf("typed loader principal = %q", rc.Identity.PrincipalID)
	}
}

// TestConfigSetPreservesSiblings verifies setting one key doesn't drop others.
func TestConfigSetPreservesSiblings(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".agentstore"), 0o755); err != nil {
		t.Fatal(err)
	}
	path := config.RepoConfigPath(root)

	if err := configSet(path, "remotes.origin.url", "u1"); err != nil {
		t.Fatal(err)
	}
	if err := configSet(path, "remotes.newhome.url", "u2"); err != nil {
		t.Fatal(err)
	}
	rc, _ := config.LoadRepo(root)
	if rc.Remotes["origin"].URL != "u1" || rc.Remotes["newhome"].URL != "u2" {
		t.Errorf("setting a sibling dropped a remote: %+v", rc.Remotes)
	}
}
