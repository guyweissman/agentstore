# AgentStore - a datastore for AI agents

**Git is for one agent. AgentStore is for agent teams.**

Git has become the storage option of choice for many AI agent users, harnesses and knowledge frameworks. It made a lot of sense when chatbots were just getting started - users were looking for ways to manage their work. Chatbots forced us into linear workflows, but we needed a multi-dimensional workspace to track prompts, revisit artifacts, chain outputs and more.

When users started managing prompts and skills Git became an even stronger fit for these code-like artifacts.

The AI agent datastore use case plays to Git's strengths:

- **Portable** - we want to own our agents' memory especially with something as sticky as a personal agent
- **Versioning and auditability** - both to counter the probabilistic nature of agents and to manage skills and prompts
- **Integration fit** - easily connect with agents like Codex or Claude Cowork, which are built to live in folders, and are also well versed in using Git

As we continue to push agents' capabilities we are starting to see new requirements. Git was built for human speed. Mixed multi-user/agent environments need finer grained permissions. Knowledge frameworks like GBrain (agent memory layer) want awareness of when things change to reduce stale knowledge.

The challenges are:

- **Repo-level permissions** - Any agent with push access can read and write every file. You cannot give a research agent access to `/customers/sanitized` without also exposing `/finance`
- **Write starvation** - Even when two agents modify completely different files, the second to push must pull, merge, and retry — because Git conflict detection is repo-level, not file-level. Agents work much faster than humans, and as agent teams grow collisions will increase
- **No live events** - Git is a passive store. Knowledge systems that need to react to changes must poll

Git isn't the only option, but all options today force a bad choice:

- **Git and open comparables** - portable, familiar, versioned — but repo-level permissions, no live events, designed for code
- **Cloud drives and note apps (Google Drive / Box / Notion)** - file permissions, real-time events — but proprietary, vendor lock-in, not agent-native
- **Version-controlled data (Dolt, lakeFS)** - versioned, branchable — but built to version data (SQL tables, data-lake objects), not files, which is where agents live

Git's strengths are: **portability + versioning + integration fit**

AgentStore adds: **file-level access control + non-blocking writes + real-time events**

I built AgentStore as a datastore for the agents I use in my work.

---

## Key features


|                          | Git | Cloud Drives | AgentStore |
| ------------------------ | --- | ------------ | ---------- |
| Portable / self-hostable | ✓   | ✗            | ✓          |
| Versioned history        | ✓   | limited      | ✓          |
| File-level permissions   | ✗   | ✓            | ✓          |
| Non-blocking writes      | ✗   | ✓            | ✓          |
| Real-time events         | ✗   | ✓            | ✓          |
| Agent-native workflow    | ✓   | ✗            | ✓          |
| Open storage format      | ✓   | ✗            | ✓          |


**File-level optimistic concurrency.** Bob pushing `b.md` is never blocked by Alice pushing `a.md`. Conflicts are scoped to the specific files in a commit, not the whole repo. Write starvation is eliminated.

**Real-time event stream.** Subscribe to a path with `store watch`. The server emits `file.created`, `file.modified`, `file.deleted`, and `commit.pushed` events over a persistent WebSocket when commits are accepted. Agents react to data changes instead of polling.

**Ed25519 keypair auth.** Every request is signed. No passwords, no server-minted secrets. The private key never leaves the machine.

**Portable self-hosted server.** A single static binary — easy to install. Run it on localhost for solo development or on any Linux/macOS machine for a team.

**Open storage format.** SQLite for metadata, content-addressed `objects/` for file content (SHA-256, git-compatible layout). Inspect, export, or migrate without proprietary tooling.

---

## Install

AgentStore is usually installed by an agent — start here.

### For agents

**Claude Code** — install the skill as a plugin (no clone required):

```
/plugin marketplace add guyweissman/agentstore
/plugin install agentstore@agentstore
```

And then ask Claude to install the AgentStore CLI.

**Other agents** — hand your assistant this instruction:

> Read [https://raw.githubusercontent.com/guyweissman/agentstore/main/internal/skill/content/SKILL.md](https://raw.githubusercontent.com/guyweissman/agentstore/main/internal/skill/content/SKILL.md) and follow it to install and use AgentStore.

The skill walks the agent through installing the `store` binary and the full read / contribute / sync workflow.

### For humans

**macOS / Linux — Homebrew**:

```sh
brew install guyweissman/tap/store
```

**Linux / macOS — prebuilt binary.** Downloads the right build for this machine and drops `store` on your `PATH`:

```sh
OS=$(uname -s | tr A-Z a-z); ARCH=$(uname -m); case $ARCH in x86_64) ARCH=amd64;; aarch64|arm64) ARCH=arm64;; esac
curl -fsSL "https://github.com/guyweissman/agentstore/releases/latest/download/store_${OS}_${ARCH}.tar.gz" | sudo tar -xz -C /usr/local/bin store
store version
```

**With Go** (no clone required):

```sh
go install github.com/guyweissman/agentstore/cmd/store@latest
```

Installs to `$(go env GOPATH)/bin` (usually `~/go/bin`) — make sure that's on your `PATH`. To build from a clone instead, see [Building from source](#building-from-source).

---

## Quickstart

### 1. Start a local server

```sh
store server start --data-dir ~/.agentstore/server-data &
```

### 2. Register an identity

```sh
mkdir -p ~/.agentstore
ssh-keygen -t ed25519 -N "" -f ~/.agentstore/id_ed25519
store register --remote http://127.0.0.1:8080 \
  --username alice \
  --public-key ~/.agentstore/id_ed25519.pub
```

### 3. Create a repo

```sh
cd ~
store init http://127.0.0.1:8080/my-workspace my-workspace
cd my-workspace
store whoami   # → alice
mkdir -p strategy
```

### 4. Watch for changes (optional - in another terminal)

```sh
cd ~/my-workspace
store watch /strategy &  # streams events under /strategy (a repo path)
```

### 5. Commit and push - watch the event fire

```sh
echo "# Strategy" > strategy/icp.md
store add strategy/icp.md
store commit -m "Add ICP draft"
store push
```

---

## Use AgentStore from an AI assistant

AgentStore ships an **agent skill** — a short guide that teaches an AI assistant
(Claude Code, Codex, and other runtimes that read `SKILL.md`) how to read, contribute
to, and stay in sync with a repo, including the conflict and permission failure modes
that otherwise trip agents up.

**Any assistant — export the skill and point your tool at it:**

```sh
store skill export ./agentstore-skill   # writes SKILL.md + reference/ (portable markdown)
store skill export --stdout             # or print SKILL.md to stdout
```

Move `agentstore-skill/` into wherever your assistant loads skills (for Claude Code:
`~/.claude/skills/`, or run `claude --plugin-dir ./agentstore-skill`).

**Claude Code — install from this repo as a marketplace (no clone required):**

```sh
/plugin marketplace add guyweissman/agentstore
/plugin install agentstore@agentstore
```

Both paths serve the **same content**, generated from the live CLI so the reference
never drifts. Regenerate after changing commands with `go generate ./internal/skill`.

---

## CLI reference

### Store setup

```sh
store init <url> [<directory>]      # create a repo on a server and check it out locally
store clone <url> [<directory>]     # download a remote repo (permission-filtered)
store remote add <name> <url>       # add a remote
store remote list                   # list remotes
store config --global <key> [value] # get/set global config
store config --local  <key> [value] # get/set per-repo config
```

### File operations

```sh
store add <path>                    # stage a file or glob
store add .                         # stage all changes
store rm <path>                     # stage a deletion
store status                        # show staged/unstaged changes
store diff [<path>]                 # unstaged diffs
store diff --staged [<path>]        # staged diffs
```

### Committing and syncing

```sh
store commit -m "<message>"         # commit staged changes
store push [<remote>]               # push to remote (file-level OCC)
store pull [<remote>]               # fetch + merge (3-way per file)
store merge --abort                 # discard an in-progress merge
store reset                         # discard all unpushed commits
store push <remote> --mirror        # admin only: full bootstrap to an empty remote
```

### History

```sh
store log [<path>]                  # commit history, optionally scoped to a file
store log -n <N>                    # most recent N commits
store log --author <principal>      # filter by author
store log --since <ISO-date>        # commits at or after a time
store log --to <ISO-date>           # commits at or before a time
store log --cursor <token>          # commits after a sync cursor (agent sync primitive)
store log --to-cursor <token>       # commits up to a sync cursor
store log --reverse                 # oldest first (for ordered delta replay)
store log --json                    # machine-readable output
store show <commit_id>              # show a specific commit
store checkout <commit|seq> <path>  # restore a file to a prior version
store checkout <commit|seq> .       # rewind the entire repo (non-destructive; admins and owners of /* only)
```

### Permissions

```sh
store grant <principal> <permission> <path>   # read / write / owner on a path
store revoke <principal> <path>               # remove a grant
store permissions <path>                      # list effective permissions
```

### Real-time events

```sh
store watch [<path>]                          # stream events under a path (JSON; defaults to /)
store watch --events <type,...>               # filter by event type
store watch --cursor <token>                  # resume from a sync cursor
```

Event types: `file.created`, `file.modified`, `file.deleted`, `commit.pushed`

### Identity

```sh
store register --remote <url> --username <name> --public-key <path>
store bind --remote <url> --username <name> --public-key <path>   # reconnect to an existing identity (e.g. after a repo move)
store whoami                                  # confirm authenticated identity
store rekey --public-key <path>               # rotate your public key
store principals list [--remote <url>]        # browse a directory
```

### Membership and admin

```sh
store principals add <username>               # admin only: add a principal to this repo
store principals remove <username>            # admin only: remove (cascades grants)
store admin add <principal>                   # grant admin role
store admin revoke <principal>                # revoke admin role
store admin list                              # list admins
```

### Server

```sh
store server start [--addr <addr>] [--data-dir <path>]
store server stop
```

### Agent skill

```sh
store skill export [<dir>]                    # write the agent skill (default: ./agentstore-skill)
store skill export --stdout                   # print SKILL.md to stdout
```

---

## Permissions model

Grants are file-level and hierarchical. A grant on `/strategy/*` covers every file under `/strategy`. Effective permission on a path is the maximum level across all matching grants.

Three levels: `read` < `write` < `owner`. An `owner` of a path may grant/revoke within its subtree. A repo `admin` may grant/revoke on any path and controls who is a repo member.

```sh
# A research agent reads sanitized customer data but cannot touch finance
store grant research-agent read /customers/sanitized/*

# A writer agent can update strategy files
store grant writer-agent write /strategy/*
```

Grants take effect immediately on the server and are retroactive: a `revoke` removes access to history, not just future commits. Repos are private by default — a new member sees nothing until granted a path.

---

## Use cases

### Secure multi-agent workspaces

Give each agent least-privilege access. A summarization agent can read `/research/raw` and write `/research/summaries` without seeing `/finance` or `/legal`. Enforced by the server, not convention.

### Reactive knowledge graph synthesis

Subscribe to source paths with `watch`. When a file changes, call `log --cursor <last_cursor> --json` to get only the delta and re-synthesize only what changed — no polling, no full re-scan.

```sh
store watch /knowledge-base --cursor 4257
# → receives file.modified for /knowledge-base/customers.md at seq 4260
store log --cursor 4257 --to-cursor 4260 --json
# → fetch and apply only the delta
```

### Concurrent multi-agent workflows

Multiple agents push to non-overlapping file paths simultaneously. No artificial serialization — each push is checked only against the specific files it touches.

### Portable knowledge bases

Self-host on any machine. Move a repo to a new server with `push --mirror` — history, grants, roles, and the member roster all travel with it. `log --cursor` consumers resume without a gap.

---

## Architecture overview

```
<data-dir>/
  server.db          # server-wide identity directory
  <repo>/
    store.db         # SQLite: commits, file_branch_heads, grants, principals, roles
    objects/         # content-addressed file content (SHA-256)
```

**Content-addressed object store.** Identical to git's layout (`objects/<hash[0:2]>/<hash[2:]>`), using SHA-256. Objects are immutable; deduplication is free.

**SQLite metadata.** Commit log, per-file version heads, access-control grants, and the member roster. WAL mode for concurrent reads during writes.

**File-level OCC.** On push, each file carries its `based_on_commit_id`. The server atomically checks that every file's base still matches the current head. If all match, the commit is accepted and heads are updated. If any file has been modified since the base, the whole commit is rejected with the conflicting file names and current head commit IDs. Unrelated files never block each other.

**In-memory event hub.** When a push is accepted, the server fans out events directly from the in-memory commit (zero extra reads). Each subscriber gets a bounded buffer; a slow subscriber is dropped and recovers via `log --cursor` on reconnect. Permission filtering is evaluated against current grants at publish time, not at subscribe time.

**Per-request signing.** Every HTTP request is signed over `(principal_id, method, request_path, body_hash, timestamp)` with ed25519. The WebSocket handshake is signed once; the connection is then trusted for its lifetime. No sessions, no server-stored secrets.

For full technical details see `[project_spec/architecture.md](project_spec/architecture.md)`.

---

## v0.1 scope

- Text files only (`.md`, `.txt`, `.json`, `.yaml`, `.toml`, `.csv`, and similar plain text)
- Maximum file size: 100 KB (configurable)
- Maximum repo size: 1 GB (configurable)
- macOS and Linux, amd64 and arm64
- Single static binary

---

## Building from source

**Prerequisites:** [Go 1.25+](https://go.dev/dl/).

Install the latest tagged build directly:

```sh
go install github.com/guyweissman/agentstore/cmd/store@latest
```

Or clone and build, for working on AgentStore itself:

```sh
git clone https://github.com/guyweissman/agentstore.git
cd agentstore
go build -o store ./cmd/store   # build the binary into ./store
go test ./...                   # run the tests
go vet ./...                    # static analysis
```

---

## License

Released under the [MIT License](LICENSE).