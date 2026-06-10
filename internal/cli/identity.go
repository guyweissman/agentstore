package cli

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/guyweissman/agentstore/internal/brand"
	"github.com/guyweissman/agentstore/internal/client"
	"github.com/guyweissman/agentstore/internal/config"
	"github.com/guyweissman/agentstore/internal/identity"
)

// serverBase returns the scheme://host portion of a URL — the key used in the
// global config to map a remote to its local identity.
func serverBase(remoteURL string) string {
	u, err := url.Parse(remoteURL)
	if err != nil {
		return remoteURL
	}
	return u.Scheme + "://" + u.Host
}

// loadIdentity resolves the local signing identity for a remote URL from the
// global config (~/.agentstore/config).
func loadIdentity(remoteURL string) (*client.Identity, error) {
	base := serverBase(remoteURL)
	gc, err := config.LoadGlobal()
	if err != nil {
		return nil, err
	}
	id, ok := gc.Remotes[base]
	if !ok {
		return nil, fmt.Errorf("no identity for %s — run `%s register --remote %s --username <name> --public-key <path>`",
			base, brand.AppName, base)
	}
	if id.PrincipalID == "" {
		return nil, fmt.Errorf("identity for %s has no principal_id; re-run register", base)
	}
	priv, err := identity.LoadPrivateKey(expandHome(id.KeyPath))
	if err != nil {
		return nil, err
	}
	return &client.Identity{PrincipalID: id.PrincipalID, PrivateKey: priv}, nil
}

// expandHome expands a leading ~ to the user's home directory.
func expandHome(path string) string {
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err == nil {
			return filepath.Join(home, path[2:])
		}
	}
	return path
}
