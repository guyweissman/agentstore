// Package skillgen renders the embedded skill content (SKILL.md + reference/)
// from templates/ and the live CLI command tree. It is imported by both the
// generator (internal/skill/gen) and the drift test that keeps content/ in sync
// with its sources. It lives outside package skill on purpose: package skill is
// imported by internal/cli, so a renderer that needs the cobra tree can't live
// there without an import cycle.
package skillgen

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/template"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/guyweissman/agentstore/internal/skill"
)

// Files renders the skill content from tmplDir and the command tree, keyed by
// content-relative path (e.g. "SKILL.md", "reference/cli.md"). Callers pass
// cli.Root() so this package needn't import internal/cli.
func Files(root *cobra.Command, meta skill.Meta, tmplDir string) (map[string][]byte, error) {
	md, err := renderTemplate(filepath.Join(tmplDir, "SKILL.md.tmpl"), meta)
	if err != nil {
		return nil, err
	}
	return map[string][]byte{
		"SKILL.md":         md,
		"reference/cli.md": []byte(buildCLIReference(root, meta)),
	}, nil
}

func renderTemplate(src string, meta skill.Meta) ([]byte, error) {
	raw, err := os.ReadFile(src)
	if err != nil {
		return nil, err
	}
	t, err := template.New(filepath.Base(src)).Option("missingkey=error").Parse(string(raw))
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, meta); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func buildCLIReference(root *cobra.Command, meta skill.Meta) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s CLI reference\n\n", meta.App)
	b.WriteString("Generated from the command tree — do not edit by hand. " +
		"Run `" + meta.App + " <command> --help` for the live version.\n\n")
	writeCommand(&b, root)
	return b.String()
}

func writeCommand(b *strings.Builder, c *cobra.Command) {
	if c.Hidden {
		return
	}
	if c.Runnable() || !c.HasParent() {
		fmt.Fprintf(b, "## `%s`\n\n", c.UseLine())
		if c.Short != "" {
			fmt.Fprintf(b, "%s\n\n", c.Short)
		}
		writeFlags(b, c.LocalFlags())
	}

	subs := append([]*cobra.Command(nil), c.Commands()...)
	sort.Slice(subs, func(i, j int) bool { return subs[i].Name() < subs[j].Name() })
	for _, sub := range subs {
		writeCommand(b, sub)
	}
}

func writeFlags(b *strings.Builder, fs *pflag.FlagSet) {
	var lines []string
	fs.VisitAll(func(f *pflag.Flag) {
		if f.Hidden {
			return
		}
		name := "--" + f.Name
		if f.Shorthand != "" {
			name = "-" + f.Shorthand + ", " + name
		}
		lines = append(lines, fmt.Sprintf("- `%s` — %s", name, f.Usage))
	})
	if len(lines) == 0 {
		return
	}
	b.WriteString("Flags:\n\n")
	for _, l := range lines {
		b.WriteString(l + "\n")
	}
	b.WriteString("\n")
}
