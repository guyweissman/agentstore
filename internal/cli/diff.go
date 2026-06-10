package cli

import (
	"fmt"
	"os"
	"sort"

	"github.com/pmezard/go-difflib/difflib"
	"github.com/spf13/cobra"

	"github.com/guyweissman/agentstore/internal/index"
	"github.com/guyweissman/agentstore/internal/store"
	"github.com/guyweissman/agentstore/internal/workspace"
)

func newDiffCmd() *cobra.Command {
	var staged bool
	cmd := &cobra.Command{
		Use:   "diff [<path>]",
		Short: "Show unstaged diffs (or --staged for staged diffs)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cwd, err := os.Getwd()
			if err != nil {
				return err
			}
			root, err := store.FindRoot(cwd)
			if err != nil {
				return err
			}
			s, err := store.Open(root)
			if err != nil {
				return err
			}
			defer s.Close()
			idx, err := index.Open(s.Dir())
			if err != nil {
				return err
			}
			defer idx.Close()
			var filterPath string
			if len(args) == 1 {
				sp, err := workspace.StorePath(root, args[0])
				if err != nil {
					return err
				}
				filterPath = sp
			}
			return RunDiff(os.Stdout, s, idx, root, staged, filterPath)
		},
	}
	cmd.Flags().BoolVar(&staged, "staged", false, "diff staged changes against HEAD")
	return cmd
}

// RunDiff writes a unified diff to w.
// If stagedMode is true, compares staged vs HEAD. Otherwise compares working tree vs HEAD.
func RunDiff(w interface{ WriteString(string) (int, error) }, s *store.Store, idx *index.Index, repoRoot string, stagedMode bool, filterPath string) error {
	staged, err := idx.Entries()
	if err != nil {
		return err
	}

	if stagedMode {
		return diffStaged(w, s, staged, filterPath)
	}
	return diffWorkingTree(w, s, idx, repoRoot, staged, filterPath)
}

func diffStaged(w interface{ WriteString(string) (int, error) }, s *store.Store, staged []index.StagedEntry, filterPath string) error {
	for _, e := range staged {
		if filterPath != "" && e.Path != filterPath {
			continue
		}
		var before, after string

		head, err := s.FileHead(e.Path)
		if err != nil {
			return err
		}
		if head != nil && head.ObjectHash != "" {
			data, err := s.Objects.ReadObject(head.ObjectHash)
			if err != nil {
				return err
			}
			before = string(data)
		}

		if e.ObjectHash != "" {
			data, err := s.Objects.ReadObject(e.ObjectHash)
			if err != nil {
				return err
			}
			after = string(data)
		}

		if err := writeDiff(w, e.Path, before, after); err != nil {
			return err
		}
	}
	return nil
}

func diffWorkingTree(w interface{ WriteString(string) (int, error) }, s *store.Store, idx *index.Index, repoRoot string, staged []index.StagedEntry, filterPath string) error {
	// Collect paths to diff: HEAD files + staged new files.
	paths := make(map[string]struct{})
	headPaths, err := s.ListHeadPaths()
	if err != nil {
		return err
	}
	for _, p := range headPaths {
		paths[p] = struct{}{}
	}
	for _, e := range staged {
		paths[e.Path] = struct{}{}
	}

	sorted := make([]string, 0, len(paths))
	for sp := range paths {
		sorted = append(sorted, sp)
	}
	sort.Strings(sorted)

	for _, sp := range sorted {
		if filterPath != "" && sp != filterPath {
			continue
		}
		// "before" is the staged version if staged, else HEAD.
		var before string
		if se, err := idx.Get(sp); err == nil && se != nil && se.ObjectHash != "" {
			data, err := s.Objects.ReadObject(se.ObjectHash)
			if err != nil {
				return err
			}
			before = string(data)
		} else {
			head, err := s.FileHead(sp)
			if err != nil {
				return err
			}
			if head != nil && head.ObjectHash != "" {
				data, err := s.Objects.ReadObject(head.ObjectHash)
				if err != nil {
					return err
				}
				before = string(data)
			}
		}

		// "after" is the working tree.
		hostPath := workspace.HostPath(repoRoot, sp)
		data, err := os.ReadFile(hostPath)
		if os.IsNotExist(err) {
			data = nil
		} else if err != nil {
			return err
		}
		after := string(data)

		if before == after {
			continue
		}
		if err := writeDiff(w, sp, before, after); err != nil {
			return err
		}
	}
	return nil
}

func writeDiff(w interface{ WriteString(string) (int, error) }, path, before, after string) error {
	diff := difflib.UnifiedDiff{
		A:        difflib.SplitLines(before),
		B:        difflib.SplitLines(after),
		FromFile: "a" + path,
		ToFile:   "b" + path,
		Context:  3,
	}
	text, err := difflib.GetUnifiedDiffString(diff)
	if err != nil {
		return fmt.Errorf("diff %s: %w", path, err)
	}
	if text == "" {
		return nil
	}
	_, err = w.WriteString(text)
	return err
}
