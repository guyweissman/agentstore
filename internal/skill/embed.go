// Package skill embeds the AgentStore agent skill and exports it to disk.
//
// The content under content/ is generated from templates/ and the CLI command
// tree. Regenerate it after changing the CLI or the skill template:
//
//go:generate go run ./gen
package skill

import (
	"embed"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/guyweissman/agentstore/internal/brand"
)

//go:embed content
var content embed.FS

// Meta is the substitution data rendered into the skill templates so the skill
// always reflects the current binary name and directory conventions.
type Meta struct {
	App       string
	StoreDir  string
	GlobalDir string
}

// CurrentMeta returns the substitution data for the active brand.
func CurrentMeta() Meta {
	return Meta{
		App:       brand.AppName,
		StoreDir:  brand.StoreDir,
		GlobalDir: brand.GlobalDirName,
	}
}

// Export writes the embedded skill (SKILL.md plus reference/) into dir,
// creating dir if needed. The output is harness-neutral markdown: any agent
// runtime can point at it.
func Export(dir string) error {
	root, err := fs.Sub(content, "content")
	if err != nil {
		return err
	}
	return fs.WalkDir(root, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		dest := filepath.Join(dir, filepath.FromSlash(path))
		if d.IsDir() {
			return os.MkdirAll(dest, 0o755)
		}
		data, err := fs.ReadFile(root, path)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return err
		}
		return os.WriteFile(dest, data, 0o644)
	})
}

// SkillMarkdown returns the rendered SKILL.md, for printing to stdout.
func SkillMarkdown() ([]byte, error) {
	return content.ReadFile("content/SKILL.md")
}
