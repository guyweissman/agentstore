package cli_test

import (
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/guyweissman/agentstore/internal/cli"
	"github.com/spf13/cobra"
)

// Gap B: bind the prose docs to the real CLI surface. The manifest drift guard
// (manifest_test.go) keeps the editorial unit list in sync with cobra, but
// nothing tied the README/PRD command listings to it — so docs could (and did:
// `watch --since`) advertise commands and flags that don't exist. These tests
// parse the doc CLI-reference blocks, resolve each line against cli.Root(), and
// assert the documented surface matches the manifest.

const (
	readmePath  = "../../README.md"
	prdPath     = "../../project_spec/agentstore-prd.md"
	readmeRef   = "## CLI reference"
	prdRef      = "### Full CLI command set"
	bracketTrim = "[]{}\"'`,"
)

func TestREADMEMatchesCLI(t *testing.T) {
	doc := docUnits(t, readmePath, readmeRef)
	cliUnits := deriveUnits()
	// The README is allowed to be a subset of the full surface, but it must not
	// document anything the CLI does not expose.
	var phantom []string
	for u := range doc {
		if !cliUnits[u] {
			phantom = append(phantom, u)
		}
	}
	sort.Strings(phantom)
	if len(phantom) > 0 {
		t.Errorf("README documents commands/flags the CLI does not expose: %v", phantom)
	}
}

func TestPRDMatchesCLI(t *testing.T) {
	doc := docUnits(t, prdPath, prdRef)
	cliUnits := deriveUnits()
	// The PRD must document every command and must not reference anything the CLI
	// doesn't expose. Flag-completeness is delegated to the manifest drift guard
	// (which ties every CLI flag to the editorial list); the PRD documents many
	// flags in prose/comments a signature parser can't extract, so flag presence
	// is not required here — but a documented flag must still be real.
	var missing, extra []string
	for u := range cliUnits {
		if strings.Contains(u, " --") {
			continue // a flag unit; coverage of flags is the drift guard's job
		}
		if !doc[u] {
			missing = append(missing, u)
		}
	}
	for u := range doc {
		if !cliUnits[u] {
			extra = append(extra, u)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	if len(missing) > 0 {
		t.Errorf("CLI exposes commands the PRD does not document: %v", missing)
	}
	if len(extra) > 0 {
		t.Errorf("PRD documents commands/flags the CLI does not expose: %v", extra)
	}
}

// TestDocArityMatchesCLI checks that flag-free signature lines in the docs imply
// the same positional-arg arity as the command's real validator. This is the
// `init <url> [<directory>]`-class guard: a doc that adds or drops a required or
// optional positional relative to the binary goes red. It compares counts, not
// placeholder names (so `<commit>` vs `<commit_id>` is fine). Lines carrying
// flags are skipped (their positional layout is mode-dependent), and only the
// first signature line per command is checked.
func TestDocArityMatchesCLI(t *testing.T) {
	root := cli.Root()
	arity := deriveArity()
	for _, src := range []struct{ path, ref string }{{readmePath, readmeRef}, {prdPath, prdRef}} {
		seen := map[string]bool{}
		for _, line := range refLines(t, src.path, src.ref) {
			fields := strings.Fields(line)
			cmd, rest := matchCommand(root, fields[1:])
			if cmd == nil || hasFlagToken(rest) {
				continue
			}
			id := unitID(root, cmd)
			if seen[id] {
				continue
			}
			seen[id] = true
			a, ok := arity[id]
			if !ok {
				continue
			}
			mn, mx := a[0], a[1]
			dmin, dmax := docArity(rest)
			// Unbounded commands routinely show one representative arg in docs, so
			// only the required count is meaningful there.
			if mx == -1 {
				if dmin != mn {
					t.Errorf("%s: `store %s` documents %d required arg(s), but the command requires %d", src.path, id, dmin, mn)
				}
				continue
			}
			if dmin != mn || dmax != mx {
				t.Errorf("%s: `store %s` documents %d-%d positional args, but the command takes %d-%d", src.path, id, dmin, dmax, mn, mx)
			}
		}
	}
}

// docArity derives (min, max) positional-arg counts from a flag-free doc
// signature: `<x>` is required, `[<x>]` optional, `...` makes it unbounded.
func docArity(tokens []string) (min, max int) {
	variadic := false
	opt := 0
	for _, tok := range positionalTokens(tokens) {
		if strings.Contains(tok, "...") {
			variadic = true
		}
		if strings.HasPrefix(tok, "[") {
			opt++
		} else {
			min++
		}
	}
	if variadic {
		return min, -1
	}
	return min, min + opt
}

// docUnits returns the coverage unit ids (command + " --flag") documented in the
// named reference section, resolved against the real cobra tree.
func docUnits(t *testing.T, path, heading string) map[string]bool {
	t.Helper()
	root := cli.Root()
	units := map[string]bool{}
	for _, line := range refLines(t, path, heading) {
		fields := strings.Fields(line)
		cmd, rest := matchCommand(root, fields[1:])
		if cmd == nil {
			continue
		}
		id := unitID(root, cmd)
		units[id] = true
		for _, tok := range rest {
			name := docFlagName(cmd, tok)
			if name != "" {
				units[id+" --"+name] = true
			}
		}
	}
	return units
}

// refLines returns the `store ...` command lines inside a doc's reference section.
// It tracks code-fence state so that `#` comment-continuation lines inside a
// fenced block are not mistaken for markdown headings (which would end the
// section early).
func refLines(t *testing.T, path, heading string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	prog := cli.Root().Name() + " "
	level := strings.Count(strings.Fields(heading)[0], "#")
	var out []string
	inSection, inFence := false, false
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			inFence = !inFence
			continue
		}
		if strings.TrimSpace(line) == heading {
			inSection = true
			continue
		}
		if !inSection {
			continue
		}
		if !inFence && isHeadingAtMost(line, level) {
			break
		}
		if strings.HasPrefix(line, prog) {
			if i := strings.Index(line, "#"); i >= 0 {
				line = line[:i]
			}
			out = append(out, strings.TrimSpace(line))
		}
	}
	return out
}

func isHeadingAtMost(line string, level int) bool {
	t := strings.TrimSpace(line)
	if !strings.HasPrefix(t, "#") {
		return false
	}
	n := len(t) - len(strings.TrimLeft(t, "#"))
	return n <= level && strings.HasPrefix(t, strings.Repeat("#", n)+" ")
}

// matchCommand descends the cobra tree consuming leading subcommand tokens and
// returns the deepest matched command plus the unconsumed (arg/flag) tokens.
func matchCommand(root *cobra.Command, fields []string) (*cobra.Command, []string) {
	cur := root
	i := 0
	for i < len(fields) {
		tok := strings.Trim(fields[i], bracketTrim)
		var next *cobra.Command
		for _, sub := range cur.Commands() {
			if sub.Name() == tok {
				next = sub
				break
			}
		}
		if next == nil {
			break
		}
		cur, i = next, i+1
	}
	if cur == root {
		return nil, fields
	}
	return cur, fields[i:]
}

func unitID(root, cmd *cobra.Command) string {
	return strings.TrimPrefix(cmd.CommandPath(), root.Name()+" ")
}

// docFlagName returns the canonical long flag name a doc token refers to, or ""
// if the token is not a flag. Unknown flags resolve to their documented name so
// the unit-set comparison flags them as phantom.
func docFlagName(cmd *cobra.Command, tok string) string {
	tok = strings.Trim(tok, bracketTrim)
	switch {
	case strings.HasPrefix(tok, "--"):
		name := strings.SplitN(tok[2:], "=", 2)[0]
		if f := cmd.Flags().Lookup(name); f != nil {
			return f.Name
		}
		return name
	case strings.HasPrefix(tok, "-") && len(tok) > 1:
		if f := cmd.Flags().ShorthandLookup(string(tok[1])); f != nil {
			return f.Name
		}
		return tok[1:]
	}
	return ""
}

func hasFlagToken(fields []string) bool {
	for _, f := range fields {
		if strings.HasPrefix(strings.Trim(f, bracketTrim), "-") {
			return true
		}
	}
	return false
}

// positionalTokens returns the non-flag tokens of a usage signature, normalized
// for comparison (surrounding whitespace only; brackets are significant).
func positionalTokens(fields []string) []string {
	var out []string
	for _, f := range fields {
		if f == "" || strings.HasPrefix(strings.Trim(f, bracketTrim), "-") {
			continue
		}
		out = append(out, f)
	}
	return out
}
