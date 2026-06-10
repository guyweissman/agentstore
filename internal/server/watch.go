package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/coder/websocket"

	"github.com/guyweissman/agentstore/internal/store"
)

// handleWatch handles GET /<repo>/watch — the live event stream over WebSocket.
// The handshake is a signed GET (verified by the auth middleware); the connection
// is then trusted for its lifetime.
func (srv *Server) handleWatch(w http.ResponseWriter, r *http.Request) {
	rh, err := srv.repo(r.PathValue("repo"))
	if err != nil {
		writeError(w, http.StatusNotFound, "repo_not_found", err.Error(), nil)
		return
	}
	caller := principalFromContext(r.Context())
	if !srv.requireMember(w, rh, caller) {
		return
	}

	q := r.URL.Query()
	pathPrefix := q.Get("path")
	if pathPrefix == "" {
		pathPrefix = "/"
	}
	var eventTypes map[string]bool
	if e := q.Get("events"); e != "" {
		eventTypes = make(map[string]bool)
		for _, t := range strings.Split(e, ",") {
			eventTypes[strings.TrimSpace(t)] = true
		}
	}
	var cursor int64
	if c := q.Get("cursor"); c != "" {
		cursor, _ = strconv.ParseInt(c, 10, 64)
	}

	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return // Accept already wrote the error
	}
	defer conn.CloseNow()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sub := &subscriber{
		principalID: caller,
		pathPrefix:  pathPrefix,
		eventTypes:  eventTypes,
		out:         make(chan []EventJSON, subscriberBuffer),
	}

	// Best-effort catch-up from the cursor, then attach to the live hub. Any seam
	// between catch-up and live is just a gap the client reconciles via the log.
	if cursor > 0 {
		if err := srv.catchUp(ctx, conn, rh, sub, cursor); err != nil {
			return
		}
	}

	rh.hub.Subscribe(sub)
	defer rh.hub.Unsubscribe(sub)

	// Reader goroutine: detect client disconnect / close frames.
	go func() {
		for {
			if _, _, err := conn.Read(ctx); err != nil {
				cancel()
				return
			}
		}
	}()

	// Writer loop: drain the subscriber's buffer to the socket, commit-atomically
	// (all events of a group, in order, with commit.pushed last).
	for {
		select {
		case group, ok := <-sub.out:
			if !ok {
				return
			}
			if err := writeEvents(ctx, conn, group); err != nil {
				return
			}
		case <-ctx.Done():
			return
		}
	}
}

// catchUp replays commits with seq > cursor (permission-filtered) before the
// connection attaches to the live hub.
func (srv *Server) catchUp(ctx context.Context, conn *websocket.Conn, rh *repoHandle, sub *subscriber, cursor int64) error {
	commits, err := rh.s.LogCommits(store.LogFilter{Cursor: cursor, Reverse: true})
	if err != nil {
		return err
	}
	for _, c := range commits {
		group := rh.hub.filterGroup(sub, buildEventGroupFromCommit(c))
		if len(group) == 0 {
			continue
		}
		if err := writeEvents(ctx, conn, group); err != nil {
			return err
		}
	}
	return nil
}

func writeEvents(ctx context.Context, conn *websocket.Conn, group []EventJSON) error {
	for _, e := range group {
		b, err := json.Marshal(e)
		if err != nil {
			return err
		}
		if err := conn.Write(ctx, websocket.MessageText, b); err != nil {
			return err
		}
	}
	return nil
}

// buildEventGroupFromCommit constructs a commit's event group from a stored commit
// (used for catch-up replay). Mirrors buildEventGroup, which builds from a push.
func buildEventGroupFromCommit(c *store.Commit) []EventJSON {
	group := make([]EventJSON, 0, len(c.Files)+1)
	paths := make([]string, 0, len(c.Files))
	for _, f := range c.Files {
		ev := EventJSON{
			Cursor:    c.Seq,
			Type:      fileEventType(f.ChangeType),
			Timestamp: c.CreatedAt,
			CommitID:  c.ID,
			AuthorID:  c.AuthorID,
			Path:      f.Path,
		}
		if f.ChangeType != "deleted" {
			ev.ObjectHash = f.ObjectHash
			ev.Size = f.Size
		}
		group = append(group, ev)
		paths = append(paths, f.Path)
	}
	group = append(group, EventJSON{
		Cursor:    c.Seq,
		Type:      EventCommitPushed,
		Timestamp: c.CreatedAt,
		CommitID:  c.ID,
		AuthorID:  c.AuthorID,
		Message:   c.Message,
		Paths:     paths,
	})
	return group
}
