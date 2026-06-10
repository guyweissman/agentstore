package cli

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/guyweissman/agentstore/internal/config"
	"github.com/guyweissman/agentstore/internal/index"
	"github.com/guyweissman/agentstore/internal/store"
)

func newCommitCmd() *cobra.Command {
	var message string
	cmd := &cobra.Command{
		Use:   "commit",
		Short: "Commit staged changes",
		RunE: func(cmd *cobra.Command, args []string) error {
			if message == "" {
				return errors.New("commit message required: use -m \"<message>\"")
			}
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
			id, err := RunCommit(s, idx, message)
			if err != nil {
				return err
			}
			fmt.Printf("[%s] %s\n", id[:8], message)
			return nil
		},
	}
	cmd.Flags().StringVarP(&message, "message", "m", "", "commit message")
	return cmd
}

// RunCommit creates a commit from the current staged entries.
// Returns the new commit ID.
func RunCommit(s *store.Store, idx *index.Index, message string) (string, error) {
	entries, err := idx.Entries()
	if err != nil {
		return "", err
	}
	if len(entries) == 0 {
		return "", errors.New("nothing to commit (use \"add\" to stage changes)")
	}

	// Determine parent(s).
	// If a merge is in progress, the commit is a merge commit with two parents:
	//   first  = our local unpushed commit (ours)
	//   second = the remote commit merged against (theirs, from merge_state)
	// Otherwise it is a normal commit with a single parent (latest commit).
	ms, err := idx.GetMergeState()
	if err != nil {
		return "", err
	}
	var parents []string
	if ms != nil {
		// Merge commit: single parent = the server-confirmed remote head.
		// The local rejected commit is NOT included as a parent because the server
		// doesn't have it, and push filters it anyway — including it would cause
		// the client and server to compute different commit IDs from different
		// parent lists.
		parents = []string{ms.SecondParentCommitID}
	} else {
		// Normal commit: single parent = latest non-absorbed commit.
		parentID, err := s.LatestCommitID()
		if err != nil {
			return "", err
		}
		if parentID != "" {
			parents = []string{parentID}
		}
	}

	// Build file records from staged entries.
	files := make([]store.CommitFileRecord, len(entries))
	for i, e := range entries {
		// Ensure every non-deletion object is present in the object store.
		if e.ObjectHash != "" && !s.Objects.HasObject(e.ObjectHash) {
			return "", fmt.Errorf("object %s for %s not in store (was the file staged?)", e.ObjectHash[:8], e.Path)
		}
		var size int64
		if e.ObjectHash != "" {
			data, err := s.Objects.ReadObject(e.ObjectHash)
			if err != nil {
				return "", err
			}
			size = int64(len(data))
		}
		files[i] = store.CommitFileRecord{
			Path:            e.Path,
			ObjectHash:      e.ObjectHash,
			Size:            size,
			ChangeType:      e.ChangeType,
			BasedOnCommitID: e.BasedOnCommitID,
		}
	}

	// Author the commit as the principal bound to this repo's origin identity,
	// so the locally-computed commit ID matches what the server computes on push.
	// Falls back to the stub principal for local-only repos with no origin/identity.
	authorID := resolveAuthor(s)

	id, err := s.WriteCommit(store.CommitRecord{
		Message:  message,
		AuthorID: authorID,
		Parents:  parents,
		Files:    files,
	})
	if err != nil {
		return "", err
	}

	if err := idx.Clear(); err != nil {
		return id, err
	}
	if ms != nil {
		if err := idx.ClearMergeState(); err != nil {
			return id, err
		}
	}
	return id, nil
}

// resolveAuthor returns the principal_id to attribute a local commit to. It reads
// the clone's own principal from the local repo config (set at init/clone), so the
// locally-computed commit ID matches what the server computes on push. Falls back
// to the stub principal for local-only repos (M1 engine tests).
func resolveAuthor(s *store.Store) string {
	cfg, err := config.LoadRepo(s.Root)
	if err != nil {
		return store.StubPrincipalID
	}
	if cfg.Identity.PrincipalID != "" {
		return cfg.Identity.PrincipalID
	}
	return store.StubPrincipalID
}
