package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/guyweissman/agentstore/internal/client"
	"github.com/guyweissman/agentstore/internal/config"
	"github.com/guyweissman/agentstore/internal/identity"
)

// newBindCmd binds local config to an EXISTING identity the remote already knows
// for your key — distinct from `register`, which always creates a NEW identity.
// Its purpose is reconnecting after a repo move: `push --mirror` seeds the new
// server's directory with the original (principal_id, username, public_key), and
// `bind` points your local config at that preserved principal_id instead of
// minting a fresh one. It is safe because you cannot choose a principal_id — you
// name a username and the server returns the id bound to that name; you only
// succeed if the key the directory holds for that username is the key you hold.
func newBindCmd() *cobra.Command {
	var remote, username, publicKey string
	cmd := &cobra.Command{
		Use:   "bind --remote <url> --username <name> --public-key <path>",
		Short: "Bind local config to an existing identity on a remote (e.g. after a repo move)",
		RunE: func(cmd *cobra.Command, args []string) error {
			if remote == "" || username == "" || publicKey == "" {
				return fmt.Errorf("--remote, --username, and --public-key are all required")
			}
			myKey, err := identity.ReadPublicKeyFile(expandHome(publicKey))
			if err != nil {
				return err
			}

			// Open directory lookup — no identity needed to read the public directory.
			cl, err := client.New(remote+"/_", nil)
			if err != nil {
				return err
			}
			entry, err := cl.LookupDirectory(username)
			if err != nil {
				return fmt.Errorf("look up %q on %s: %w", username, remote, err)
			}

			// Only bind if the directory's key for this username is the key you hold —
			// otherwise you would be pointing your config at someone else's identity.
			if entry.PublicKey != myKey {
				return fmt.Errorf("the key registered for %q on %s is not the key at %s; cannot bind",
					username, serverBase(remote), publicKey)
			}

			base := serverBase(remote)
			gc, err := config.LoadGlobal()
			if err != nil {
				return err
			}
			if gc.Remotes == nil {
				gc.Remotes = make(map[string]config.RemoteIdentity)
			}
			gc.Remotes[base] = config.RemoteIdentity{
				Username:    entry.Username,
				KeyPath:     identity.PrivateKeyPathFromPublic(expandHome(publicKey)),
				PrincipalID: entry.PrincipalID,
			}
			if err := config.SaveGlobal(gc); err != nil {
				return err
			}

			fmt.Printf("Bound %s at %s\n", entry.Username, base)
			fmt.Printf("principal: %s\n", entry.PrincipalID)
			return nil
		},
	}
	cmd.Flags().StringVar(&remote, "remote", "", "remote server URL")
	cmd.Flags().StringVar(&username, "username", "", "your username on the remote")
	cmd.Flags().StringVar(&publicKey, "public-key", "", "path to the ed25519 .pub file")
	return cmd
}
