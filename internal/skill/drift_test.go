package skill_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/guyweissman/agentstore/internal/cli"
	"github.com/guyweissman/agentstore/internal/skill"
	"github.com/guyweissman/agentstore/internal/skill/skillgen"
)

// TestContentMatchesGenerator guards the embedded skill against drift. content/
// is generated from templates/SKILL.md.tmpl and the live cobra tree (see
// internal/skill/gen), but nothing in the build runs that generator — so this
// test is what makes `go test ./...` fail when a CLI command or the template
// changes without a corresponding `go generate ./internal/skill`. It compares
// the generator's output against the files on disk in content/, not against git.
func TestContentMatchesGenerator(t *testing.T) {
	want, err := skillgen.Files(cli.Root(), skill.CurrentMeta(), "templates")
	if err != nil {
		t.Fatalf("render skill content: %v", err)
	}
	for rel, fresh := range want {
		onDisk, err := os.ReadFile(filepath.Join("content", filepath.FromSlash(rel)))
		if err != nil {
			t.Fatalf("read content/%s: %v", rel, err)
		}
		if !bytes.Equal(onDisk, fresh) {
			t.Errorf("content/%s is stale — run `go generate ./internal/skill`", rel)
		}
	}
}
