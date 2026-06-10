package cli

import (
	"github.com/guyweissman/agentstore/internal/brand"
	"github.com/spf13/cobra"
)

// Root builds and returns the root cobra command with all subcommands attached.
func Root() *cobra.Command {
	root := &cobra.Command{
		Use:   brand.AppName,
		Short: "Portable, file-native datastore for agent knowledge work",
		Long: brand.AppName + ` is an open-source, portable, file-native datastore for agent
knowledge work. It combines versioned change control, file-level access control,
real-time event streams, and a commit log for higher-level knowledge graphs.

Run ` + "`" + brand.AppName + ` <command> --help` + "`" + ` for details on any command.`,
		SilenceUsage: true,
	}

	root.AddCommand(
		// local store
		newAddCmd(),
		newRmCmd(),
		newStatusCmd(),
		newDiffCmd(),
		newCommitCmd(),
		newLogCmd(),
		newShowCmd(),
		newCheckoutCmd(),
		// server and sync
		newServerCmd(),
		newInitCmd(),
		newCloneCmd(),
		newPushCmd(),
		newPullCmd(),
		newResetCmd(),
		newMergeCmd(),
		newRemoteCmd(),
		newConfigCmd(),
		// identity, auth, permissions
		newRegisterCmd(),
		newBindCmd(),
		newWhoAmICmd(),
		newRekeyCmd(),
		newGrantCmd(),
		newRevokeCmd(),
		newPermissionsCmd(),
		newPrincipalsCmd(),
		newAdminCmd(),
		// events and watch
		newWatchCmd(),
		// agent skill
		newSkillCmd(),
		// meta
		newVersionCmd(),
	)

	return root
}
