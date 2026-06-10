package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/guyweissman/agentstore/internal/client"
	"github.com/guyweissman/agentstore/internal/config"
	"github.com/guyweissman/agentstore/internal/index"
	"github.com/guyweissman/agentstore/internal/store"
)

func newCloneCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "clone <url> [<directory>]",
		Short: "Download a remote repo locally",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			repoURL := args[0]
			repoRoot, err := checkoutDir(args)
			if err != nil {
				return err
			}
			id, err := loadIdentity(repoURL)
			if err != nil {
				return err
			}
			return RunClone(repoURL, repoRoot, id)
		},
	}
}

// RunClone downloads the server repo at repoURL into repoRoot.
func RunClone(repoURL, repoRoot string, id *client.Identity) error {
	cl, err := client.New(repoURL, id)
	if err != nil {
		return err
	}

	// Init a fresh local store (no stub principal — seed the real roster).
	s, err := store.InitBare(repoRoot)
	if err != nil {
		return fmt.Errorf("init local store: %w", err)
	}
	defer s.Close()

	idx, err := index.Init(s.Dir())
	if err != nil {
		return err
	}
	defer idx.Close()

	// Seed the member roster so commit authors resolve and FK constraints hold.
	if err := seedLocalRoster(s, cl); err != nil {
		return err
	}

	// Fetch all commits (paginated; since=0 means everything).
	commits, err := cl.GetAllCommits(0)
	if err != nil {
		return fmt.Errorf("fetch commits: %w", err)
	}

	// Download objects and apply commits in order.
	for _, c := range commits {
		if c.Redacted {
			// Fully-inaccessible commit — record the stub to keep the local seq
			// space contiguous, then move on (no content to apply).
			if err := s.RecordRedactedStub(c.Seq); err != nil {
				return err
			}
			continue
		}
		for _, f := range c.Files {
			if f.ObjectHash == "" {
				continue
			}
			if s.Objects.HasObject(f.ObjectHash) {
				continue
			}
			data, err := cl.DownloadObject(f.ObjectHash)
			if err != nil {
				return fmt.Errorf("download object %s: %w", f.ObjectHash[:8], err)
			}
			// Verify content integrity before storing — guards against a malicious
			// server or HTTP MITM serving tampered bytes for an advertised hash.
			if store.HashContent(data) != f.ObjectHash {
				return fmt.Errorf("object %s: content does not match advertised hash (server or transport error)", f.ObjectHash[:8])
			}
			if _, err := s.Objects.WriteObject(data); err != nil {
				return err
			}
		}

		parents := c.Parents
		if parents == nil {
			parents = []string{}
		}
		files := make([]store.CommitFileRecord, len(c.Files))
		for i, f := range c.Files {
			files[i] = store.CommitFileRecord{
				Path:            f.Path,
				ObjectHash:      f.ObjectHash,
				Size:            f.Size,
				ChangeType:      f.ChangeType,
				BasedOnCommitID: f.BasedOnCommitID,
			}
		}
		if _, err := s.WriteRemoteCommit(store.CommitRecord{
			Message:   c.Message,
			AuthorID:  c.AuthorID,
			CreatedAt: c.CreatedAt,
			Parents:   parents,
			Files:     files,
		}, c.Seq, c.ID); err != nil {
			return fmt.Errorf("write commit %s: %w", c.ID[:8], err)
		}
	}

	// Check out the working tree (latest head for every file).
	headPaths, err := s.ListHeadPaths()
	if err != nil {
		return err
	}
	for _, sp := range headPaths {
		head, err := s.FileHead(sp)
		if err != nil || head == nil || head.ObjectHash == "" {
			continue
		}
		data, err := s.Objects.ReadObject(head.ObjectHash)
		if err != nil {
			return err
		}
		if err := writeWorkingFile(repoRoot, sp, data); err != nil {
			return err
		}
	}

	cfg := config.RepoConfig{
		Remotes:  map[string]config.RepoRemote{"origin": {URL: repoURL}},
		Identity: config.RepoIdentity{PrincipalID: id.PrincipalID},
	}
	if err := config.SaveRepo(repoRoot, cfg); err != nil {
		return err
	}
	fmt.Printf("Cloned %s → %s (%d commits)\n", repoURL, repoRoot, len(commits))
	return nil
}
