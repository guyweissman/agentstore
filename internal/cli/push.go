package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/guyweissman/agentstore/internal/client"
	"github.com/guyweissman/agentstore/internal/config"
	"github.com/guyweissman/agentstore/internal/index"
	"github.com/guyweissman/agentstore/internal/server"
	"github.com/guyweissman/agentstore/internal/store"
)

func newPushCmd() *cobra.Command {
	var remote string
	var mirror bool
	cmd := &cobra.Command{
		Use:   "push [<remote>]",
		Short: "Push the local commit to the remote (or --mirror to relocate the repo)",
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
			cfg, err := config.LoadRepo(root)
			if err != nil {
				return err
			}
			target, ok := cfg.Remotes[remote]
			if !ok {
				return fmt.Errorf("remote %q not configured", remote)
			}

			if mirror {
				origin, ok := cfg.Remotes["origin"]
				if !ok {
					return fmt.Errorf("no origin remote to mirror from")
				}
				sourceID, err := loadIdentity(origin.URL)
				if err != nil {
					return err
				}
				resp, err := RunMirror(origin.URL, target.URL, sourceID)
				if err != nil {
					return err
				}
				fmt.Printf("Mirrored %s → %s\n", origin.URL, target.URL)

				// Auto-bind: record local identity for the new remote using the
				// preserved principal_id (and possibly auto-renamed username) the
				// target reported, reusing the source remote's key. The admin can
				// then operate against the new home without a separate `bind`.
				gc, err := config.LoadGlobal()
				if err != nil {
					return err
				}
				if gc.Remotes == nil {
					gc.Remotes = map[string]config.RemoteIdentity{}
				}
				dstBase := serverBase(target.URL)
				gc.Remotes[dstBase] = config.RemoteIdentity{
					Username:    resp.Username,
					KeyPath:     gc.Remotes[serverBase(origin.URL)].KeyPath,
					PrincipalID: resp.PrincipalID,
				}
				if err := config.SaveGlobal(gc); err != nil {
					return err
				}
				fmt.Printf("You are %q on %s (principal %s)\n", resp.Username, dstBase, resp.PrincipalID)
				for _, rn := range resp.Renames {
					fmt.Printf("  note: username %q was taken on the new server — %s is now %q (they should `bind %s`)\n",
						rn.From, rn.PrincipalID, rn.To, rn.To)
				}
				return nil
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
			id, err := loadIdentity(target.URL)
			if err != nil {
				return err
			}
			result, err := RunPush(s, target.URL, id)
			if err != nil {
				return err
			}
			if len(result.Conflicts) > 0 {
				fmt.Fprintf(os.Stderr, "rejected — conflict on %d file(s):\n", len(result.Conflicts))
				for _, c := range result.Conflicts {
					head := c.CurrentHeadCommitID
					if head == "" {
						head = "(none)"
					} else {
						head = shortID(head)
					}
					fmt.Fprintf(os.Stderr, "  %s (server head: %s)\n", c.Path, head)
				}
				return fmt.Errorf("push rejected; run \"pull\" to reconcile, then push again")
			}
			if result.ID != "" {
				fmt.Printf("[%s] seq %d\n", result.ID[:8], result.Seq)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&remote, "remote", "", "remote name (default: origin)")
	cmd.Flags().BoolVar(&mirror, "mirror", false, "admin only: relocate the repo to an EMPTY remote (full history, grants, roles, roster)")
	return cmd
}

// RunPush pushes all unpushed local commits to remoteURL, oldest first.
// If any commit is rejected by OCC, it stops and returns the conflict.
// Returns the result of the last successfully pushed commit.
func RunPush(s *store.Store, remoteURL string, id *client.Identity) (client.PushResult, error) {
	chain, err := s.UnpushedChain()
	if err != nil {
		return client.PushResult{}, err
	}
	if len(chain) == 0 {
		return client.PushResult{}, fmt.Errorf("nothing to push (no unpushed local commits)")
	}

	cl, err := client.New(remoteURL, id)
	if err != nil {
		return client.PushResult{}, err
	}

	var lastResult client.PushResult
	for _, c := range chain {
		result, err := pushOne(s, cl, c)
		if err != nil {
			return result, err
		}
		if len(result.Conflicts) > 0 {
			return result, nil // OCC conflict — caller reports and user must pull
		}
		if err := s.ConfirmSeq(c.ID, result.Seq); err != nil {
			return result, fmt.Errorf("confirm seq %s: %w", c.ID[:8], err)
		}
		lastResult = result
	}

	// Absorb any stale seq=NULL commits not in the chain (e.g. the pre-merge
	// rejected commit whose parents diverged from the merge commit's chain).
	if err := s.AbsorbAllUnpushedCommits(); err != nil {
		return lastResult, fmt.Errorf("absorb: %w", err)
	}
	return lastResult, nil
}

// pushOne uploads objects and sends one commit to the server.
func pushOne(s *store.Store, cl *client.Client, c *store.Commit) (client.PushResult, error) {
	// Upload objects first (object-before-metadata ordering).
	for _, f := range c.Files {
		if f.ObjectHash == "" {
			continue
		}
		data, err := s.Objects.ReadObject(f.ObjectHash)
		if err != nil {
			return client.PushResult{}, fmt.Errorf("read object %s: %w", f.ObjectHash[:8], err)
		}
		if err := cl.UploadObject(f.ObjectHash, data); err != nil {
			return client.PushResult{}, fmt.Errorf("upload %s: %w", f.ObjectHash[:8], err)
		}
	}

	// Only include parents the server already knows about (confirmed seq > 0).
	// Local-only parents (seq=NULL or seq=-1) are not on the server.
	var parents []string
	for _, parentID := range c.Parents {
		p, err := s.GetCommit(parentID)
		if err != nil {
			continue
		}
		if p.Seq > 0 {
			parents = append(parents, parentID)
		}
	}
	if parents == nil {
		parents = []string{}
	}

	files := make([]server.PushFile, len(c.Files))
	for i, f := range c.Files {
		files[i] = server.PushFile{
			Path:            f.Path,
			ChangeType:      f.ChangeType,
			ObjectHash:      f.ObjectHash,
			Size:            f.Size,
			BasedOnCommitID: f.BasedOnCommitID,
		}
	}
	return cl.Push(server.PushRequest{
		Message:   c.Message,
		CreatedAt: c.CreatedAt,
		Parents:   parents,
		Files:     files,
	})
}
