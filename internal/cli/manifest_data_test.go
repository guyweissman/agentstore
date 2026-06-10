package cli_test

// Editorial layer for the coverage manifest: the PRD-ordered list of coverage
// units and their grouping. prdOrder must stay in sync with the real CLI surface
// (enforced by TestCoverageManifestMatchesCLI) and is kept in the same order as
// the "Full CLI command set" section of project_spec/agentstore-prd.md so the
// generated manifest.json can be eyeballed against the PRD.

var prdOrder = []string{
	// Store setup
	"init",
	"clone",
	"remote add",
	"remote remove",
	"remote list",
	"config",
	"config --global",
	"config --local",
	"config --list",
	// File operations
	"add",
	"rm",
	"status",
	"diff",
	"diff --staged",
	// Committing and syncing
	"commit",
	"commit --message",
	"push",
	"push --remote",
	"push --mirror",
	"pull",
	"pull --remote",
	"merge",
	"merge --abort",
	"reset",
	// History and versions
	"log",
	"log --number",
	"log --author",
	"log --since",
	"log --to",
	"log --cursor",
	"log --to-cursor",
	"log --reverse",
	"log --json",
	"show",
	"checkout",
	// Permissions
	"grant",
	"revoke",
	"permissions",
	// Real-time events
	"watch",
	"watch --events",
	"watch --cursor",
	// Identity
	"register",
	"register --remote",
	"register --username",
	"register --public-key",
	"bind",
	"bind --remote",
	"bind --username",
	"bind --public-key",
	"whoami",
	"whoami --remote",
	"rekey",
	"rekey --remote",
	"rekey --public-key",
	"principals list --remote",
	// Membership
	"principals add",
	"principals list",
	"principals remove",
	// Admin role
	"admin add",
	"admin list",
	"admin revoke",
	// Server
	"server start",
	"server start --addr",
	"server start --data-dir",
	"server stop",
	"server stop --data-dir",
	// Agent skill
	"skill export",
	"skill export --stdout",
	// Meta
	"version",
}

// commandGroup maps a command path to its PRD section.
var commandGroup = map[string]string{
	"init":              "Store setup",
	"clone":             "Store setup",
	"remote add":        "Store setup",
	"remote remove":     "Store setup",
	"remote list":       "Store setup",
	"config":            "Store setup",
	"add":               "File operations",
	"rm":                "File operations",
	"status":            "File operations",
	"diff":              "File operations",
	"commit":            "Committing and syncing",
	"push":              "Committing and syncing",
	"pull":              "Committing and syncing",
	"merge":             "Committing and syncing",
	"reset":             "Committing and syncing",
	"log":               "History and versions",
	"show":              "History and versions",
	"checkout":          "History and versions",
	"grant":             "Permissions",
	"revoke":            "Permissions",
	"permissions":       "Permissions",
	"watch":             "Real-time events",
	"register":          "Identity",
	"bind":              "Identity",
	"whoami":            "Identity",
	"rekey":             "Identity",
	"principals add":    "Membership",
	"principals list":   "Membership",
	"principals remove": "Membership",
	"admin add":         "Admin role",
	"admin list":        "Admin role",
	"admin revoke":      "Admin role",
	"server start":      "Server",
	"server stop":       "Server",
	"skill export":      "Agent skill",
	"version":           "Meta",
}

// groupOverride pins specific unit ids to a different group than their command.
// `principals list --remote` is an Identity operation (browse a remote directory)
// even though bare `principals list` is Membership.
var groupOverride = map[string]string{
	"principals list --remote": "Identity",
}
