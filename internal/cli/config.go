package cli

import (
	"bytes"
	"fmt"
	"os"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/spf13/cobra"

	"github.com/guyweissman/agentstore/internal/config"
)

func newConfigCmd() *cobra.Command {
	var global, local, list bool
	cmd := &cobra.Command{
		Use:   "config [--global|--local] <key> [value]",
		Short: "Get or set configuration values (or --list to show resolved config)",
		Long: `Read or write configuration in the plain-TOML config files.

  config --list                       show resolved config (local over global)
  config --global <key>               get a value from ~/.agentstore/config
  config --global <key> <value>       set a value in ~/.agentstore/config
  config --local <key>                get a value from .agentstore/config
  config --local <key> <value>        set a value in .agentstore/config

Keys are dotted paths into the TOML tree, e.g. "remotes.origin.url" or
"identity.principal_id". Per-remote identity in the global config is keyed by
remote URL (which contains dots); view it with --list and manage it with
"register" / "rekey" rather than by key.`,
		Args: cobra.MaximumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if list {
				return runConfigList()
			}
			if global == local { // both false or both true
				return fmt.Errorf("specify exactly one of --global or --local (or --list)")
			}
			if len(args) == 0 {
				return fmt.Errorf("specify a key to get, a key and value to set, or --list")
			}

			path, err := configPath(global)
			if err != nil {
				return err
			}
			if len(args) == 1 {
				return configGet(path, args[0])
			}
			return configSet(path, args[0], args[1])
		},
	}
	cmd.Flags().BoolVar(&global, "global", false, "operate on the global config (~/.agentstore/config)")
	cmd.Flags().BoolVar(&local, "local", false, "operate on this repo's config (.agentstore/config)")
	cmd.Flags().BoolVar(&list, "list", false, "show all resolved config")
	return cmd
}

// configPath returns the global or local config file path.
func configPath(global bool) (string, error) {
	if global {
		return config.GlobalConfigPath(), nil
	}
	root, err := repoRootFromCwd()
	if err != nil {
		return "", err
	}
	return config.RepoConfigPath(root), nil
}

// loadTOMLTree decodes a config file into a generic tree. A missing file yields
// an empty tree (not an error), so set can create it.
func loadTOMLTree(path string) (map[string]any, error) {
	tree := map[string]any{}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return tree, nil
	}
	if _, err := toml.DecodeFile(path, &tree); err != nil {
		return nil, fmt.Errorf("read config %s: %w", path, err)
	}
	return tree, nil
}

func configGet(path, key string) error {
	tree, err := loadTOMLTree(path)
	if err != nil {
		return err
	}
	segs := strings.Split(key, ".")
	cur := tree
	for i, seg := range segs {
		v, ok := cur[seg]
		if !ok {
			return fmt.Errorf("key %q not set", key)
		}
		if i == len(segs)-1 {
			fmt.Println(formatValue(v))
			return nil
		}
		next, ok := v.(map[string]any)
		if !ok {
			return fmt.Errorf("key %q not set", key)
		}
		cur = next
	}
	return nil
}

func configSet(path, key, value string) error {
	tree, err := loadTOMLTree(path)
	if err != nil {
		return err
	}
	segs := strings.Split(key, ".")
	cur := tree
	for _, seg := range segs[:len(segs)-1] {
		next, ok := cur[seg].(map[string]any)
		if !ok {
			next = map[string]any{}
			cur[seg] = next
		}
		cur = next
	}
	cur[segs[len(segs)-1]] = value

	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(tree); err != nil {
		return err
	}
	if err := writeConfigFile(path, buf.Bytes()); err != nil {
		return err
	}
	fmt.Printf("%s = %s\n", key, value)
	return nil
}

func runConfigList() error {
	// Global.
	if data, err := os.ReadFile(config.GlobalConfigPath()); err == nil && len(bytes.TrimSpace(data)) > 0 {
		fmt.Printf("# global (%s)\n%s\n", config.GlobalConfigPath(), strings.TrimRight(string(data), "\n"))
	}
	// Local (only when inside a repo).
	if root, err := repoRootFromCwd(); err == nil {
		p := config.RepoConfigPath(root)
		if data, err := os.ReadFile(p); err == nil && len(bytes.TrimSpace(data)) > 0 {
			fmt.Printf("\n# local (%s)\n%s\n", p, strings.TrimRight(string(data), "\n"))
		}
	}
	return nil
}

func formatValue(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case []any:
		parts := make([]string, len(x))
		for i, e := range x {
			parts[i] = fmt.Sprint(e)
		}
		return strings.Join(parts, ", ")
	default:
		return fmt.Sprint(x)
	}
}

func writeConfigFile(path string, data []byte) error {
	if err := os.MkdirAll(dirOf(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

func dirOf(path string) string {
	if i := strings.LastIndexByte(path, os.PathSeparator); i >= 0 {
		return path[:i]
	}
	return "."
}
