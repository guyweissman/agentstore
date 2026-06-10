package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/guyweissman/agentstore/internal/client"
)

func newPrincipalsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "principals",
		Short: "Manage repo membership",
	}
	cmd.AddCommand(
		newPrincipalsAddCmd(),
		newPrincipalsListCmd(),
		newPrincipalsRemoveCmd(),
	)
	return cmd
}

func newPrincipalsAddCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "add <username>",
		Short: "Add a directory principal to this repo (admin only)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl, err := repoClient()
			if err != nil {
				return err
			}
			if err := cl.AddMember(args[0]); err != nil {
				return err
			}
			fmt.Printf("added %s to the repo\n", args[0])
			return nil
		},
	}
}

func newPrincipalsListCmd() *cobra.Command {
	var remote string
	cmd := &cobra.Command{
		Use:   "list [--remote <url>]",
		Short: "List this repo's members, or browse a remote's directory with --remote",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			// --remote browses the open directory: every principal registered on
			// the remote, no repo context needed (like register/whoami).
			if remote != "" {
				cl, err := client.New(serverBase(remote)+"/_", nil)
				if err != nil {
					return err
				}
				entries, err := cl.ListDirectory()
				if err != nil {
					return err
				}
				for _, e := range entries {
					fmt.Printf("%s\t%s\n", e.Username, e.PrincipalID)
				}
				return nil
			}

			cl, err := repoClient()
			if err != nil {
				return err
			}
			members, err := cl.ListMembers()
			if err != nil {
				return err
			}
			for _, m := range members {
				fmt.Printf("%s\t%s\n", m.Username, m.ID)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&remote, "remote", "", "browse this remote's directory instead of repo members")
	return cmd
}

func newPrincipalsRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "remove <username>",
		Short: "Remove a member from this repo (admin only)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl, err := repoClient()
			if err != nil {
				return err
			}
			if err := cl.RemoveMember(args[0]); err != nil {
				return err
			}
			fmt.Printf("removed %s from the repo\n", args[0])
			return nil
		},
	}
}

func newAdminCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "admin",
		Short: "Manage the repo admin role",
	}
	cmd.AddCommand(newAdminAddCmd(), newAdminRevokeCmd(), newAdminListCmd())
	return cmd
}

func newAdminAddCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "add <username>",
		Short: "Grant the repo-admin role (admin only)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl, err := repoClient()
			if err != nil {
				return err
			}
			if err := cl.AddAdmin(args[0]); err != nil {
				return err
			}
			fmt.Printf("%s is now an admin\n", args[0])
			return nil
		},
	}
}

func newAdminRevokeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "revoke <username>",
		Short: "Revoke the repo-admin role — refuses to remove the last admin (admin only)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl, err := repoClient()
			if err != nil {
				return err
			}
			if err := cl.RevokeAdmin(args[0]); err != nil {
				return err
			}
			fmt.Printf("revoked admin from %s\n", args[0])
			return nil
		},
	}
}

func newAdminListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List this repo's admins",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cl, err := repoClient()
			if err != nil {
				return err
			}
			admins, err := cl.ListAdmins()
			if err != nil {
				return err
			}
			for _, a := range admins {
				fmt.Println(a)
			}
			return nil
		},
	}
}
