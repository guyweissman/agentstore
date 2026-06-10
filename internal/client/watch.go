package client

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"
	"strings"

	"github.com/coder/websocket"

	"github.com/guyweissman/agentstore/internal/server"
)

// WatchStream opens the watch WebSocket and delivers events to onEvent until the
// connection closes or ctx is cancelled. It is a single connection — callers add
// reconnect-with-cursor on top (see RunWatch in the cli package).
func (c *Client) WatchStream(ctx context.Context, pathPrefix, events string, cursor int64, onEvent func(server.EventJSON)) error {
	// Build the request target (path + query) — signed and dialed identically.
	q := url.Values{}
	if pathPrefix != "" {
		q.Set("path", pathPrefix)
	}
	if events != "" {
		q.Set("events", events)
	}
	if cursor > 0 {
		q.Set("cursor", strconv.FormatInt(cursor, 10))
	}
	target := "/" + c.repo + "/watch"
	if enc := q.Encode(); enc != "" {
		target += "?" + enc
	}

	wsURL := toWebSocketScheme(c.baseURL) + target

	opts := &websocket.DialOptions{}
	if c.id != nil {
		opts.HTTPHeader = c.authHeaders("GET", target, nil)
	}

	conn, _, err := websocket.Dial(ctx, wsURL, opts)
	if err != nil {
		return err
	}
	defer conn.CloseNow()
	// Lift the default 32KiB read limit; event messages are small but unbounded counts.
	conn.SetReadLimit(1 << 20)

	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			return err
		}
		var ev server.EventJSON
		if err := json.Unmarshal(data, &ev); err != nil {
			continue // skip malformed frames rather than tearing down the stream
		}
		onEvent(ev)
	}
}

func toWebSocketScheme(httpURL string) string {
	if strings.HasPrefix(httpURL, "https") {
		return "wss" + strings.TrimPrefix(httpURL, "https")
	}
	return "ws" + strings.TrimPrefix(httpURL, "http")
}
