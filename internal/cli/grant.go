package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/guyweissman/agentstore/internal/client"
)

// repoClient builds an authenticated client for the current repo's origin.
func repoClient() (*client.Client, error) {
	root, err := repoRootFromCwd()
	if err != nil {
		return nil, err
	}
	originURL, err := originURL(root)
	if err != nil {
		return nil, err
	}
	id, err := loadIdentity(originURL)
	if err != nil {
		return nil, err
	}
	return client.New(originURL, id)
}

func newGrantCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "grant <principal> <permission> <path>",
		Short: "Set a principal's access level on a path",
		Args:  cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl, err := repoClient()
			if err != nil {
				return err
			}
			if err := cl.Grant(args[0], args[1], args[2]); err != nil {
				return err
			}
			fmt.Printf("granted %s %s on %s\n", args[0], args[1], args[2])
			return nil
		},
	}
}

func newRevokeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "revoke <principal> <path>",
		Short: "Remove a principal's grant on a path",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl, err := repoClient()
			if err != nil {
				return err
			}
			if err := cl.Revoke(args[0], args[1]); err != nil {
				return err
			}
			fmt.Printf("revoked %s on %s\n", args[0], args[1])
			return nil
		},
	}
}

func newPermissionsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "permissions <path>",
		Short: "List effective permissions on a path",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl, err := repoClient()
			if err != nil {
				return err
			}
			entries, err := cl.Permissions(args[0])
			if err != nil {
				return err
			}
			if len(entries) == 0 {
				fmt.Println("(no grants match this path)")
				return nil
			}
			for _, e := range entries {
				fmt.Printf("%s\t%s\t%s\n", e.Principal, e.Permission, e.Path)
			}
			return nil
		},
	}
}
