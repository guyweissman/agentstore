# store CLI reference

Generated from the command tree — do not edit by hand. Run `store <command> --help` for the live version.

## `store`

Portable, file-native datastore for agent knowledge work

## `store add <path>...`

Stage files for the next commit

## `store admin add <username>`

Grant the repo-admin role (admin only)

## `store admin list`

List this repo's admins

## `store admin revoke <username>`

Revoke the repo-admin role — refuses to remove the last admin (admin only)

## `store bind --remote <url> --username <name> --public-key <path> [flags]`

Bind local config to an existing identity on a remote (e.g. after a repo move)

Flags:

- `--public-key` — path to the ed25519 .pub file
- `--remote` — remote server URL
- `--username` — your username on the remote

## `store checkout <commit|seq> <path|.>`

Restore a file (or the whole repo) to a prior version

## `store clone <url> [<directory>]`

Download a remote repo locally

## `store commit [flags]`

Commit staged changes

Flags:

- `-m, --message` — commit message

## `store config [--global|--local] <key> [value] [flags]`

Get or set configuration values (or --list to show resolved config)

Flags:

- `--global` — operate on the global config (~/.agentstore/config)
- `--list` — show all resolved config
- `--local` — operate on this repo's config (.agentstore/config)

## `store diff [<path>] [flags]`

Show unstaged diffs (or --staged for staged diffs)

Flags:

- `--staged` — diff staged changes against HEAD

## `store grant <principal> <permission> <path>`

Set a principal's access level on a path

## `store init <url> [<directory>]`

Create a new repo on the server and check it out locally

## `store log [<path>] [flags]`

Show commit history

Flags:

- `--author` — filter by author principal ID
- `--cursor` — commits after this seq cursor
- `--json` — machine-readable JSON output
- `-n, --number` — limit to the most recent N commits
- `--reverse` — show oldest first
- `--since` — commits at or after this ISO-8601 date
- `--to` — commits at or before this ISO-8601 date
- `--to-cursor` — commits up to this seq cursor

## `store merge [flags]`

Manage in-progress merges

Flags:

- `--abort` — discard an in-progress merge and restore the last committed state

## `store permissions <path>`

List effective permissions on a path

## `store principals add <username>`

Add a directory principal to this repo (admin only)

## `store principals list [--remote <url>] [flags]`

List this repo's members, or browse a remote's directory with --remote

Flags:

- `--remote` — browse this remote's directory instead of repo members

## `store principals remove <username>`

Remove a member from this repo (admin only)

## `store pull [<remote>] [flags]`

Fetch remote commits and merge them

Flags:

- `--remote` — remote name (default: origin)

## `store push [<remote>] [flags]`

Push the local commit to the remote (or --mirror to relocate the repo)

Flags:

- `--mirror` — admin only: relocate the repo to an EMPTY remote (full history, grants, roles, roster)
- `--remote` — remote name (default: origin)

## `store register --remote <url> --username <name> --public-key <path> [flags]`

Register an identity in a remote's open directory

Flags:

- `--public-key` — path to the ed25519 .pub file
- `--remote` — remote server URL
- `--username` — username to register

## `store rekey --public-key <path> [flags]`

Rotate your own public key on a remote

Flags:

- `--public-key` — path to the new ed25519 .pub file
- `--remote` — remote server URL (defaults to origin)

## `store remote add <name> <url>`

Add a named remote

## `store remote list`

List configured remotes

## `store remote remove <name>`

Remove a named remote

## `store reset`

Discard all unpushed commits and restore to the last pushed state

## `store revoke <principal> <path>`

Remove a principal's grant on a path

## `store rm <path>`

Stage a file deletion

## `store server start [flags]`

Start the server in the foreground

Flags:

- `--addr` — bind address (overrides server.toml; default 127.0.0.1:8080)
- `--data-dir` — server data directory

## `store server stop [flags]`

Signal the running server to shut down gracefully

Flags:

- `--data-dir` — server data directory

## `store show <commit>`

Show a commit and its changes

## `store skill export [<dir>] [flags]`

Write the skill (SKILL.md + reference) to a directory for any agent runtime

Flags:

- `--stdout` — print SKILL.md to stdout instead of writing files

## `store status`

Show staged and unstaged changes

## `store version`

Print version information

## `store watch [<path>] [flags]`

Stream live events under a path (JSON)

Flags:

- `--cursor` — resume from this seq cursor
- `--events` — comma-separated event types to filter (default: all)

## `store whoami [flags]`

Show the principal the remote authenticates you as

Flags:

- `--remote` — remote server URL (defaults to origin)

