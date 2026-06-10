package store_test

import (
	"strings"
	"testing"

	"github.com/guyweissman/agentstore/internal/index"
	"github.com/guyweissman/agentstore/internal/store"
	"github.com/guyweissman/agentstore/internal/testutil"
)

func TestObjectStoreWriteRead(t *testing.T) {
	repo := testutil.NewRepo(t)
	content := []byte("hello agentstore")

	hash, err := repo.Store.Objects.WriteObject(content)
	if err != nil {
		t.Fatalf("WriteObject: %v", err)
	}
	if !repo.Store.Objects.HasObject(hash) {
		t.Error("HasObject should return true after WriteObject")
	}

	got, err := repo.Store.Objects.ReadObject(hash)
	if err != nil {
		t.Fatalf("ReadObject: %v", err)
	}
	if string(got) != string(content) {
		t.Errorf("ReadObject got %q, want %q", got, content)
	}
}

func TestObjectDedupe(t *testing.T) {
	repo := testutil.NewRepo(t)
	content := []byte("same content")

	h1, err := repo.Store.Objects.WriteObject(content)
	if err != nil {
		t.Fatalf("first write: %v", err)
	}
	h2, err := repo.Store.Objects.WriteObject(content)
	if err != nil {
		t.Fatalf("second write: %v", err)
	}
	if h1 != h2 {
		t.Error("same content must produce same hash")
	}
}

func TestWriteAndReadCommit(t *testing.T) {
	repo := testutil.NewRepo(t)

	content := []byte("# ICP\n\nContent.\n")
	hash, err := repo.Store.Objects.WriteObject(content)
	if err != nil {
		t.Fatalf("WriteObject: %v", err)
	}

	id, err := repo.Store.WriteCommit(store.CommitRecord{
		Message:  "Initial commit",
		AuthorID: store.StubPrincipalID,
		Files: []store.CommitFileRecord{
			{Path: "/strategy/icp.md", ObjectHash: hash, Size: int64(len(content)), ChangeType: "added"},
		},
	})
	if err != nil {
		t.Fatalf("WriteCommit: %v", err)
	}
	if len(id) != 64 {
		t.Errorf("commit ID should be 64 hex chars, got %d", len(id))
	}

	c, err := repo.Store.GetCommit(id)
	if err != nil {
		t.Fatalf("GetCommit: %v", err)
	}
	if c.Message != "Initial commit" {
		t.Errorf("message = %q, want %q", c.Message, "Initial commit")
	}
	if len(c.Files) != 1 || c.Files[0].Path != "/strategy/icp.md" {
		t.Errorf("unexpected files: %v", c.Files)
	}
}

func TestShortCommitIDLookup(t *testing.T) {
	repo := testutil.NewRepo(t)

	content := []byte("hello")
	hash, _ := repo.Store.Objects.WriteObject(content)
	id, err := repo.Store.WriteCommit(store.CommitRecord{
		Message:  "test",
		AuthorID: store.StubPrincipalID,
		Files:    []store.CommitFileRecord{{Path: "/f.md", ObjectHash: hash, Size: 5, ChangeType: "added"}},
	})
	if err != nil {
		t.Fatalf("WriteCommit: %v", err)
	}

	c, err := repo.Store.GetCommit(id[:8])
	if err != nil {
		t.Fatalf("GetCommit short: %v", err)
	}
	if c.ID != id {
		t.Errorf("short lookup returned wrong commit")
	}
}

func TestFileHead(t *testing.T) {
	repo := testutil.NewRepo(t)

	// Before any commit, FileHead should return nil.
	head, err := repo.Store.FileHead("/notes.md")
	if err != nil {
		t.Fatalf("FileHead before commit: %v", err)
	}
	if head != nil {
		t.Error("expected nil head for unknown file")
	}

	// After a commit, FileHead should return the commit.
	content := []byte("notes")
	hash, _ := repo.Store.Objects.WriteObject(content)
	id, err := repo.Store.WriteCommit(store.CommitRecord{
		Message:  "add notes",
		AuthorID: store.StubPrincipalID,
		Files:    []store.CommitFileRecord{{Path: "/notes.md", ObjectHash: hash, Size: 5, ChangeType: "added"}},
	})
	if err != nil {
		t.Fatalf("WriteCommit: %v", err)
	}

	head, err = repo.Store.FileHead("/notes.md")
	if err != nil {
		t.Fatalf("FileHead after commit: %v", err)
	}
	if head == nil {
		t.Fatal("expected non-nil head after commit")
	}
	if head.CommitID != id {
		t.Errorf("head commit = %s, want %s", head.CommitID[:8], id[:8])
	}
}

func TestLogCommits(t *testing.T) {
	repo := testutil.NewRepo(t)

	for i, msg := range []string{"first", "second", "third"} {
		content := []byte(msg)
		hash, _ := repo.Store.Objects.WriteObject(content)
		path := "/file.md"
		ct := "added"
		if i > 0 {
			ct = "modified"
		}
		if _, err := repo.Store.WriteCommit(store.CommitRecord{
			Message:  msg,
			AuthorID: store.StubPrincipalID,
			Files:    []store.CommitFileRecord{{Path: path, ObjectHash: hash, ChangeType: ct}},
		}); err != nil {
			t.Fatalf("WriteCommit %d: %v", i, err)
		}
	}

	commits, err := repo.Store.LogCommits(store.LogFilter{})
	if err != nil {
		t.Fatalf("LogCommits: %v", err)
	}
	if len(commits) != 3 {
		t.Fatalf("got %d commits, want 3", len(commits))
	}
	// Default order is newest-first.
	if commits[0].Message != "third" {
		t.Errorf("first result should be newest: got %q", commits[0].Message)
	}

	// Limit.
	limited, err := repo.Store.LogCommits(store.LogFilter{Limit: 1})
	if err != nil {
		t.Fatalf("LogCommits limit: %v", err)
	}
	if len(limited) != 1 {
		t.Errorf("limit=1 returned %d commits", len(limited))
	}

	// Reverse.
	reversed, err := repo.Store.LogCommits(store.LogFilter{Reverse: true})
	if err != nil {
		t.Fatalf("LogCommits reverse: %v", err)
	}
	if reversed[0].Message != "first" {
		t.Errorf("reverse first result should be oldest: got %q", reversed[0].Message)
	}
}

func TestSeqMonotonic(t *testing.T) {
	// Local (WriteCommit) commits have seq=NULL until pushed; seq monotonicity
	// is a server-side invariant enforced by WriteRemoteCommit.
	repo := testutil.NewRepo(t)
	for i := 0; i < 3; i++ {
		content := []byte{byte(i)}
		hash, _ := repo.Store.Objects.WriteObject(content)
		ct := "added"
		if i > 0 {
			ct = "modified"
		}
		id, err := repo.Store.WriteCommit(store.CommitRecord{
			Message:  "x",
			AuthorID: store.StubPrincipalID,
			Files:    []store.CommitFileRecord{{Path: "/f.md", ObjectHash: hash, ChangeType: ct}},
		})
		if err != nil {
			t.Fatalf("WriteCommit %d: %v", i, err)
		}
		c, _ := repo.Store.GetCommit(id)
		if c.Seq != 0 {
			t.Errorf("local commit should have seq=0 (unconfirmed), got %d", c.Seq)
		}
	}

	// WriteRemoteCommit (server path) must assign monotonically increasing seq.
	var prevSeq int64
	for i := 0; i < 3; i++ {
		content := []byte{byte(i + 100)}
		hash, _ := repo.Store.Objects.WriteObject(content)
		id, err := repo.Store.WriteRemoteCommit(store.CommitRecord{
			Message:  "remote",
			AuthorID: store.StubPrincipalID,
			Files:    []store.CommitFileRecord{{Path: "/r.md", ObjectHash: hash, ChangeType: "added"}},
		}, 0, "") // seq=0 → auto-assign; "" → recompute canonical ID
		if err != nil {
			t.Fatalf("WriteRemoteCommit %d: %v", i, err)
		}
		c, _ := repo.Store.GetCommit(id)
		if c.Seq <= prevSeq {
			t.Errorf("server seq not monotonically increasing: got %d after %d", c.Seq, prevSeq)
		}
		prevSeq = c.Seq
	}
}

func TestDeleteAndRecreate(t *testing.T) {
	repo := testutil.NewRepo(t)

	// Add file.
	data1 := []byte("v1")
	h1, _ := repo.Store.Objects.WriteObject(data1)
	id1, err := repo.Store.WriteCommit(store.CommitRecord{
		Message: "add", AuthorID: store.StubPrincipalID,
		Files: []store.CommitFileRecord{{Path: "/f.md", ObjectHash: h1, ChangeType: "added"}},
	})
	if err != nil {
		t.Fatalf("add: %v", err)
	}

	// Delete file.
	if _, err := repo.Store.WriteCommit(store.CommitRecord{
		Message: "del", AuthorID: store.StubPrincipalID,
		Files: []store.CommitFileRecord{{Path: "/f.md", ObjectHash: "", ChangeType: "deleted"}},
	}); err != nil {
		t.Fatalf("delete: %v", err)
	}

	// FileHead should show deleted.
	head, err := repo.Store.FileHead("/f.md")
	if err != nil {
		t.Fatalf("FileHead after delete: %v", err)
	}
	if head == nil || head.ChangeType != "deleted" {
		t.Errorf("expected deleted head, got %+v", head)
	}

	// Recreate.
	data2 := []byte("v2")
	h2, _ := repo.Store.Objects.WriteObject(data2)
	if _, err := repo.Store.WriteCommit(store.CommitRecord{
		Message: "recreate", AuthorID: store.StubPrincipalID,
		Files: []store.CommitFileRecord{{Path: "/f.md", ObjectHash: h2, ChangeType: "added"}},
	}); err != nil {
		t.Fatalf("recreate: %v", err)
	}

	head, err = repo.Store.FileHead("/f.md")
	if err != nil {
		t.Fatalf("FileHead after recreate: %v", err)
	}
	if head == nil || head.ChangeType != "added" {
		t.Errorf("expected active head after recreate, got %+v", head)
	}
	_ = id1 // used to set the initial commit
}

func TestStagingAndCommit(t *testing.T) {
	repo := testutil.NewRepo(t)

	// Stage a file.
	data := []byte("hello world")
	hash, _ := repo.Store.Objects.WriteObject(data)
	if err := repo.Index.Stage(index.StagedEntry{
		Path: "/hello.md", ObjectHash: hash, ChangeType: "added",
	}); err != nil {
		t.Fatalf("Stage: %v", err)
	}

	entries, err := repo.Index.Entries()
	if err != nil {
		t.Fatalf("Entries: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 staged entry, got %d", len(entries))
	}

	// Commit from index.
	from_staged_to_commit(t, repo)

	// Index should be clear after commit.
	entries, _ = repo.Index.Entries()
	if len(entries) != 0 {
		t.Error("index should be empty after commit")
	}

	// Commit should be in log.
	commits, _ := repo.Store.LogCommits(store.LogFilter{})
	if len(commits) == 0 {
		t.Fatal("expected at least one commit in log")
	}
	if !strings.Contains(commits[0].Message, "staged test") {
		t.Errorf("unexpected message: %s", commits[0].Message)
	}
}

func from_staged_to_commit(t *testing.T, repo *testutil.Repo) {
	t.Helper()
	entries, err := repo.Index.Entries()
	if err != nil || len(entries) == 0 {
		t.Fatal("no staged entries to commit")
	}
	latestID, _ := repo.Store.LatestCommitID()
	var parents []string
	if latestID != "" {
		parents = []string{latestID}
	}
	files := make([]store.CommitFileRecord, len(entries))
	for i, e := range entries {
		var size int64
		if e.ObjectHash != "" {
			data, _ := repo.Store.Objects.ReadObject(e.ObjectHash)
			size = int64(len(data))
		}
		files[i] = store.CommitFileRecord{
			Path:       e.Path,
			ObjectHash: e.ObjectHash,
			ChangeType: e.ChangeType,
			Size:       size,
		}
	}
	if _, err := repo.Store.WriteCommit(store.CommitRecord{
		Message:  "staged test",
		AuthorID: store.StubPrincipalID,
		Parents:  parents,
		Files:    files,
	}); err != nil {
		t.Fatalf("WriteCommit from staged: %v", err)
	}
	repo.Index.Clear()
}
