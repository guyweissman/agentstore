package cli

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/guyweissman/agentstore/internal/index"
	"github.com/guyweissman/agentstore/internal/store"
	"github.com/guyweissman/agentstore/internal/workspace"
)

func newResetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "reset",
		Short: "Discard all unpushed commits and restore to the last pushed state",
		Long: `Discards every local commit that has not been pushed to the server
and restores the working tree to the last server-confirmed state.

Unlike git reset, this always resets to the last pushed state — there is no
HEAD~1 variant. Staged changes must be committed or manually cleared first.

This operation cannot be undone.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
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

			fmt.Fprintln(os.Stderr, "WARNING: this will discard all unpushed commits and cannot be undone.")
			fmt.Fprint(os.Stderr, "Type \"reset\" to confirm: ")
			scanner := bufio.NewScanner(os.Stdin)
			scanner.Scan()
			if strings.TrimSpace(scanner.Text()) != "reset" {
				return fmt.Errorf("aborted")
			}

			return RunReset(s, idx, root)
		},
	}
}

// RunReset discards all unpushed commits and restores the working tree to the
// last server-confirmed state.
func RunReset(s *store.Store, idx *index.Index, repoRoot string) error {
	// Abort if staged changes exist — reset would make them confusing.
	staged, err := idx.Entries()
	if err != nil {
		return err
	}
	if len(staged) > 0 {
		return fmt.Errorf("staged changes present; commit or clear them before reset")
	}

	// Check first so we can give a count in the success message.
	chain, err := s.UnpushedChain()
	if err != nil {
		return err
	}
	if len(chain) == 0 {
		return fmt.Errorf("nothing to reset (no unpushed commits)")
	}
	n := len(chain)

	// Reset the store: absorb unpushed commits + restore file_branch_heads.
	paths, err := s.Reset()
	if err != nil {
		return err
	}

	// Restore the working tree for each affected path.
	for _, path := range paths {
		head, err := s.FileHead(path)
		if err != nil {
			return err
		}
		hostPath := workspace.HostPath(repoRoot, path)
		if head == nil || head.ObjectHash == "" {
			// File was only in unpushed commits — remove it from the working tree.
			if err := os.Remove(hostPath); err != nil && !os.IsNotExist(err) {
				return err
			}
			fmt.Printf("  removed  %s\n", path)
		} else {
			if err := applyServerHead(s, repoRoot, path, head.ObjectHash); err != nil {
				return err
			}
			fmt.Printf("  restored %s\n", path)
		}
	}

	word := "commit"
	if n != 1 {
		word = "commits"
	}
	fmt.Printf("Reset complete — %d unpushed %s discarded.\n", n, word)
	return nil
}
