// Package workspace handles interactions with the working tree: path normalization,
// directory scanning, and hashing files on disk.
package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/text/unicode/norm"

	"github.com/guyweissman/agentstore/internal/brand"
	"github.com/guyweissman/agentstore/internal/store"
)

// StorePath converts a host filesystem path to a store path (/-prefixed, forward slashes, NFC).
// repoRoot must be absolute.
func StorePath(repoRoot, hostPath string) (string, error) {
	abs, err := filepath.Abs(hostPath)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(repoRoot, abs)
	if err != nil {
		return "", err
	}
	if strings.HasPrefix(rel, "..") {
		return "", fmt.Errorf("%s is outside the repo", hostPath)
	}
	// Convert to forward slashes and prepend /.
	storePath := "/" + filepath.ToSlash(rel)
	// NFC normalize: macOS hands back NFD; normalize to NFC for cross-platform stability.
	return norm.NFC.String(storePath), nil
}

// HostPath converts a store path (/strategy/icp.md) to an absolute host path.
func HostPath(repoRoot, storePath string) string {
	return filepath.Join(repoRoot, filepath.FromSlash(strings.TrimPrefix(storePath, "/")))
}

// HashFile reads a file from disk and returns its object hash.
func HashFile(path string) (string, int64, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", 0, err
	}
	return store.HashContent(data), int64(len(data)), nil
}

// WalkRepo returns all file paths (as store paths) in the working tree,
// excluding the .agentstore/ directory.
func WalkRepo(repoRoot string) ([]string, error) {
	var paths []string
	err := filepath.WalkDir(repoRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		// Skip the .agentstore directory entirely.
		if d.IsDir() && d.Name() == brand.StoreDir {
			return filepath.SkipDir
		}
		if d.IsDir() {
			return nil
		}
		sp, err := StorePath(repoRoot, path)
		if err != nil {
			return err
		}
		paths = append(paths, sp)
		return nil
	})
	return paths, err
}
