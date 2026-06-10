package config

import (
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
	"github.com/guyweissman/agentstore/internal/brand"
)

const (
	defaultAddr                   = "127.0.0.1:8080"
	defaultMaxFileSizeBytes int64 = 100 * 1024         // 100 KB
	defaultMaxRepoSizeBytes int64 = 1024 * 1024 * 1024 // 1 GB
	defaultFreshnessSeconds int   = 300
)

// ServerConfig is parsed from server.toml in the data directory.
type ServerConfig struct {
	Server ServerSection `toml:"server"`
	Limits LimitsSection `toml:"limits"`
	Auth   AuthSection   `toml:"auth"`
}

// ServerSection holds network configuration.
type ServerSection struct {
	Addr string `toml:"addr"`
}

// LimitsSection holds resource limits.
type LimitsSection struct {
	MaxFileSizeBytes int64    `toml:"max_file_size_bytes"`
	MaxRepoSizeBytes int64    `toml:"max_repo_size_bytes"`
	AllowedFileTypes []string `toml:"allowed_file_types"`
}

// AuthSection holds authentication settings.
type AuthSection struct {
	RequestFreshnessSeconds int `toml:"request_freshness_seconds"`
}

// DefaultServerConfig returns a ServerConfig populated with all defaults.
func DefaultServerConfig() ServerConfig {
	return ServerConfig{
		Server: ServerSection{Addr: defaultAddr},
		Limits: LimitsSection{
			MaxFileSizeBytes: defaultMaxFileSizeBytes,
			MaxRepoSizeBytes: defaultMaxRepoSizeBytes,
			AllowedFileTypes: []string{"text/*"},
		},
		Auth: AuthSection{RequestFreshnessSeconds: defaultFreshnessSeconds},
	}
}

// LoadServer reads server.toml from the given data directory, merging over defaults.
// Missing file is not an error.
func LoadServer(dataDir string) (ServerConfig, error) {
	cfg := DefaultServerConfig()
	path := filepath.Join(dataDir, brand.ServerConfig)
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return cfg, nil
	}
	_, err := toml.DecodeFile(path, &cfg)
	return cfg, err
}
