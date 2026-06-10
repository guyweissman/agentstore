package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/guyweissman/agentstore/internal/index"
	"github.com/guyweissman/agentstore/internal/store"
	"github.com/guyweissman/agentstore/internal/workspace"
)

func newAddCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "add <path>...",
		Short: "Stage files for the next commit",
		Args:  cobra.MinimumNArgs(1),
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
			return RunAdd(s, idx, root, args)
		},
	}
}

// RunAdd stages the given paths (relative to repoRoot, or "." for everything).
func RunAdd(s *store.Store, idx *index.Index, repoRoot string, paths []string) error {
	// Expand "." to all working-tree files.
	var targets []string
	for _, p := range paths {
		if p == "." {
			all, err := workspace.WalkRepo(repoRoot)
			if err != nil {
				return err
			}
			targets = append(targets, all...)
		} else {
			// workspace.StorePath calls filepath.Abs internally, so relative paths
			// are resolved against the process CWD — matching git's behaviour when
			// the user runs the command from a subdirectory.
			sp, err := workspace.StorePath(repoRoot, p)
			if err != nil {
				return err
			}
			targets = append(targets, sp)
		}
	}

	for _, sp := range targets {
		if _, err := stageOne(s, idx, repoRoot, sp); err != nil {
			return err
		}
	}
	return nil
}

// stageOne stages the working-tree version of storePath. It returns false (no
// error) when there is nothing to stage because the content already matches HEAD.
func stageOne(s *store.Store, idx *index.Index, repoRoot, storePath string) (bool, error) {
	hostPath := workspace.HostPath(repoRoot, storePath)
	data, err := os.ReadFile(hostPath)
	if err != nil {
		return false, fmt.Errorf("add %s: %w", storePath, err)
	}
	// Compute the hash from the already-read bytes — avoids a second read and
	// ensures the stored object and the staged hash are always consistent.
	hash := store.HashContent(data)

	head, err := s.FileHead(storePath)
	if err != nil {
		return false, err
	}

	var changeType, basedOnCommitID string
	switch {
	case head == nil:
		changeType = "added"
	case head.ChangeType == "deleted":
		// Recreating a previously-deleted file. Carry the delete commit as the
		// OCC base so the server can verify the file is still deleted at push time.
		changeType = "added"
		basedOnCommitID = head.CommitID
	case head.ObjectHash == hash:
		// Content unchanged; nothing to stage.
		return false, nil
	default:
		changeType = "modified"
		basedOnCommitID = head.CommitID
	}

	// Object-before-metadata: write object first.
	if _, err := s.Objects.WriteObject(data); err != nil {
		return false, err
	}

	if err := idx.Stage(index.StagedEntry{
		Path:            storePath,
		ObjectHash:      hash,
		ChangeType:      changeType,
		BasedOnCommitID: basedOnCommitID,
	}); err != nil {
		return false, err
	}
	return true, nil
}

func newRmCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "rm <path>",
		Short: "Stage a file deletion",
		Args:  cobra.ExactArgs(1),
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
			return RunRm(s, idx, root, args[0])
		},
	}
}

// RunRm stages a deletion for the given path.
func RunRm(s *store.Store, idx *index.Index, repoRoot, path string) error {
	sp, err := workspace.StorePath(repoRoot, path)
	if err != nil {
		return err
	}

	head, err := s.FileHead(sp)
	if err != nil {
		return err
	}
	if head == nil || head.ChangeType == "deleted" {
		return fmt.Errorf("%s: not in repository", path)
	}

	// Remove from working tree.
	if err := os.Remove(workspace.HostPath(repoRoot, sp)); err != nil && !os.IsNotExist(err) {
		return err
	}

	return idx.Stage(index.StagedEntry{
		Path:            sp,
		ObjectHash:      "",
		ChangeType:      "deleted",
		BasedOnCommitID: head.CommitID,
	})
}
