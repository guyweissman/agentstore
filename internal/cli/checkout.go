package cli

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/guyweissman/agentstore/internal/client"
	"github.com/guyweissman/agentstore/internal/index"
	"github.com/guyweissman/agentstore/internal/store"
	"github.com/guyweissman/agentstore/internal/workspace"
)

func newCheckoutCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "checkout <commit|seq> <path|.>",
		Short: "Restore a file (or the whole repo) to a prior version",
		Long: `Restore content to an earlier point in history.

  checkout <commit|seq> <path>   restore a single file and stage it
  checkout <commit|seq> .        restore the ENTIRE repo and stage all changes

The reference may be a commit ID (full or abbreviated) or a server seq number
(as shown in "log"). checkout finds the most recent version of each path at or
before that point. It restores files to disk and stages the changes, leaving
you to "commit" — it never rewrites history.

Whole-repo checkout (".") is restricted to repo admins and owners of /* and
requires typed confirmation. Unlike git, there is no branch switching.`,
		Args: cobra.ExactArgs(2),
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

			commitID, err := resolveCommitRef(s, args[0])
			if err != nil {
				return err
			}

			// Whole-repo checkout.
			if args[1] == "." {
				// "." means the whole repo, not the current directory. To avoid the
				// git footgun where "." is cwd-relative, require running from the
				// repo root so the scope is unambiguous.
				if filepath.Clean(cwd) != root {
					return fmt.Errorf("whole-repo checkout (\".\") must be run from the repo root (%s)", root)
				}
				if err := ensureWholeRepoCheckoutAllowed(root); err != nil {
					return err
				}
				fmt.Fprintln(os.Stderr, "WARNING: this restores the ENTIRE repo to an earlier state and stages all changes.")

				// Warn if unpushed commits exist: checkout . will fold them into
				// permanent history (they get pushed, then the rewind undoes them).
				// To abandon unpushed work instead, use "reset".
				chain, err := s.UnpushedChain()
				if err != nil {
					return err
				}
				if len(chain) > 0 {
					fmt.Fprintf(os.Stderr,
						"NOTE: you have %d unpushed commit(s). They will be pushed as history and then\n"+
							"      undone by this rewind. To discard unpushed work instead, run \"reset\".\n",
						len(chain))
				}

				fmt.Fprint(os.Stderr, "Type \"checkout\" to confirm: ")
				sc := bufio.NewScanner(os.Stdin)
				sc.Scan()
				if strings.TrimSpace(sc.Text()) != "checkout" {
					return fmt.Errorf("aborted")
				}
				return RunCheckoutRepo(s, idx, root, commitID)
			}

			// Single-file checkout.
			sp, err := workspace.StorePath(root, args[1])
			if err != nil {
				return err
			}
			return RunCheckout(s, idx, root, commitID, sp)
		},
	}
}

// resolveCommitRef turns a user-supplied reference into a full commit ID.
// A purely numeric reference is treated as a server seq; otherwise it is a
// commit ID (full or abbreviated prefix).
func resolveCommitRef(s *store.Store, ref string) (string, error) {
	if seq, err := strconv.ParseInt(ref, 10, 64); err == nil {
		return s.CommitIDBySeq(seq)
	}
	c, err := s.GetCommit(ref)
	if err != nil {
		return "", err
	}
	return c.ID, nil
}

// ensureWholeRepoCheckoutAllowed gates `checkout .` to admins / owners of /*.
// It asks the server for the caller's current authority (live, never stale).
// The authoritative enforcement is still the per-file write check the server
// applies on push; this is upfront feedback so a forbidden rewind isn't staged.
func ensureWholeRepoCheckoutAllowed(repoRoot string) error {
	originURL, err := originURL(repoRoot)
	if err != nil {
		return err
	}
	id, err := loadIdentity(originURL)
	if err != nil {
		return err
	}
	cl, err := client.New(originURL, id)
	if err != nil {
		return err
	}
	authz, err := cl.Authz()
	if err != nil {
		return fmt.Errorf("check authorization: %w", err)
	}
	if !authz.Admin && !authz.RootOwner {
		return fmt.Errorf("whole-repo checkout requires admin or owner of /*")
	}
	return nil
}

// RunCheckout restores storePath to the version it had at commitID's position in
// history, writing it to disk and staging the change (aligns with git, which
// stages on checkout of a path).
func RunCheckout(s *store.Store, idx *index.Index, repoRoot, commitID, storePath string) error {
	objectHash, err := s.FileAtCommit(commitID, storePath)
	if err != nil {
		return err
	}
	if objectHash == "" {
		return fmt.Errorf("%s did not exist at commit %s", storePath, shortID(commitID))
	}

	data, err := s.Objects.ReadObject(objectHash)
	if err != nil {
		return err
	}
	if err := writeWorkingFile(repoRoot, storePath, data); err != nil {
		return fmt.Errorf("checkout %s: %w", storePath, err)
	}
	if _, err := stageOne(s, idx, repoRoot, storePath); err != nil {
		return err
	}

	fmt.Printf("restored %s to commit %s\n", storePath, shortID(commitID))
	return nil
}

// RunCheckoutRepo restores the entire working tree to the state at commitID,
// staging every resulting change (restores, modifications, and deletions).
func RunCheckoutRepo(s *store.Store, idx *index.Index, repoRoot, commitID string) error {
	target, err := s.FilesAtCommit(commitID)
	if err != nil {
		return err
	}

	currentPaths, err := s.ListHeadPaths()
	if err != nil {
		return err
	}

	// Restore (and stage) every file present in the target state.
	targetPaths := make([]string, 0, len(target))
	for p := range target {
		targetPaths = append(targetPaths, p)
	}
	sort.Strings(targetPaths)
	for _, path := range targetPaths {
		data, err := s.Objects.ReadObject(target[path])
		if err != nil {
			return err
		}
		if err := writeWorkingFile(repoRoot, path, data); err != nil {
			return err
		}
		if _, err := stageOne(s, idx, repoRoot, path); err != nil {
			return err
		}
	}

	// Stage deletions for files present now but absent from the target state.
	for _, path := range currentPaths {
		if _, inTarget := target[path]; inTarget {
			continue
		}
		if err := os.Remove(workspace.HostPath(repoRoot, path)); err != nil && !os.IsNotExist(err) {
			return err
		}
		head, err := s.FileHead(path)
		if err != nil {
			return err
		}
		basedOn := ""
		if head != nil {
			basedOn = head.CommitID
		}
		if err := idx.Stage(index.StagedEntry{
			Path: path, ObjectHash: "", ChangeType: "deleted", BasedOnCommitID: basedOn,
		}); err != nil {
			return err
		}
	}

	// Drop staged-new files that don't exist at the target point. They are NOT
	// part of the target state, so the rewind commit must not carry them.
	// Unstage them (leaving the file on disk as untracked) rather than deleting —
	// the committed rewind then matches the target exactly, without losing work.
	staged, err := idx.Entries()
	if err != nil {
		return err
	}
	var untracked []string
	for _, e := range staged {
		if e.ChangeType != "added" {
			continue
		}
		if _, inTarget := target[e.Path]; inTarget {
			continue // legitimately restored by this checkout
		}
		if err := idx.Unstage(e.Path); err != nil {
			return err
		}
		untracked = append(untracked, e.Path)
	}
	sort.Strings(untracked)
	for _, p := range untracked {
		fmt.Printf("  left untracked %s (new file, absent at target)\n", p)
	}

	staged, err = idx.Entries()
	if err != nil {
		return err
	}
	fmt.Printf("Restored repo to %s — %d change(s) staged.\n", shortID(commitID), len(staged))
	fmt.Println("Review with \"status\", then \"commit\".")
	return nil
}

func writeWorkingFile(repoRoot, storePath string, data []byte) error {
	// Defence in depth: never materialize a path that isn't a canonical in-repo
	// path (a malicious server could otherwise traverse out of the working tree).
	if !store.ValidPath(storePath) {
		return fmt.Errorf("refusing to write invalid path %q", storePath)
	}
	hostPath := workspace.HostPath(repoRoot, storePath)
	parent := parentDir(hostPath)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return err
	}
	// Resolve both the parent directory and the repo root through symlinks, then
	// verify the parent stays within the root. Both must be resolved: on macOS
	// /tmp is a symlink to /private/tmp, so an unresolved root and a resolved
	// parent would never share a prefix.
	resolvedParent, err := filepath.EvalSymlinks(parent)
	if err != nil {
		return fmt.Errorf("resolve parent for %s: %w", storePath, err)
	}
	resolvedRoot, err := filepath.EvalSymlinks(repoRoot)
	if err != nil {
		return fmt.Errorf("resolve repo root: %w", err)
	}
	if resolvedParent != resolvedRoot && !strings.HasPrefix(resolvedParent, resolvedRoot+string(filepath.Separator)) {
		return fmt.Errorf("refusing to write %s: parent directory escapes repo root via symlink", storePath)
	}
	return os.WriteFile(hostPath, data, 0o644)
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

func parentDir(path string) string {
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == os.PathSeparator {
			return path[:i]
		}
	}
	return "."
}
