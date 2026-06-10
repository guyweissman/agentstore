package cli_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/guyweissman/agentstore/internal/cli"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// manifestPath is the coverage-demo manifest, relative to this package dir.
const manifestPath = "../../coverage-demo/manifest.json"

// A coverage unit is either a runnable leaf command (a "bare" unit) or a single
// non-help flag on it. The id is the command path without the program name
// (e.g. "server start"), plus " --flag" for a flag unit. This file is the drift
// guard: the editorial unit list below must exactly match the real CLI surface
// derived from cli.Root(), and the committed manifest.json must match what this
// list generates. Add a flag to the CLI without updating prdOrder -> red build.

type manifestEntry struct {
	ID      string  `json:"id"`
	Group   string  `json:"group"`
	Order   int     `json:"order"`
	Command string  `json:"command"`
	Flag    *string `json:"flag"`
	Display string  `json:"display"`
	// Positional-arg arity, present only on bare command units (not flag units).
	// ArgsMax of -1 means unbounded. Derived from the real cobra Args validator,
	// so a change to a command's arity makes the committed manifest stale.
	ArgsMin *int `json:"args_min,omitempty"`
	ArgsMax *int `json:"args_max,omitempty"`
}

// cobra built-in commands that are not part of the product surface.
var skipCommands = map[string]bool{"help": true, "completion": true}

// deriveUnits walks the cobra tree and returns the set of coverage unit ids.
func deriveUnits() map[string]bool {
	units := map[string]bool{}
	root := cli.Root()
	rootName := root.Name()

	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		for _, sub := range c.Commands() {
			if skipCommands[sub.Name()] {
				continue
			}
			walk(sub)
		}
		if !c.Runnable() {
			return
		}
		id := strings.TrimPrefix(c.CommandPath(), rootName+" ")
		if id == rootName || id == "" {
			return // the root command itself
		}
		units[id] = true
		c.Flags().VisitAll(func(f *pflag.Flag) {
			if f.Name == "help" {
				return
			}
			units[id+" --"+f.Name] = true
		})
	}
	walk(root)
	return units
}

// commandArity probes a command's real Args validator to find the accepted
// positional-arg count range. max == -1 means unbounded.
func commandArity(c *cobra.Command) (min, max int) {
	const probe = 6
	if c.Args == nil {
		return 0, -1 // cobra's default is arbitrary args
	}
	min = -1
	for n := 0; n <= probe; n++ {
		if c.Args(c, make([]string, n)) == nil {
			if min == -1 {
				min = n
			}
			max = n
		}
	}
	if min == -1 {
		min = 0
	}
	if max == probe {
		max = -1 // accepted up to the probe ceiling — treat as unbounded
	}
	return min, max
}

// deriveArity returns the positional-arg arity of every runnable command, keyed
// by the same id deriveUnits uses for bare command units.
func deriveArity() map[string][2]int {
	arity := map[string][2]int{}
	root := cli.Root()
	rootName := root.Name()

	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		for _, sub := range c.Commands() {
			if skipCommands[sub.Name()] {
				continue
			}
			walk(sub)
		}
		if !c.Runnable() {
			return
		}
		id := strings.TrimPrefix(c.CommandPath(), rootName+" ")
		if id == rootName || id == "" {
			return
		}
		min, max := commandArity(c)
		arity[id] = [2]int{min, max}
	}
	walk(root)
	return arity
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// TestDumpUnits prints the cobra-derived unit set when COVERAGE_DUMP=1.
// Used to bootstrap/inspect the editorial prdOrder list. Always passes.
func TestDumpUnits(t *testing.T) {
	if os.Getenv("COVERAGE_DUMP") == "" {
		t.Skip("set COVERAGE_DUMP=1 to print the derived unit set")
	}
	for _, id := range sortedKeys(deriveUnits()) {
		t.Logf("UNIT %q", id)
	}
}

func TestCoverageManifestMatchesCLI(t *testing.T) {
	derived := deriveUnits()

	// (a) drift guard: editorial list must equal the real CLI surface.
	editorial := map[string]bool{}
	for _, id := range prdOrder {
		editorial[id] = true
	}
	var missing, extra []string
	for id := range derived {
		if !editorial[id] {
			missing = append(missing, id)
		}
	}
	for id := range editorial {
		if !derived[id] {
			extra = append(extra, id)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	if len(missing) > 0 {
		t.Errorf("CLI exposes units missing from coverage list (add to prdOrder): %v", missing)
	}
	if len(extra) > 0 {
		t.Errorf("coverage list has units the CLI no longer exposes (remove from prdOrder): %v", extra)
	}

	// (b) golden guard: committed manifest.json must equal generated output.
	generated := buildManifest()
	if os.Getenv("UPDATE_MANIFEST") != "" {
		writeManifest(t, generated)
		t.Log("wrote", manifestPath)
		return
	}
	want, err := json.MarshalIndent(generated, "", "  ")
	if err != nil {
		t.Fatalf("marshal generated: %v", err)
	}
	got, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("read %s: %v (run with UPDATE_MANIFEST=1 to create)", manifestPath, err)
	}
	if strings.TrimSpace(string(got)) != strings.TrimSpace(string(want)) {
		t.Errorf("%s is stale; run: UPDATE_MANIFEST=1 go test ./internal/cli -run Manifest", manifestPath)
	}
}

// buildManifest turns the editorial prdOrder + group map into manifest entries.
func buildManifest() []manifestEntry {
	arity := deriveArity()
	out := make([]manifestEntry, 0, len(prdOrder))
	for i, id := range prdOrder {
		cmd, flag := splitUnit(id)
		var flagPtr *string
		var minPtr, maxPtr *int
		if flag != "" {
			f := flag
			flagPtr = &f
		} else if a, ok := arity[cmd]; ok {
			mn, mx := a[0], a[1]
			minPtr, maxPtr = &mn, &mx
		}
		out = append(out, manifestEntry{
			ID:      id,
			Group:   groupFor(id, cmd),
			Order:   i,
			Command: cmd,
			Flag:    flagPtr,
			Display: "store " + id,
			ArgsMin: minPtr,
			ArgsMax: maxPtr,
		})
	}
	return out
}

func writeManifest(t *testing.T, entries []manifestEntry) {
	t.Helper()
	b, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(manifestPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(manifestPath, append(b, '\n'), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

// splitUnit splits a unit id into its command path and flag ("" if a bare command).
func splitUnit(id string) (cmd, flag string) {
	if i := strings.Index(id, " --"); i >= 0 {
		return id[:i], strings.TrimPrefix(id[i+1:], " ")
	}
	return id, ""
}

func groupFor(id, cmd string) string {
	if g, ok := groupOverride[id]; ok {
		return g
	}
	if g, ok := commandGroup[cmd]; ok {
		return g
	}
	return "Other"
}
