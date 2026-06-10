package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/guyweissman/agentstore/internal/client"
	"github.com/guyweissman/agentstore/internal/config"
	"github.com/guyweissman/agentstore/internal/identity"
)

func newRegisterCmd() *cobra.Command {
	var remote, username, publicKey string
	cmd := &cobra.Command{
		Use:   "register --remote <url> --username <name> --public-key <path>",
		Short: "Register an identity in a remote's open directory",
		RunE: func(cmd *cobra.Command, args []string) error {
			if remote == "" || username == "" || publicKey == "" {
				return fmt.Errorf("--remote, --username, and --public-key are all required")
			}
			pubLine, err := identity.ReadPublicKeyFile(expandHome(publicKey))
			if err != nil {
				return err
			}

			// register is the open endpoint — no identity needed to call it.
			cl, err := client.New(remote+"/_", nil) // repo segment unused by /register
			if err != nil {
				return err
			}
			principalID, err := cl.Register(username, pubLine)
			if err != nil {
				return err
			}

			// Persist the identity for this remote in the global config.
			base := serverBase(remote)
			gc, err := config.LoadGlobal()
			if err != nil {
				return err
			}
			if gc.Remotes == nil {
				gc.Remotes = make(map[string]config.RemoteIdentity)
			}
			gc.Remotes[base] = config.RemoteIdentity{
				Username:    username,
				KeyPath:     identity.PrivateKeyPathFromPublic(expandHome(publicKey)),
				PrincipalID: principalID,
			}
			if err := config.SaveGlobal(gc); err != nil {
				return err
			}

			fmt.Printf("Registered %s at %s\n", username, base)
			fmt.Printf("principal: %s\n", principalID)
			return nil
		},
	}
	cmd.Flags().StringVar(&remote, "remote", "", "remote server URL")
	cmd.Flags().StringVar(&username, "username", "", "username to register")
	cmd.Flags().StringVar(&publicKey, "public-key", "", "path to the ed25519 .pub file")
	return cmd
}

func newWhoAmICmd() *cobra.Command {
	var remote string
	cmd := &cobra.Command{
		Use:   "whoami",
		Short: "Show the principal the remote authenticates you as",
		RunE: func(cmd *cobra.Command, args []string) error {
			// Default the remote to this repo's origin if not given.
			if remote == "" {
				root, err := repoRootFromCwd()
				if err != nil {
					return fmt.Errorf("--remote required outside a repo")
				}
				cfg, err := config.LoadRepo(root)
				if err != nil {
					return err
				}
				origin, ok := cfg.Remotes["origin"]
				if !ok {
					return fmt.Errorf("no origin remote configured; pass --remote")
				}
				remote = origin.URL
			}
			id, err := loadIdentity(remote)
			if err != nil {
				return err
			}
			cl, err := client.New(serverBase(remote)+"/_", id)
			if err != nil {
				return err
			}
			who, err := cl.WhoAmI()
			if err != nil {
				return err
			}
			fmt.Println(who.Username)
			return nil
		},
	}
	cmd.Flags().StringVar(&remote, "remote", "", "remote server URL (defaults to origin)")
	return cmd
}

func newRekeyCmd() *cobra.Command {
	var remote, publicKey string
	cmd := &cobra.Command{
		Use:   "rekey --public-key <path>",
		Short: "Rotate your own public key on a remote",
		RunE: func(cmd *cobra.Command, args []string) error {
			if publicKey == "" {
				return fmt.Errorf("--public-key required")
			}
			if remote == "" {
				root, err := repoRootFromCwd()
				if err != nil {
					return fmt.Errorf("--remote required outside a repo")
				}
				cfg, _ := config.LoadRepo(root)
				origin, ok := cfg.Remotes["origin"]
				if !ok {
					return fmt.Errorf("no origin remote configured; pass --remote")
				}
				remote = origin.URL
			}

			pubLine, err := identity.ReadPublicKeyFile(expandHome(publicKey))
			if err != nil {
				return err
			}
			id, err := loadIdentity(remote)
			if err != nil {
				return err
			}
			cl, err := client.New(serverBase(remote)+"/_", id)
			if err != nil {
				return err
			}
			if err := cl.Rekey(pubLine); err != nil {
				return err
			}

			// Update the local key path to the new key.
			base := serverBase(remote)
			gc, _ := config.LoadGlobal()
			ri := gc.Remotes[base]
			ri.KeyPath = identity.PrivateKeyPathFromPublic(expandHome(publicKey))
			gc.Remotes[base] = ri
			if err := config.SaveGlobal(gc); err != nil {
				return err
			}
			fmt.Printf("Rotated key for %s on %s\n", ri.Username, base)
			return nil
		},
	}
	cmd.Flags().StringVar(&remote, "remote", "", "remote server URL (defaults to origin)")
	cmd.Flags().StringVar(&publicKey, "public-key", "", "path to the new ed25519 .pub file")
	return cmd
}
