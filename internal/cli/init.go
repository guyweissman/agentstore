package cli

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/guyweissman/agentstore/internal/client"
	"github.com/guyweissman/agentstore/internal/config"
	"github.com/guyweissman/agentstore/internal/store"
)

func newInitCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init <url> [<directory>]",
		Short: "Create a new repo on the server and check it out locally",
		Long: `Creates an empty repo at <url> on the server and initialises a local checkout.
The checkout goes in <directory> if given, otherwise a directory named after the
URL's last path segment.
Example: agentstore init http://127.0.0.1:8080/my-repo`,
		Args: cobra.RangeArgs(1, 2),
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
			return RunInit(repoRoot, repoURL, id)
		},
	}
}

// RunInit creates a repo on the server and initialises a local checkout at repoRoot.
func RunInit(repoRoot, repoURL string, id *client.Identity) error {
	cl, err := client.New(repoURL, id)
	if err != nil {
		return err
	}
	if err := cl.CreateRepo(); err != nil {
		return fmt.Errorf("create repo on server: %w", err)
	}

	// Local checkout with no stub; seed the roster from the server (just the caller).
	s, err := store.InitBare(repoRoot)
	if err != nil {
		return fmt.Errorf("init local store: %w", err)
	}
	defer s.Close()
	if err := seedLocalRoster(s, cl); err != nil {
		return err
	}

	cfg := config.RepoConfig{
		Remotes: map[string]config.RepoRemote{
			"origin": {URL: repoURL},
		},
		Identity: config.RepoIdentity{PrincipalID: id.PrincipalID},
	}
	if err := config.SaveRepo(repoRoot, cfg); err != nil {
		return fmt.Errorf("write local config: %w", err)
	}
	fmt.Printf("Initialized empty repo in %s\n", repoRoot)
	fmt.Printf("origin: %s\n", repoURL)
	return nil
}

// seedLocalRoster copies the server's principal roster into the local store so
// commit authors resolve and FK constraints hold offline.
func seedLocalRoster(s *store.Store, cl *client.Client) error {
	principals, err := cl.GetPrincipals()
	if err != nil {
		return fmt.Errorf("fetch roster: %w", err)
	}
	for _, p := range principals {
		if err := s.AddPrincipal(store.Principal{ID: p.ID, Username: p.Username, PublicKey: p.PublicKey}); err != nil {
			return err
		}
	}
	return nil
}

func repoNameFromURL(u string) string {
	parts := strings.Split(strings.TrimRight(u, "/"), "/")
	if len(parts) == 0 {
		return ""
	}
	return parts[len(parts)-1]
}

// checkoutDir resolves the local checkout path for init/clone: args[1] if the
// caller named a directory, otherwise the URL's last path segment. Relative
// paths resolve against the current directory; an absolute arg is used as-is.
func checkoutDir(args []string) (string, error) {
	dir := ""
	if len(args) == 2 {
		dir = args[1]
	} else {
		dir = repoNameFromURL(args[0])
		if dir == "" {
			return "", fmt.Errorf("cannot determine repo name from URL %q", args[0])
		}
	}
	return filepath.Abs(dir)
}
