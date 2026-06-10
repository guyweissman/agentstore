package server_test

import (
	"context"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/guyweissman/agentstore/internal/cli"
	"github.com/guyweissman/agentstore/internal/client"
	"github.com/guyweissman/agentstore/internal/server"
)

// realServer starts the server on its actual listen path — Server.Serve over a
// real TCP socket — rather than httptest's in-process loopback handler. It binds
// 0.0.0.0:0 so the non-loopback branch of Serve runs, and returns a base URL on
// 127.0.0.1 that reaches the same socket.
func realServer(t *testing.T) string {
	t.Helper()
	srv, err := server.New(t.TempDir())
	if err != nil {
		t.Fatalf("server.New: %v", err)
	}
	ln, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	go func() {
		if err := srv.Serve(ln); err != nil {
			t.Errorf("serve: %v", err) // nil on graceful shutdown; anything else is a real failure
		}
	}()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(ctx)
	})
	_, port, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		t.Fatalf("split host port: %v", err)
	}
	return "http://127.0.0.1:" + port
}

// TestServe_NonLoopback_RoundTrip exercises the real listen path that the
// in-process httptest tests skip: a genuine TCP bind on a non-loopback address,
// signed HTTP requests (register/init/push), and — the part with no other
// coverage — the watch WebSocket upgrade, signed handshake, and event framing
// over a real socket.
//
// Two commits are pushed so the watched one has a prior cursor; the watch then
// attaches with cursor > 0 and receives the event via deterministic catch-up
// replay, with no subscribe-before-push race.
func TestServe_NonLoopback_RoundTrip(t *testing.T) {
	serverURL := realServer(t)
	repoURL := serverURL + "/team"
	alice := registerUser(t, serverURL, "alice")

	repoDir := t.TempDir()
	if err := cli.RunInit(repoDir, repoURL, alice); err != nil {
		t.Fatalf("init: %v", err)
	}
	s, idx := openRepo(t, repoDir)

	pushFile := func(rel, content string) {
		t.Helper()
		abs := filepath.Join(repoDir, rel)
		writeFile(t, abs, content)
		if err := cli.RunAdd(s, idx, repoDir, []string{abs}); err != nil {
			t.Fatalf("add %s: %v", rel, err)
		}
		if _, err := cli.RunCommit(s, idx, "add "+rel); err != nil {
			t.Fatalf("commit %s: %v", rel, err)
		}
		if res, err := cli.RunPush(s, repoURL, alice); err != nil {
			t.Fatalf("push %s: %v", rel, err)
		} else if len(res.Conflicts) > 0 {
			t.Fatalf("push %s rejected: %v", rel, res.Conflicts)
		}
	}

	cl, err := client.New(repoURL, alice)
	if err != nil {
		t.Fatalf("client.New: %v", err)
	}

	// First push establishes a cursor; the second is the commit we replay.
	pushFile("strategy/icp.md", "# ICP\n")
	commits, err := cl.GetCommits(0)
	if err != nil {
		t.Fatalf("get commits: %v", err)
	}
	var baseCursor int64
	for _, c := range commits {
		if c.Seq > baseCursor {
			baseCursor = c.Seq
		}
	}
	if baseCursor == 0 {
		t.Fatal("no commits on server after first push")
	}

	const watched = "strategy/plan.md"
	pushFile(watched, "# Plan\n")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	events := make(chan server.EventJSON, 8)
	go func() {
		_ = cl.WatchStream(ctx, "/", "", baseCursor, func(ev server.EventJSON) {
			select {
			case events <- ev:
			case <-ctx.Done():
			}
		})
	}()

	wantPath := "/" + watched
	deadline := time.After(8 * time.Second)
	for {
		select {
		case ev := <-events:
			if ev.Type == server.EventFileCreated && ev.Path == wantPath {
				return // the real-socket watch WebSocket delivered the event
			}
		case <-deadline:
			t.Fatalf("did not receive %s for %s over the watch WebSocket", server.EventFileCreated, wantPath)
		}
	}
}
