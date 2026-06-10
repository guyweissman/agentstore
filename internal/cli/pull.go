package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/guyweissman/agentstore/internal/client"
	"github.com/guyweissman/agentstore/internal/config"
	"github.com/guyweissman/agentstore/internal/index"
	merge3 "github.com/guyweissman/agentstore/internal/merge"
	"github.com/guyweissman/agentstore/internal/server"
	"github.com/guyweissman/agentstore/internal/store"
	"github.com/guyweissman/agentstore/internal/workspace"
)

func newPullCmd() *cobra.Command {
	var remote string
	cmd := &cobra.Command{
		Use:   "pull [<remote>]",
		Short: "Fetch remote commits and merge them",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 {
				remote = args[0]
			}
			if remote == "" {
				remote = "origin"
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
			cfg, err := config.LoadRepo(root)
			if err != nil {
				return err
			}
			r, ok := cfg.Remotes[remote]
			if !ok {
				return fmt.Errorf("remote %q not configured", remote)
			}
			id, err := loadIdentity(r.URL)
			if err != nil {
				return err
			}
			return RunPull(s, idx, root, r.URL, id)
		},
	}
	cmd.Flags().StringVar(&remote, "remote", "", "remote name (default: origin)")
	return cmd
}

// RunPull fetches new server commits and merges them with the local working tree.
func RunPull(s *store.Store, idx *index.Index, repoRoot, remoteURL string, id *client.Identity) error {
	// Abort if there are staged changes — per the spec.
	staged, err := idx.Entries()
	if err != nil {
		return err
	}
	if len(staged) > 0 {
		return fmt.Errorf("uncommitted changes present; commit first, then pull")
	}

	cl, err := client.New(remoteURL, id)
	if err != nil {
		return err
	}

	// Fetch commits since our highest confirmed seq. Pull is forward-only by
	// design: it never re-scans history below the cursor. A grant that opens up
	// historical commits (seq below the cursor) is therefore NOT backfilled here
	// — picking up newly-granted historical access is done by re-clone. See
	// architecture.md "Retroactive grant and incremental pull (forward-only)".
	maxSeq, err := s.MaxConfirmedSeq()
	if err != nil {
		return err
	}
	newCommits, err := cl.GetAllCommits(maxSeq)
	if err != nil {
		return fmt.Errorf("fetch commits: %w", err)
	}
	if len(newCommits) == 0 {
		fmt.Println("Already up to date.")
		return nil
	}

	// Record redacted stubs (to advance the cursor past them) and drop them from
	// the working set — they have no content the caller can apply.
	visible := make([]server.CommitJSON, 0, len(newCommits))
	for _, c := range newCommits {
		if c.Redacted {
			if err := s.RecordRedactedStub(c.Seq); err != nil {
				return err
			}
			continue
		}
		visible = append(visible, c)
	}
	newCommits = visible
	if len(newCommits) == 0 {
		fmt.Println("Already up to date.")
		return nil
	}

	// Download objects for all new commits.
	for _, c := range newCommits {
		for _, f := range c.Files {
			if f.ObjectHash == "" || s.Objects.HasObject(f.ObjectHash) {
				continue
			}
			data, err := cl.DownloadObject(f.ObjectHash)
			if err != nil {
				return fmt.Errorf("download object %s: %w", f.ObjectHash[:8], err)
			}
			if store.HashContent(data) != f.ObjectHash {
				return fmt.Errorf("object %s: content does not match advertised hash (server or transport error)", f.ObjectHash[:8])
			}
			if _, err := s.Objects.WriteObject(data); err != nil {
				return err
			}
		}
	}

	// Determine which server commits are the latest head for each file.
	// Build map: path → latest server commit (last wins, commits are oldest-first).
	type serverHead struct {
		commitID   string
		objectHash string
		changeType string
	}
	serverHeads := make(map[string]serverHead)
	var secondParentID string // the most recent server commit — used as merge parent
	for _, c := range newCommits {
		secondParentID = c.ID
		for _, f := range c.Files {
			serverHeads[f.Path] = serverHead{
				commitID:   c.ID,
				objectHash: f.ObjectHash,
				changeType: f.ChangeType,
			}
		}
	}

	// Get the local unpushed commit (if any) — this is "ours."
	localUnpushed, err := s.UnpushedCommit()
	if err != nil {
		return err
	}

	// Build a map of our locally committed changes.
	ourFiles := make(map[string]store.CommitFile)
	if localUnpushed != nil {
		for _, f := range localUnpushed.Files {
			ourFiles[f.Path] = f
		}
	}

	// Abort if the pull would overwrite unstaged working-tree modifications.
	// This must run BEFORE storing the remote commits below, because
	// WriteRemoteCommit advances file_branch_heads — after that, FileHead would
	// return the INCOMING remote version, not the local base, and a clean
	// fast-forward (working tree == local HEAD) would false-trip the guard.
	for path := range serverHeads {
		head, err := s.FileHead(path)
		if err != nil || head == nil || head.ObjectHash == "" {
			continue // new file or already deleted in HEAD — no unstaged edit possible
		}
		// Skip if we have a local committed change for this file (handled below).
		if _, weChanged := ourFiles[path]; weChanged {
			continue
		}
		// Safe to update when the working tree still matches the local HEAD
		// (i.e. unmodified), regardless of how different the incoming version is.
		data, err := os.ReadFile(workspace.HostPath(repoRoot, path))
		if err != nil {
			continue // file not on disk
		}
		if store.HashContent(data) != head.ObjectHash {
			return fmt.Errorf("pull would overwrite unstaged changes to %s; commit or revert first", path)
		}
	}

	// Store the new remote commits locally.
	for _, c := range newCommits {
		files := make([]store.CommitFileRecord, len(c.Files))
		for i, f := range c.Files {
			files[i] = store.CommitFileRecord{
				Path: f.Path, ObjectHash: f.ObjectHash,
				Size: f.Size, ChangeType: f.ChangeType,
				BasedOnCommitID: f.BasedOnCommitID,
			}
		}
		parents := c.Parents
		if parents == nil {
			parents = []string{}
		}
		if _, err := s.WriteRemoteCommit(store.CommitRecord{
			Message: c.Message, AuthorID: c.AuthorID, CreatedAt: c.CreatedAt,
			Parents: parents, Files: files,
		}, c.Seq, c.ID); err != nil {
			return fmt.Errorf("store commit %s: %w", c.ID[:8], err)
		}
	}

	// Per-file merge.
	hasConflicts := false
	for path, sh := range serverHeads {
		ours, weChanged := ourFiles[path]

		if !weChanged {
			// Only the server changed this file — fast-forward working tree.
			if err := applyServerHead(s, repoRoot, path, sh.objectHash); err != nil {
				return err
			}
			fmt.Printf("  fast-forward %s\n", path)
			continue
		}

		// Both sides changed.
		oursDeleted := ours.ChangeType == "deleted"
		theirsDeleted := sh.changeType == "deleted"

		// Modify/delete conflict: one side deleted, the other modified.
		// These cannot be resolved by textual merge — the user must choose a side.
		if oursDeleted && !theirsDeleted {
			fmt.Printf("  CONFLICT %s — modify/delete: we deleted, server modified (their version shown)\n", path)
			applyServerHead(s, repoRoot, path, sh.objectHash)
			idx.SetUnresolved(path)
			hasConflicts = true
			continue
		}
		if !oursDeleted && theirsDeleted {
			fmt.Printf("  CONFLICT %s — modify/delete: we modified, server deleted (our version kept)\n", path)
			// Working tree already has our modified version — leave it.
			idx.SetUnresolved(path)
			hasConflicts = true
			continue
		}
		if oursDeleted && theirsDeleted {
			// Both deleted — already consistent, nothing to do.
			fmt.Printf("  fast-forward %s (both deleted)\n", path)
			continue
		}

		// Both modified — three-way textual merge.
		// base = content at based_on_commit_id (the common ancestor).
		baseContent := ""
		if ours.BasedOnCommitID != "" {
			baseHash, err := s.FileAtCommit(ours.BasedOnCommitID, path)
			if err == nil && baseHash != "" {
				data, err := s.Objects.ReadObject(baseHash)
				if err == nil {
					baseContent = string(data)
				}
			}
		}

		ourData, err := s.Objects.ReadObject(ours.ObjectHash)
		if err != nil {
			return err
		}
		theirData, err := s.Objects.ReadObject(sh.objectHash)
		if err != nil {
			return err
		}

		result := merge3.Merge3(baseContent, string(ourData), string(theirData))
		if err := writeWorkingFile(repoRoot, path, []byte(result.Text)); err != nil {
			return err
		}

		if result.HasConflict {
			fmt.Printf("  CONFLICT %s — resolve markers, then add + commit\n", path)
			hasConflicts = true
			if err := idx.SetUnresolved(path); err != nil {
				return err
			}
		} else {
			fmt.Printf("  auto-merged %s\n", path)
			mergedHash, err := s.Objects.WriteObject([]byte(result.Text))
			if err != nil {
				return err
			}
			if err := idx.Stage(index.StagedEntry{
				Path:            path,
				ObjectHash:      mergedHash,
				ChangeType:      "modified",
				BasedOnCommitID: sh.commitID,
			}); err != nil {
				return err
			}
		}
	}

	// Record the second parent for the eventual merge commit.
	if localUnpushed != nil && secondParentID != "" {
		if err := idx.SetMergeState(index.MergeState{SecondParentCommitID: secondParentID}); err != nil {
			return err
		}
	}

	if hasConflicts {
		fmt.Println("Merge in progress — resolve conflicts, add the files, then commit.")
	} else if localUnpushed != nil {
		fmt.Println("All files auto-merged. Run \"commit\" to create the merge commit, then \"push\".")
	} else {
		fmt.Printf("Pulled %d commit(s).\n", len(newCommits))
	}
	return nil
}

func applyServerHead(s *store.Store, repoRoot, path, objectHash string) error {
	// Defence in depth: never touch a host file for a non-canonical path.
	if !store.ValidPath(path) {
		return fmt.Errorf("refusing to apply invalid path %q", path)
	}
	if objectHash == "" {
		// File deleted — remove from working tree.
		return os.Remove(workspace.HostPath(repoRoot, path))
	}
	data, err := s.Objects.ReadObject(objectHash)
	if err != nil {
		return err
	}
	return writeWorkingFile(repoRoot, path, data)
}
