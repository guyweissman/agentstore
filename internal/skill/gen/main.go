// Command gen renders the embedded skill content from templates and the live
// CLI command tree. Run via `go generate ./internal/skill`. The actual
// rendering lives in internal/skill/skillgen, which the drift test shares.
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/guyweissman/agentstore/internal/cli"
	"github.com/guyweissman/agentstore/internal/skill"
	"github.com/guyweissman/agentstore/internal/skill/skillgen"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "skill gen:", err)
		os.Exit(1)
	}
}

func run() error {
	// gen runs from internal/skill (go:generate dir is the file's dir).
	files, err := skillgen.Files(cli.Root(), skill.CurrentMeta(), "templates")
	if err != nil {
		return err
	}
	for rel, data := range files {
		dst := filepath.Join("content", filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(dst, data, 0o644); err != nil {
			return err
		}
	}
	return nil
}
