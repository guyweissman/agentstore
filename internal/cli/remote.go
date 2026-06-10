package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/guyweissman/agentstore/internal/config"
	"github.com/guyweissman/agentstore/internal/store"
)

func newRemoteCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "remote",
		Short: "Manage remote server connections",
	}
	cmd.AddCommand(newRemoteAddCmd(), newRemoteRemoveCmd(), newRemoteListCmd())
	return cmd
}

func newRemoteAddCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "add <name> <url>",
		Short: "Add a named remote",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := repoRootFromCwd()
			if err != nil {
				return err
			}
			return remoteAdd(root, args[0], args[1])
		},
	}
}

func newRemoteRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "remove <name>",
		Short: "Remove a named remote",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := repoRootFromCwd()
			if err != nil {
				return err
			}
			return remoteRemove(root, args[0])
		},
	}
}

func newRemoteListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List configured remotes",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := repoRootFromCwd()
			if err != nil {
				return err
			}
			return remoteList(root)
		},
	}
}

func remoteAdd(repoRoot, name, remoteURL string) error {
	cfg, err := config.LoadRepo(repoRoot)
	if err != nil {
		return err
	}
	if cfg.Remotes == nil {
		cfg.Remotes = make(map[string]config.RepoRemote)
	}
	cfg.Remotes[name] = config.RepoRemote{URL: remoteURL}
	return config.SaveRepo(repoRoot, cfg)
}

func remoteRemove(repoRoot, name string) error {
	cfg, err := config.LoadRepo(repoRoot)
	if err != nil {
		return err
	}
	delete(cfg.Remotes, name)
	return config.SaveRepo(repoRoot, cfg)
}

func remoteList(repoRoot string) error {
	cfg, err := config.LoadRepo(repoRoot)
	if err != nil {
		return err
	}
	for name, r := range cfg.Remotes {
		fmt.Printf("%s\t%s\n", name, r.URL)
	}
	return nil
}

func repoRootFromCwd() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	return store.FindRoot(cwd)
}

// originURL returns the configured origin remote URL for the repo at root.
func originURL(root string) (string, error) {
	cfg, err := config.LoadRepo(root)
	if err != nil {
		return "", err
	}
	origin, ok := cfg.Remotes["origin"]
	if !ok {
		return "", fmt.Errorf("no origin remote configured")
	}
	return origin.URL, nil
}
