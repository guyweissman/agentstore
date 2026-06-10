package server_test

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/guyweissman/agentstore/internal/cli"
	"github.com/guyweissman/agentstore/internal/client"
	"github.com/guyweissman/agentstore/internal/index"
	"github.com/guyweissman/agentstore/internal/server"
	"github.com/guyweissman/agentstore/internal/store"
)

// collector accumulates events from a watch stream, thread-safe.
type collector struct {
	mu     sync.Mutex
	events []server.EventJSON
}

func (c *collector) add(e server.EventJSON) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events = append(c.events, e)
}

func (c *collector) commitsSeen() map[string]bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	seen := map[string]bool{}
	for _, e := range c.events {
		if e.Type == server.EventCommitPushed {
			seen[e.Message] = true
		}
	}
	return seen
}

// commitAndPush is a helper: write a file, commit, push.
func commitAndPush(t *testing.T, s *store.Store, idx *index.Index, root, repoURL string, id *client.Identity, path, content, msg string) {
	t.Helper()
	writeFile(t, filepath.Join(root, path), content)
	cli.RunAdd(s, idx, root, []string{filepath.Join(root, path)})
	if _, err := cli.RunCommit(s, idx, msg); err != nil {
		t.Fatalf("commit %q: %v", msg, err)
	}
	if _, err := cli.RunPush(s, repoURL, id); err != nil {
		t.Fatalf("push %q: %v", msg, err)
	}
}

// TestWatchLiveAndRecovery is the M4 acceptance scenario: a client watches a path
// and sees live events; after a forced disconnect it reconnects with its cursor,
// the server backfills the gap, and it resumes with no missed commits.
func TestWatchLiveAndRecovery(t *testing.T) {
	serverURL := testServer(t)
	repoURL := serverURL + "/live"
	alice := registerUser(t, serverURL, "alice")

	repoA := t.TempDir()
	if err := cli.RunInit(repoA, repoURL, alice); err != nil {
		t.Fatalf("init: %v", err)
	}
	sA, idxA := openRepo(t, repoA)

	cl, _ := client.New(repoURL, alice)

	// --- Phase 1: live ---
	// Open a live watch in the background.
	got := &collector{}
	ctx1, cancel1 := context.WithCancel(context.Background())
	streamDone := make(chan struct{})
	go func() {
		defer close(streamDone)
		// single connection (no auto-reconnect) so we can force a disconnect
		_ = cl.WatchStream(ctx1, "/", "", 0, got.add)
	}()
	time.Sleep(100 * time.Millisecond) // let the subscription attach

	commitAndPush(t, sA, idxA, repoA, repoURL, alice, "a.md", "a1\n", "commit-1")

	// Wait for the live event to arrive.
	waitFor(t, func() bool { return got.commitsSeen()["commit-1"] }, 2*time.Second)

	// --- Phase 2: forced disconnect, commits land while disconnected ---
	cancel1()
	<-streamDone

	commitAndPush(t, sA, idxA, repoA, repoURL, alice, "b.md", "b1\n", "commit-2")
	commitAndPush(t, sA, idxA, repoA, repoURL, alice, "c.md", "c1\n", "commit-3")

	// --- Phase 3: reconnect with the last cursor; server catch-up backfills ---
	// The last fully-consumed commit was commit-1 (seq 1), so resume from cursor 1.
	cursor := lastCommitCursor(got, "commit-1")
	ctx2, cancel2 := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel2()
	recovered := &collector{}
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = cl.WatchStream(ctx2, "/", "", cursor, recovered.add)
	}()

	// The catch-up must deliver commit-2 and commit-3 (the gap).
	waitFor(t, func() bool {
		seen := recovered.commitsSeen()
		return seen["commit-2"] && seen["commit-3"]
	}, 3*time.Second)

	// --- Phase 4: resume live ---
	commitAndPush(t, sA, idxA, repoA, repoURL, alice, "d.md", "d1\n", "commit-4")
	waitFor(t, func() bool { return recovered.commitsSeen()["commit-4"] }, 3*time.Second)

	cancel2()
	<-done

	// Completeness: across both streams, no accepted commit was missed.
	all := map[string]bool{}
	for k := range got.commitsSeen() {
		all[k] = true
	}
	for k := range recovered.commitsSeen() {
		all[k] = true
	}
	for _, msg := range []string{"commit-1", "commit-2", "commit-3", "commit-4"} {
		if !all[msg] {
			t.Errorf("missed commit %q across watch + recovery", msg)
		}
	}
}

// TestWatchRevokeMidStream verifies live permission filtering is evaluated against
// CURRENT grants: once bob's read is revoked, he stops receiving events for that
// path on the same live connection (no reconnect needed).
func TestWatchRevokeMidStream(t *testing.T) {
	serverURL := testServer(t)
	repoURL := serverURL + "/revwatch"
	alice := registerUser(t, serverURL, "alice")
	bob := registerUser(t, serverURL, "bob")

	repoA := t.TempDir()
	if err := cli.RunInit(repoA, repoURL, alice); err != nil {
		t.Fatalf("init: %v", err)
	}
	sA, idxA := openRepo(t, repoA)

	aliceCl, _ := client.New(repoURL, alice)
	if err := aliceCl.AddMember("bob"); err != nil {
		t.Fatalf("add bob: %v", err)
	}
	if err := aliceCl.Grant("bob", store.PermRead, "/strategy/*"); err != nil {
		t.Fatalf("grant bob: %v", err)
	}

	// Bob watches /strategy live.
	bobCl, _ := client.New(repoURL, bob)
	got := &collector{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = bobCl.WatchStream(ctx, "/strategy", "", 0, got.add) }()
	time.Sleep(100 * time.Millisecond)

	// First commit while bob can read — he should see it.
	commitAndPush(t, sA, idxA, repoA, repoURL, alice, "strategy/a.md", "a1\n", "before-revoke")
	waitFor(t, func() bool { return got.commitsSeen()["before-revoke"] }, 2*time.Second)

	// Revoke bob's read, then commit again — bob must NOT receive it.
	if err := aliceCl.Revoke("bob", "/strategy/*"); err != nil {
		t.Fatalf("revoke bob: %v", err)
	}
	commitAndPush(t, sA, idxA, repoA, repoURL, alice, "strategy/b.md", "b1\n", "after-revoke")

	// Give the event time to (not) arrive.
	time.Sleep(300 * time.Millisecond)
	if got.commitsSeen()["after-revoke"] {
		t.Error("bob received an event after his read was revoked (stale permission)")
	}
}

// TestWatchEventsFilterResumability verifies that --events filtering out
// commit.pushed does not stall the recovery cursor: RunWatch advances on any
// event, so a reconnect resumes correctly.
func TestWatchEventsFilterResumability(t *testing.T) {
	serverURL := testServer(t)
	repoURL := serverURL + "/evfilter"
	alice := registerUser(t, serverURL, "alice")

	repoA := t.TempDir()
	if err := cli.RunInit(repoA, repoURL, alice); err != nil {
		t.Fatalf("init: %v", err)
	}
	sA, idxA := openRepo(t, repoA)

	// Watch only file.created (commit.pushed is filtered out). Capture the cursor
	// that RunWatch would resume from by observing the events it advances on.
	cl, _ := client.New(repoURL, alice)
	got := &collector{}
	var lastCursor int64
	var mu sync.Mutex
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		_ = cl.WatchStream(ctx, "/", server.EventFileCreated, 0, func(e server.EventJSON) {
			got.add(e)
			mu.Lock()
			if e.Cursor > lastCursor {
				lastCursor = e.Cursor
			}
			mu.Unlock()
		})
	}()
	time.Sleep(100 * time.Millisecond) // let the subscription attach

	// Push after the watch attaches so the file.created is delivered live.
	commitAndPush(t, sA, idxA, repoA, repoURL, alice, "a.md", "a1\n", "c1")

	waitFor(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return lastCursor > 0
	}, 2*time.Second)

	cancel()

	// Only file.created should have been delivered; commit.pushed is filtered out.
	got.mu.Lock()
	for _, e := range got.events {
		if e.Type == server.EventCommitPushed {
			t.Error("commit.pushed should have been filtered out by --events")
		}
	}
	got.mu.Unlock()
	// The cursor advanced from a file.created event even though commit.pushed was
	// filtered — so a reconnect would resume from a real position, not 0.
	mu.Lock()
	defer mu.Unlock()
	if lastCursor == 0 {
		t.Error("cursor did not advance under --events filter that excludes commit.pushed")
	}
}

func lastCommitCursor(c *collector, message string) int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, e := range c.events {
		if e.Type == server.EventCommitPushed && e.Message == message {
			return e.Cursor
		}
	}
	return 0
}

func waitFor(t *testing.T, cond func() bool, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("condition not met within timeout")
}
