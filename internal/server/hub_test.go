package server

import (
	"testing"
	"time"
)

// allowAll is a canRead that grants read on everything.
func allowAll(string, string) bool { return true }

func drain(t *testing.T, ch chan []EventJSON) []EventJSON {
	t.Helper()
	select {
	case g := <-ch:
		return g
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for event group")
		return nil
	}
}

func TestHubFanOut(t *testing.T) {
	h := newHub(allowAll)
	go h.run()
	defer h.Stop()

	sub := &subscriber{principalID: "p", pathPrefix: "/", out: make(chan []EventJSON, 8)}
	h.Subscribe(sub)

	group := []EventJSON{
		{Cursor: 1, Type: EventFileCreated, Path: "/a.md", CommitID: "c1"},
		{Cursor: 1, Type: EventCommitPushed, CommitID: "c1", Paths: []string{"/a.md"}},
	}
	h.Publish(group)

	got := drain(t, sub.out)
	if len(got) != 2 {
		t.Fatalf("expected 2 events, got %d", len(got))
	}
	if got[1].Type != EventCommitPushed {
		t.Errorf("commit.pushed should be last, got %s", got[1].Type)
	}
}

func TestHubPathFilter(t *testing.T) {
	h := newHub(allowAll)
	go h.run()
	defer h.Stop()

	// Subscriber only watches /strategy.
	sub := &subscriber{principalID: "p", pathPrefix: "/strategy", out: make(chan []EventJSON, 8)}
	h.Subscribe(sub)

	h.Publish([]EventJSON{
		{Cursor: 1, Type: EventFileModified, Path: "/finance/x.md", CommitID: "c1"},
		{Cursor: 1, Type: EventFileModified, Path: "/strategy/y.md", CommitID: "c1"},
		{Cursor: 1, Type: EventCommitPushed, CommitID: "c1", Paths: []string{"/finance/x.md", "/strategy/y.md"}},
	})

	got := drain(t, sub.out)
	// Should see only the /strategy file event + a commit.pushed with paths filtered.
	var fileEvents int
	var commitPaths []string
	for _, e := range got {
		if e.Type == EventCommitPushed {
			commitPaths = e.Paths
		} else {
			fileEvents++
			if e.Path != "/strategy/y.md" {
				t.Errorf("unexpected file event for %s", e.Path)
			}
		}
	}
	if fileEvents != 1 {
		t.Errorf("expected 1 file event under /strategy, got %d", fileEvents)
	}
	if len(commitPaths) != 1 || commitPaths[0] != "/strategy/y.md" {
		t.Errorf("commit.pushed paths should be filtered to /strategy/y.md, got %v", commitPaths)
	}
}

func TestHubPermissionFilter(t *testing.T) {
	// Deny everything: subscriber should receive no events for a commit it can't read.
	h := newHub(func(string, string) bool { return false })
	go h.run()
	defer h.Stop()

	sub := &subscriber{principalID: "p", pathPrefix: "/", out: make(chan []EventJSON, 8)}
	h.Subscribe(sub)

	h.Publish([]EventJSON{
		{Cursor: 1, Type: EventFileModified, Path: "/secret.md", CommitID: "c1"},
		{Cursor: 1, Type: EventCommitPushed, CommitID: "c1", Paths: []string{"/secret.md"}},
	})

	select {
	case g := <-sub.out:
		t.Errorf("subscriber with no read access should get nothing, got %v", g)
	case <-time.After(150 * time.Millisecond):
		// expected: no delivery
	}
}

// TestHubDropDoesNotStarveOthers proves a stuck subscriber's full buffer never
// blocks the hub: a healthy subscriber keeps receiving while the stuck one drops.
func TestHubDropDoesNotStarveOthers(t *testing.T) {
	h := newHub(allowAll)
	go h.run()
	defer h.Stop()

	stuck := &subscriber{principalID: "stuck", pathPrefix: "/", out: make(chan []EventJSON, 1)}
	healthy := &subscriber{principalID: "ok", pathPrefix: "/", out: make(chan []EventJSON, 256)}
	h.Subscribe(stuck)
	h.Subscribe(healthy)

	const n = 100
	for i := 0; i < n; i++ {
		h.Publish([]EventJSON{
			{Cursor: int64(i + 1), Type: EventCommitPushed, CommitID: "c", Paths: []string{"/a.md"}},
		})
	}

	// The healthy subscriber must receive (nearly) all groups — the stuck one's
	// full buffer must not have blocked fan-out. Drain healthy and count.
	received := 0
	deadline := time.After(2 * time.Second)
	for received < n {
		select {
		case <-healthy.out:
			received++
		case <-deadline:
			t.Fatalf("healthy subscriber starved: only received %d/%d while a peer was stuck", received, n)
		}
	}

	// The stuck subscriber's buffer never exceeds capacity (excess dropped).
	if len(stuck.out) > cap(stuck.out) {
		t.Errorf("stuck buffer exceeded capacity: %d > %d", len(stuck.out), cap(stuck.out))
	}
}
