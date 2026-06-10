// Package log provides the application logger and the structured audit-line helper.
package log

import (
	"log/slog"
	"os"
	"time"
)

// Default is the application-wide logger (text format, stderr, Info level).
var Default = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

// Audit emits a structured JSON line recording an identity or access control action.
// These lines are the sole record of permission history in v0.1; they ride in
// the server's general application log and are not a queryable store.
func Audit(actor, action, target string) {
	// Write a dedicated JSON line so it can be grepped and promoted to a table later.
	// Using a separate handler rather than Default to guarantee JSON format regardless
	// of how Default is configured.
	h := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})
	l := slog.New(h)
	l.Info("audit",
		slog.String("actor", actor),
		slog.String("action", action),
		slog.String("target", target),
		slog.Int64("ts", time.Now().UnixMilli()),
	)
}
