package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/guyweissman/agentstore/internal/index"
	"github.com/guyweissman/agentstore/internal/store"
	"github.com/guyweissman/agentstore/internal/workspace"
)

func newMergeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "merge",
		Short: "Manage in-progress merges",
	}
	var abortFlag bool
	cmd.Flags().BoolVar(&abortFlag, "abort", false, "discard an in-progress merge and restore the last committed state")
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		if !abortFlag {
			return fmt.Errorf("use --abort to discard an in-progress merge")
		}
		root, err := repoRootFromCwd()
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
		return RunMergeAbort(s, idx, root)
	}
	return cmd
}

// RunMergeAbort discards an in-progress merge and restores the last committed state.
func RunMergeAbort(s *store.Store, idx *index.Index, repoRoot string) error {
	ms, err := idx.GetMergeState()
	if err != nil {
		return err
	}
	if ms == nil {
		return fmt.Errorf("no merge in progress")
	}

	// Restore all conflict-marked files to the local committed (our) version.
	unresolved, err := idx.UnresolvedPaths()
	if err != nil {
		return err
	}
	for _, path := range unresolved {
		// Get the locally committed content (our side before the merge).
		localC, err := s.UnpushedCommit()
		if err != nil {
			return err
		}
		var objectHash string
		if localC != nil {
			for _, f := range localC.Files {
				if f.Path == path {
					objectHash = f.ObjectHash
					break
				}
			}
		}
		if objectHash == "" {
			// Not in our unpushed commit — restore from confirmed HEAD.
			head, err := s.FileHead(path)
			if err != nil {
				return err
			}
			if head != nil {
				objectHash = head.ObjectHash
			}
		}
		if objectHash != "" {
			data, err := s.Objects.ReadObject(objectHash)
			if err != nil {
				return err
			}
			hostPath := workspace.HostPath(repoRoot, path)
			if err := os.WriteFile(hostPath, data, 0o644); err != nil {
				return err
			}
		}
		idx.ClearUnresolved(path)
	}

	// Discard any staged merge entries.
	if err := idx.Clear(); err != nil {
		return err
	}
	if err := idx.ClearMergeState(); err != nil {
		return err
	}
	fmt.Println("Merge aborted. Working tree restored to last committed state.")
	return nil
}
