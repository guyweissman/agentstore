package cli

import (
	"fmt"
	"os"
	"sort"

	"github.com/spf13/cobra"

	"github.com/guyweissman/agentstore/internal/index"
	"github.com/guyweissman/agentstore/internal/store"
	"github.com/guyweissman/agentstore/internal/workspace"
)

func newStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show staged and unstaged changes",
		Args:  cobra.NoArgs,
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
			return RunStatus(os.Stdout, s, idx, root)
		},
	}
}

// RunStatus writes a git-style status report to w.
func RunStatus(w interface{ Write([]byte) (int, error) }, s *store.Store, idx *index.Index, repoRoot string) error {
	out := func(format string, args ...any) {
		fmt.Fprintf(w, format+"\n", args...)
	}

	staged, err := idx.Entries()
	if err != nil {
		return err
	}
	stagedByPath := make(map[string]index.StagedEntry, len(staged))
	for _, e := range staged {
		stagedByPath[e.Path] = e
	}

	// HEAD paths for unstaged-change detection.
	headPaths, err := s.ListHeadPaths()
	if err != nil {
		return err
	}
	headSet := make(map[string]struct{}, len(headPaths))
	for _, p := range headPaths {
		headSet[p] = struct{}{}
	}

	// Working-tree paths for untracked detection.
	wtPaths, err := workspace.WalkRepo(repoRoot)
	if err != nil {
		return err
	}
	wtSet := make(map[string]struct{}, len(wtPaths))
	for _, p := range wtPaths {
		wtSet[p] = struct{}{}
	}

	// --- Staged section ---
	if len(staged) > 0 {
		out("Changes to be committed:")
		for _, e := range staged {
			label := e.ChangeType
			if label == "modified" {
				label = "modified:  "
			} else if label == "added" {
				label = "new file:  "
			} else {
				label = "deleted:   "
			}
			out("\t%s %s", label, e.Path)
		}
		out("")
	}

	// --- Unstaged section ---
	// Check HEAD paths and staged-new paths for working-tree divergence.
	// Both categories can have unstaged changes: a HEAD file may be modified
	// on disk, and a staged-new file may have been edited after staging.
	unstaged := checkUnstaged(s, idx, repoRoot, headPaths, stagedByPath, wtSet)
	sort.Strings(unstaged)
	if len(unstaged) > 0 {
		out("Changes not staged for commit:")
		for _, line := range unstaged {
			out("\t%s", line)
		}
		out("")
	}

	// --- Untracked section ---
	var untracked []string
	for _, wtp := range wtPaths {
		if _, inHead := headSet[wtp]; inHead {
			continue
		}
		if _, inStaged := stagedByPath[wtp]; inStaged {
			continue
		}
		untracked = append(untracked, wtp)
	}
	if len(untracked) > 0 {
		out("Untracked files:")
		for _, p := range untracked {
			out("\t%s", p)
		}
		out("")
	}

	if len(staged) == 0 && len(unstaged) == 0 && len(untracked) == 0 {
		out("nothing to commit, working tree clean")
	}
	return nil
}

// checkUnstaged returns lines describing working-tree changes that have not been staged.
// It checks both HEAD paths and staged-new paths (a staged-new file can be edited after staging).
func checkUnstaged(s *store.Store, idx *index.Index, repoRoot string, headPaths []string, stagedByPath map[string]index.StagedEntry, wtSet map[string]struct{}) []string {
	// Collect the full set of paths to examine: HEAD paths + staged-new paths.
	check := make(map[string]struct{}, len(headPaths)+len(stagedByPath))
	for _, p := range headPaths {
		check[p] = struct{}{}
	}
	for p, e := range stagedByPath {
		if e.ChangeType == "added" {
			check[p] = struct{}{}
		}
	}

	var lines []string
	for p := range check {
		if e, ok := stagedByPath[p]; ok && e.ChangeType == "deleted" {
			continue // staged deletion — already shown in staged section
		}
		if _, inWT := wtSet[p]; !inWT {
			lines = append(lines, "deleted: \t"+p)
			continue
		}
		hostPath := workspace.HostPath(repoRoot, p)
		wtHash, _, err := workspace.HashFile(hostPath)
		if err != nil {
			continue
		}
		// Reference is the staged version if staged, otherwise HEAD.
		var referenceHash string
		if se, ok := stagedByPath[p]; ok {
			referenceHash = se.ObjectHash
		} else {
			head, err := s.FileHead(p)
			if err != nil || head == nil {
				continue
			}
			referenceHash = head.ObjectHash
		}
		if wtHash != referenceHash {
			lines = append(lines, "modified:\t"+p)
		}
	}
	return lines
}
