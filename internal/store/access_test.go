package store_test

import (
	"testing"

	"github.com/guyweissman/agentstore/internal/store"
	"github.com/guyweissman/agentstore/internal/testutil"
)

// TestAllGrantsPreservesGrantedBy verifies AllGrants returns the actual granter,
// not the grantee — the provenance that a mirror must carry verbatim.
func TestAllGrantsPreservesGrantedBy(t *testing.T) {
	repo := testutil.NewRepo(t)
	s := repo.Store

	// Two principals: alice grants bob read on /x.
	if err := s.AddPrincipal(store.Principal{ID: "principal_alice", Username: "alice"}); err != nil {
		t.Fatal(err)
	}
	if err := s.AddPrincipal(store.Principal{ID: "principal_bob", Username: "bob"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetGrant("principal_bob", "/x/*", store.PermRead, "principal_alice"); err != nil {
		t.Fatal(err)
	}

	grants, err := s.AllGrants()
	if err != nil {
		t.Fatalf("AllGrants: %v", err)
	}
	var found *store.Grant
	for i := range grants {
		if grants[i].PrincipalID == "principal_bob" && grants[i].PathPattern == "/x/*" {
			found = &grants[i]
		}
	}
	if found == nil {
		t.Fatal("grant not returned")
	}
	if found.GrantedBy != "principal_alice" {
		t.Errorf("granted_by = %q, want principal_alice (the granter, not the grantee)", found.GrantedBy)
	}
}
