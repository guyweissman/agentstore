package config

import (
	"bytes"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
	"github.com/guyweissman/agentstore/internal/brand"
)

// RemoteIdentity holds per-remote authentication config from the global config file.
type RemoteIdentity struct {
	Username    string `toml:"username"`
	KeyPath     string `toml:"key_path"`
	PrincipalID string `toml:"principal_id"`
}

// RepoRemote holds the URL for a named remote in a per-repo config file.
type RepoRemote struct {
	URL string `toml:"url"`
}

// GlobalConfig is parsed from ~/.agentstore/config.
type GlobalConfig struct {
	// Remotes maps remote URL to its identity config: [remotes."https://..."]
	Remotes map[string]RemoteIdentity `toml:"remotes"`
}

// RepoConfig is parsed from .agentstore/config (per-clone, never pushed).
type RepoConfig struct {
	// Remotes maps remote name to URL: [remotes.origin]
	Remotes  map[string]RepoRemote `toml:"remotes"`
	Identity RepoIdentity          `toml:"identity"`
}

// RepoIdentity records which principal this clone belongs to, so local commits
// are authored by the right principal without consulting the global config.
// Local-only (never pushed); set at init/clone time.
type RepoIdentity struct {
	PrincipalID string `toml:"principal_id"`
}

// LoadGlobal reads ~/.agentstore/config. Missing file is not an error.
func LoadGlobal() (GlobalConfig, error) {
	var cfg GlobalConfig
	path := filepath.Join(globalConfigDir(), brand.ConfigFile)
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return cfg, nil
	}
	_, err := toml.DecodeFile(path, &cfg)
	return cfg, err
}

// LoadRepo reads .agentstore/config from the given repo root. Missing file is not an error.
func LoadRepo(repoRoot string) (RepoConfig, error) {
	var cfg RepoConfig
	path := filepath.Join(repoRoot, brand.StoreDir, brand.ConfigFile)
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return cfg, nil
	}
	_, err := toml.DecodeFile(path, &cfg)
	return cfg, err
}

// SaveGlobal writes the GlobalConfig to ~/.agentstore/config.
func SaveGlobal(cfg GlobalConfig) error {
	dir := globalConfigDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(cfg); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, brand.ConfigFile), buf.Bytes(), 0o600)
}

// SaveRepo writes a RepoConfig to .agentstore/config in the repo root.
func SaveRepo(repoRoot string, cfg RepoConfig) error {
	dir := filepath.Join(repoRoot, brand.StoreDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(cfg); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, brand.ConfigFile), buf.Bytes(), 0o644)
}

func globalConfigDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, brand.GlobalDirName)
}

// GlobalConfigPath is the path to the global config file (~/.agentstore/config).
func GlobalConfigPath() string {
	return filepath.Join(globalConfigDir(), brand.ConfigFile)
}

// RepoConfigPath is the path to a clone's local config file (.agentstore/config).
func RepoConfigPath(repoRoot string) string {
	return filepath.Join(repoRoot, brand.StoreDir, brand.ConfigFile)
}
