package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/guyweissman/agentstore/internal/client"
	"github.com/guyweissman/agentstore/internal/server"
)

func newWatchCmd() *cobra.Command {
	var (
		events string
		cursor int64
	)
	cmd := &cobra.Command{
		Use:   "watch [<path>]",
		Short: "Stream live events under a path (JSON)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			pathPrefix, err := watchPathPrefix(args)
			if err != nil {
				return err
			}
			root, err := repoRootFromCwd()
			if err != nil {
				return err
			}
			originURL, err := originURL(root)
			if err != nil {
				return err
			}
			id, err := loadIdentity(originURL)
			if err != nil {
				return err
			}
			cl, err := client.New(originURL, id)
			if err != nil {
				return err
			}

			// Stream to stdout until interrupted.
			ctx := cmd.Context()
			return RunWatch(ctx, cl, pathPrefix, events, cursor, func(e server.EventJSON) {
				b, _ := json.Marshal(e)
				fmt.Fprintln(os.Stdout, string(b))
			})
		},
	}
	cmd.Flags().StringVar(&events, "events", "", "comma-separated event types to filter (default: all)")
	cmd.Flags().Int64Var(&cursor, "cursor", 0, "resume from this seq cursor")
	return cmd
}

// watchPathPrefix validates the optional watch path argument and returns the
// store-path prefix to subscribe to. Watch paths are repo paths — the same
// /-rooted namespace that grants use — not working-tree paths, so they must be
// /-rooted. With no argument the prefix defaults to the repo root ("/").
func watchPathPrefix(args []string) (string, error) {
	if len(args) == 0 {
		return "/", nil
	}
	if !strings.HasPrefix(args[0], "/") {
		return "", errors.New(`watch paths are repo paths and must start with "/" (e.g. /strategy)`)
	}
	return args[0], nil
}

// RunWatch streams events to onEvent, reconnecting with the last cursor after a
// disconnect. On each (re)connect the server replays missed commits from the
// durable log (catch-up), so no accepted commit is missed — the live stream is
// the fast path, the seq cursor is the recovery anchor.
func RunWatch(ctx context.Context, cl *client.Client, pathPrefix, events string, cursor int64, onEvent func(server.EventJSON)) error {
	for {
		if ctx.Err() != nil {
			return nil
		}
		err := cl.WatchStream(ctx, pathPrefix, events, cursor, func(e server.EventJSON) {
			onEvent(e)
			// Advance the recovery cursor on every event, not just commit.pushed:
			// all events of a commit share the same seq, and --events may filter
			// commit.pushed out entirely, which would otherwise stall the cursor.
			if e.Cursor > cursor {
				cursor = e.Cursor
			}
		})
		if ctx.Err() != nil || err == io.EOF {
			return nil
		}
		// Disconnected for another reason: pause briefly, then reconnect with the
		// last cursor (server catch-up backfills the gap).
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(200 * time.Millisecond):
		}
	}
}
