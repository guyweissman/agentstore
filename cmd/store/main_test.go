package main_test

import (
	"io"
	"testing"

	"github.com/guyweissman/agentstore/internal/cli"
)

// TestHelpRuns is the smoke test: the root command builds and --help exits cleanly.
func TestHelpRuns(t *testing.T) {
	cmd := cli.Root()
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"--help"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("--help returned error: %v", err)
	}
}
