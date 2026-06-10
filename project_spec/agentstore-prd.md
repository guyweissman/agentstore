# AgentStore

## Project Description

**A portable datastore with file-level access control and live events for agent knowledge work**

AgentStore is built for multi-user, multi-agent knowledge work environments: agent workspaces, prompts, skills, and knowledge frameworks. It's a Git-shaped datastore with file-level access control and real-time events — the missing layer between "everyone shares one Git repo" and "lock everything behind a proprietary cloud."

---

## The problem

Git has become the storage option of choice for many AI agent users and harnesses / tools. It made a lot of sense as we started working on bigger projects and wanted our agents to work on multiple files coupled with actions (coding / web research / etc). Even more so with the development of skills and prompts which have a code-like nature.

The use case plays to its strengths:
- **Portable** - users want to own their memory especially with something as sticky as a personal agent
- **Versioning and auditability** - both to counter the probabilistic nature of agents and to manage skills and prompts
- **Integration fit** - easily connect with agents like Codex or Claude Cowork, which are built to live in folders

But Git will start reaching a wall when we move into multi-user, multi-agent environments. Git isn't the only option, but current options force a bad choice:

- **Git** — self-hosted, familiar, versioned — but repo-level permissions, no live events, designed for code
- **Network drives and note apps (Google Drive / Box / Notion)** — file permissions, real-time events — but proprietary, vendor lock-in, not agent-native
- **Version-controlled data (Dolt, lakeFS)** — versioned, branchable — but built to version data (SQL tables, data-lake objects), not files, which is where agents live

Git's strengths are: **portability + versioning**

AgentStore adds: **file-level access control + non-blocking writes + real-time events**

---

## Core data model

```
file  →  commit  →  event
```

Three primitives:

- **Files** have individual permissions and history.
- **Commits** group multi-file changes when needed, like a git commit spanning files. **A (commit, path) pair is a file version.**
- **Events** are the reactive surface agents subscribe to.

---

## Interface design

### Principle
Git-shaped, not Git-compatible. The workflow (add, commit, push, watch, diff, log) is familiar to developers and LLMs. The semantics are agent-native.

### Full CLI command set

**Store setup**
```
store init <url> [<directory>]      # create a new EMPTY repo on a server and check it out
                                         # locally; you become its first admin and owner of /*.
                                         # <url> = server + repo name, e.g.
                                         # https://store.example.com/strategy, or
                                         # http://127.0.0.1:8080/strategy for a local server.
                                         # Requires the server reachable at creation time, and
                                         # registers you in its directory if you are not known yet.
                                         # NOTE: diverges from git — there is no local-only repo;
                                         # every repo is server-hosted (the server may be local).
                                         # After init, add/commit are offline; push when reachable.
store clone <url> [<directory>]     # download a remote repo locally, set it as origin,
                                         # and check out HEAD; permission-filtered — only files
                                         # the principal has read access to are downloaded
store remote add <name> <url>       # add a remote server
store remote remove <name>          # remove a remote
store remote list                   # list configured remotes
store config --global <key> [value] # get or set a value in ~/.agentstore/config
store config --local <key> [value]  # get or set a value in .agentstore/config
store config --list                 # show all resolved config (local overrides global)
```

**File operations**
```
store add <path>                    # stage a file or glob
store add .                         # stage all changes in the entire repo
store rm <path>                     # stage a file deletion
store status                        # show staged/unstaged changes, and any
                                         # unresolved merge conflicts after a pull
store diff [<path>]                 # show unstaged diffs
store diff --staged [<path>]        # show staged diffs
```

**Committing and syncing**
```
store commit -m "<message>"         # commit staged changes; error if nothing staged
store push [<remote>]               # push commits to remote; error if nothing to push
                                         # NOTE: significantly different from git — uses
                                         # file-level optimistic concurrency, not repo-level
                                         # fast-forward checks (see use cases / architecture)
store pull [<remote>]               # fetch latest commits and merge them in.
                                         # Files only changed remotely fast-forward;
                                         # files changed on both sides get a 3-way merge.
                                         # Overlapping edits write git-style conflict
                                         # markers into the file (resolve, then add + commit
                                         # to produce a merge commit). Aborts if uncommitted
                                         # local changes would be overwritten (commit first).
store merge --abort                 # discard an in-progress merge after a conflicting
                                         # pull; restore the last committed state
store reset                         # discard ALL unpushed local commits and restore the
                                         # working tree to the last server-confirmed state.
                                         # Unlike git reset, there is no HEAD~1 variant —
                                         # reset always returns to the last pushed state.
                                         # Staged changes must be committed or cleared first.
                                         # NOTE: this operation cannot be undone.
store push <remote> --mirror        # admin only: full bootstrap upload to an EMPTY remote
                                         # — all objects, history, grants/roles and the member
                                         # roster, preserving commit IDs and seq. Used to move a
                                         # repo to a new server (see "Moving a repo"). Refuses a
                                         # non-empty target.
```

**History and versions**
```
store log [<path>]                  # show commit history, optionally scoped to a file
store log -n <number>               # limit to the most recent N commits
store log --author <principal>      # filter by author
store log --since <ISO-date>        # commits at or after a wall-clock time
store log --to <ISO-date>           # commits at or before a wall-clock time
store log --cursor <token>          # commits after an opaque sync cursor
store log --to-cursor <token>       # commits up to an opaque sync cursor
store log --reverse                 # oldest-first (for ordered delta replay)
store log --json                    # machine-readable json output (default is human-readable)
store show <commit_id>              # show a specific commit and its changes
store checkout <commit|seq> <path>  # restore a single file to a prior version and stage it.
                                         # The reference may be a commit ID (full or abbreviated)
                                         # or a server seq number (as shown in `log`). Finds the
                                         # most recent version of the path at or before that point.
store checkout <commit|seq> .       # restore the ENTIRE repo to a prior point and stage all
                                         # resulting changes (modifications, restores, deletions),
                                         # leaving you to commit. This is a non-destructive rewind:
                                         # it never rewrites history — the restore lands as a new
                                         # commit, so `log --cursor` consumers and other clients see
                                         # it like any other change. Use it to abandon recent work
                                         # and return the whole repo to an earlier state.
                                         # RESTRICTED: admins and owners of /* only (the operation
                                         # spans every path, so it requires repo-wide write authority).
                                         # Must be run from the repo root — "." always means the WHOLE
                                         # repo, never the current subfolder (diverges from git, which
                                         # treats "." as cwd-relative; subtree scoping is post-v0.1).
                                         # Prompts for typed confirmation.
                                         # New files (staged via `add` but not yet committed, hence
                                         # absent at the target point) are NOT deleted: they are
                                         # unstaged and left on disk as untracked, so the rewind commit
                                         # matches the target exactly without destroying uncommitted
                                         # work. Untracked files (never added) are left untouched.
                                         # If unpushed local commits exist, checkout warns (they would
                                         # be pushed then undone) and suggests `reset` to discard them.
                                         # NOTE: unlike git, checkout ONLY restores content (to disk,
                                         # and staged) — there is no branch switching (no branches in
                                         # v0.1). checkout always stages, matching `git checkout <ref> -- <path>`.
                                         # NOTE: the agent sync primitive is `log --cursor <token> --json`
                                         # — git has no `changes` command; log does this work.
```

**Permissions**
```
store grant <principal> <permission> <path>   # set access level (read/write/owner) on the path;
                                         # re-granting a different level replaces it (raise or lower)
store revoke <principal> <path>               # remove the principal's grant on the path entirely
                                         # (not a downgrade — use grant to lower a level)
store permissions <path>                      # list effective permissions on a path
                                         # <path> is an exact path (/strategy/icp.md) or a
                                         # prefix (/strategy/*). v0.1 has no interior
                                         # wildcards like /customers/*/sanitized.
                                         # NOTE: no git parallel — git has no in-repo access
                                         # control; permissions live in the hosting layer.
                                         # An owner of a path may grant/revoke on it; a repo
                                         # admin may grant/revoke any permission on any path.
                                         # grant/revoke take effect immediately on the server
                                         # (not staged/committed). Access is enforced against
                                         # the current grants — a revoke is retroactive across
                                         # all versions, not just future ones.
```

**Real-time events**
```
store watch [<path>]                          # stream all events under a path (JSON; defaults to /)
store watch --events <event,...>              # filter by event type
store watch --cursor <token>                  # resume the stream from a sync cursor
                                         # NOTE: no git parallel — git is a passive store and
                                         # never notifies. Historical query is `log`; `watch`
                                         # is the live counterpart, resumable from a cursor.
                                         # Output is JSON only (no git-comparable to mirror).
```

**Identity**
```
store register --remote <url> --username <name> --public-key <path>
                                         # self-register a NEW identity in the remote's open directory
                                         # (mints a fresh principal_id). Fails if the username is taken.
store bind --remote <url> --username <name> --public-key <path>
                                         # bind local config to an EXISTING identity the remote already
                                         # knows for your key — the counterpart to register. Used after a
                                         # repo move: `push --mirror` seeds the new server's directory with
                                         # your original (principal_id, username, public_key), and `bind`
                                         # points local config at that preserved principal_id instead of
                                         # minting a new one. Safe: you name a username and the server
                                         # returns the id bound to it; it only succeeds if the key the
                                         # directory holds for that username is the key at <path> — you
                                         # prove who you are with the key, you never get to declare it.
store principals list --remote <url>          # browse a remote's directory (what admins add from)
store whoami                                  # show the principal the remote authenticates you as
store rekey --public-key <path>               # rotate YOUR OWN public key in the directory of the
                                         # remote this repo points to — effective across all repos on
                                         # that remote. Self only; admins cannot rekey others.
                                         # NOTE: identity is an ed25519 keypair generated OUTSIDE
                                         # store (ssh-keygen) — only the public key is ever sent.
                                         # The remote directory is open (like a GitHub account); having
                                         # an identity grants access to nothing until a repo admin adds you.
                                         # `whoami` proves identity is key-derived, not config-asserted.
```

**Membership**
```
store principals add <username>               # admin only: add a directory principal to this repo
                                         # (copies username + public key in) — this IS the repo invitation
store principals list                         # list this repo's members
store principals remove <username>            # admin only: remove from repo (cascades grants)
                                         # NOTE: no git parallel. Repos are private by default; the only
                                         # way in is an admin adding you. Membership ≠ access — a new
                                         # member sees nothing until granted a path (see Permissions).
```

**Admin role**
```
store admin add <principal>                   # admin only: grant the repo-admin role
store admin revoke <principal>                # admin only: revoke it — refuses to remove the LAST admin
store admin list                              # list this repo's admins
                                         # NOTE: no git parallel. Admin is a repo-level role ORTHOGONAL to
                                         # path grants: it controls membership + the admin role itself, and
                                         # carries full data access. A path `owner` can grant/revoke within
                                         # its subtree but cannot add principals — that is admin-only.
```

**Server**
```
store server start                            # run the server in the foreground; binds loopback
                                         # (127.0.0.1) by default — use --addr to expose it and
                                         # --data-dir for the store path. Ctrl-C / SIGTERM drains
                                         # gracefully: stop accepting new requests, finish in-flight
                                         # ones, close watch sockets, checkpoint the DB, then exit.
store server stop                             # signal a running instance to drain and shut down
                                         # gracefully (via the pidfile in its data dir)
```

**Skill**
```
store skill export [<dir>]                    # export the agent skill (SKILL.md + reference) so an
                                         # AI assistant can learn to use the store; defaults to
                                         # ./agentstore-skill.
store skill export --stdout                   # print SKILL.md to stdout instead of writing files
```

**Meta**
```
store version                                 # print version, commit, and build date
```

### Key semantic refinements over git

**File-level permission grants:**
```
store grant research-agent read /customers/sanitized/*
store grant writer-agent write /strategy/*
```

**Reactive subscriptions:**
```
store watch /strategy
store watch --events commit.pushed,file.modified
```

**Commit log queries:**
```
store log --cursor 4257 --json                  # everything after an opaque sync cursor
store log --since 2026-05-31T10:00:00Z --json   # everything at or after a wall-clock time
```

**Concurrent edits to different files — no conflict:**
```
# Alice makes changes to strategy/positioning.md
vi strategy/positioning.md
store add strategy/positioning.md
store commit -m "Sharpen positioning"
store push

# Bob is working only on docs/onboarding.md — a different file.
vi docs/onboarding.md                                # edit the setup steps, :wq
store add docs/onboarding.md
store commit -m "Clarify onboarding steps"
store push                                      # accepted immediately —
                                                     # Alice's edit to positioning.md is irrelevant
```

**Conflict & merge — both edited the same file:**
```
# Alice makes changes to strategy/positioning.md
vi strategy/positioning.md
store add strategy/positioning.md
store commit -m "Sharpen positioning"
store push

# Bob edits a file Alice is also editing
vi strategy/positioning.md                           # rewrite the headline, :wq
store add strategy/positioning.md
store commit -m "Additional changes"
store push
# → rejected — conflict: strategy/positioning.md updated by @alice in 7c21be
store pull                                      # fetch + 3-way merge; markers written into the file
store status                                    # unresolved: strategy/positioning.md (both modified)
vi strategy/positioning.md                           # resolve <<<< / ==== / >>>> markers, :wq
store add strategy/positioning.md
store commit -m "Merge positioning edits"       # 2-parent merge commit
store push                                      # accepted
```

### Event vocabulary
The server only emits events for things it actually observes — i.e. state changes that reach the server. Every event corresponds to a server-side mutation that has been accepted and is now visible to other principals.

```
file.created          # an accepted commit added a file
file.modified         # an accepted commit changed a file's content
file.deleted          # an accepted commit removed a file
commit.pushed         # a commit was accepted into the store (carries id, author, message)
```

The file-level events are derived from the `change_type` of each file in an accepted commit. Watching a path is hierarchical — `watch /strategy` receives events for every file beneath it, so there is no separate `folder.changed` event. Local-only actions (staging, a local commit before push) emit nothing: the server never sees them, so there is no subscriber to notify. A rejected push is a synchronous error to the pusher, not a broadcast event. Permission changes are not events (control-plane state, re-evaluated per request).

### Authentication and identity

A **principal** is the unit of identity, named by a stable `principal_id` (a UUID) that grants and roles key on — never on the username or key, so both can change without breaking access. Authentication is by an **ed25519 keypair**, generated outside AgentStore with `ssh-keygen`. AgentStore only ever ingests the public key — the private key never leaves the principal's machine, and rotating it (`rekey`) leaves the identity intact. This mirrors how git delegates authentication to SSH: there is no password to transmit, no server-minted secret to leak over a chat channel, and onboarding over an untrusted channel is safe because only a public key is ever shared.

**Open directory, private repos.** A principal self-registers a `username` + public key with a remote (`register`), which mints a fresh `principal_id`. The remote's directory is **open** — like a GitHub account, having an identity grants access to nothing. Repos are **private by default**; the only way into one is a repo admin adding you (`principals add`). That act *is* the invitation — there is no separate invite token. A separate verb, `bind`, points local config at an *existing* directory identity for your key (used after a move, where your `principal_id` was seeded by the mirror); it proves key ownership and returns the bound id, but cannot create or claim an id you don't hold the key for.

**Sign every request.** AgentStore has no persistent authenticated session. Every HTTP request **asserts** a `principal_id` and is signed with the private key (over method, path, body hash, and a freshness timestamp); the server looks up that principal's registered public key and accepts the request only if the signature verifies against it. So the asserted id is the identity and the key is its *proof* — never its source. Two consequences follow: a key may legitimately back a returning identity after a move (the basis of `bind`), and you can never authenticate as another principal, because you cannot produce their private key. The one long-lived connection, the `watch` WebSocket, signs its connection request and is then authenticated for its lifetime.

**Two planes of authority.** Repo `admin` (control plane) is a repo-level role, orthogonal to path grants: it governs membership and the admin role itself, and carries full data access. Path `owner` (data plane) can grant/revoke within its subtree but cannot add principals. So admins control *who is in the repo*; owners control *what members can touch*. There can be many path-owners but few admins; `init` makes the first principal the first admin, and `admin revoke` refuses to remove the last one. Because admins carry full data access, they are also the data-plane recovery authority: an admin may grant or revoke any permission on any path (including to or from themselves), so no path can ever be orphaned with no owner — not even `/*`.

**Self-hosted.** The remote's directory is the live authentication source, so a key rotation (`rekey`) is a single per-remote operation that takes effect across every repo on that remote. For self-hosted capability the repo also embeds a synced snapshot of its members' `(principal_id, username, public_key)`; grants and roles key on the stable `principal_id` (a UUID), never on the username or key. Moving a repo to a new remote seeds that remote's directory from the snapshot, so nothing dangles — only a username collision is possible (a cosmetic conflict resolved by auto-rename).

**Scope for v0.1.** Identity binds one principal per OS-user-per-remote — the same default as git over SSH. Running several distinct principals under one OS account (e.g. multiple agents that should each have their own provenance and grants) arrives with PATs in future versions; until then, isolate them with separate OS users or config homes.

**Onboarding, end to end:**
```
# new principal, once:
ssh-keygen -t ed25519 -f ~/.agentstore/id_ed25519
store register --remote https://store.acme.com --username alice --public-key ~/.agentstore/id_ed25519.pub

# a repo admin, elsewhere:
store principals add alice
store grant alice write /strategy

# new principal:
store clone https://store.acme.com/brand         # flagless — the key signs the request automatically
store whoami                                      # → alice
# edit / add / commit / push  (every call signed by the key)
```

### Moving a repo (portability)

A repo can be relocated to a different server without losing its history *or* its access layer. This is strictly more than git, which carries only content and history and leaves permissions and collaborators to be rebuilt by hand on the new host. In AgentStore the member roster, grants, and admin roles travel with the repo and reconstruct on arrival.

How it works:
- **The roster rides in every clone.** Each local repo carries the member roster — `(principal_id, username, public_key)`. These are all public, so replicating them everywhere is harmless, and it is what seeds the new server's directory. The sensitive metadata (grants, roles, the commit log, file content) is permission-filtered, so only an **admin's** clone — which has full read access — is a complete, migration-ready copy. That is why only an admin moves a repo.
- **The migration is the one admin-gated step.** The only privileged action is `push --mirror` — only an admin holds a complete, migration-ready copy, so only an admin can perform the move. The mirror **self-authenticates against the payload roster**: the signature proves the caller holds the private key for a principal the payload lists, and the payload roles prove that principal is a repo admin. No prior registration on the target is required — and pre-registering would actively *break* the move, because it would mint a second `principal_id` for the same key, leaving the directory with two ids per key and the member's stable id no longer the one their config points at. Because the target must be empty, a self-declared admin can only create a brand-new repo it owns (the authority `init` already grants); it can never touch an existing repo. There is no server-shared `origin` to flip: each member's `origin` is local config (as in git). Designating the new canonical home is a coordination step — the admin announces the new URL — not an enforced control-plane mutation.
- **Identity is the stable `principal_id`, reused across the move.** A `principal_id` is preserved by the mirror and is the same on the old and new server. So a member does not get a *new* identity on the new home — they reconnect their local config to their *existing* one with `bind`, which proves key ownership and returns the seeded `principal_id` (the admin's own config is bound automatically by `push --mirror`, which reports the resulting username). Authentication is by the asserted `principal_id` plus a signature against that principal's registered key, so the key is the *proof* of identity, never its *source*: a key rotation (`rekey`) keeps the `principal_id` and all grants intact, and you can never claim someone else's id, because you cannot produce their private key.
- **A move is a copy.** The old remote stays and simply goes stale; don't run both as masters. (A `delete repo` workflow is future.)

The flow — git's exact shape (`remote add` + a mirror push):
```
# an admin, against the new (empty) server — NO pre-registration needed:
store remote add newhome https://new-server.com/brand
store push newhome --mirror      # full upload: objects, history, grants/roles, roster —
                                      # commit IDs and seq preserved verbatim. Refuses a non-empty target.
                                      # Self-authenticated against the admin in the payload roster.
                                      # Seeds the new directory from the roster (username collisions
                                      # auto-renamed) and binds the admin's local config automatically,
                                      # reporting the admin's resulting username + any renames.

# the admin then announces the new canonical home (new-server.com/brand) out of band —
# there is no shared origin to flip; each member sets their own local origin by re-cloning.

# every other member, once, to reconnect to their preserved identity, then re-clone:
store bind --remote https://new-server.com --username alice --public-key ~/.agentstore/id_ed25519.pub
                                      # (use the username the admin announced — it may have been
                                      #  auto-renamed if it collided with a principal already on the server)
store clone https://new-server.com/brand
store whoami                     # confirm your username on the new remote
```

Members **re-clone** rather than repoint an existing checkout. Re-cloning guarantees a consistent starting state; `bind` re-establishes the local identity mapping against the new remote, resolving any auto-renamed username. Because commit IDs are content-addressed and `seq` is preserved, history is identical across the move and `log --cursor` consumers (graph layers) resume seamlessly.

**Known limitation — identity fragmentation.** If a member had *independently self-registered* on the destination server (a different `principal_id`, same key) *before* the repo arrived, the move seeds their original id alongside it, so one person now holds two real ids on that server. Authentication stays unambiguous (it is per asserted id), but the two ids cannot be *merged*, because each id's authored commits are hash-bound to it (merging would rewrite commit ids). `bind <username>` lets the member choose which id to act as; unifying them is out of scope. The case is rare and avoidable — don't self-register on a destination you'll be moved into; let the move seed you.

---

## Server architecture

The AgentStore server is the authoritative backend for one or more repositories. The CLI is its client, communicating with it over HTTP and WebSocket.

### Responsibilities
- **Repository storage** — holds the file store, version history, and commit log for each repository it hosts
- **Authentication** — verifies the signature on every request against the principal's registered public key; maintains the open identity directory (`register`)
- **Access-control enforcement** — evaluates the repo `admin` role and all read/write/owner path grants before serving or accepting any operation
- **Event streaming** — maintains WebSocket connections with subscribed clients and fans out events when commits are pushed
- **Control-plane audit log** — emits a structured log line for each identity/access-control action (`register`, `principals add/remove`, `grant`/`revoke`, admin-role changes, `rekey`) with actor, action, target, and timestamp. This rides in the server's general application log — it's for accountability, not a queryable store, and is distinct from the versioned commit ledger and the event stream. (A queryable `store audit` surface is on the roadmap.)

### Deployment modes

| Mode | Description | Use case |
|---|---|---|
| Local server | Runs on localhost, started with `store server start`; binds the loopback interface (127.0.0.1) so connections never leave the machine | Solo dev, offline work |
| Self-hosted | The same server binary run on any machine or cloud | Teams, enterprises, data-residency requirements |

Local and self-hosted run the *identical* server binary — there is no feature-gated, hosted-only variant. Self-hosting is the standard deployment, not a degraded mode.

### Repository model
A server hosts one or more repositories. Each repository is an isolated namespace with its own:
- File tree and version history
- Commit log
- Access-control rules (path grants + the repo `admin` role)
- Event subscriptions
- Member principals — a snapshot of each member's `(principal_id, username, public_key)`, seeded from the remote's open directory when an admin adds them

This is analogous to how a GitHub instance hosts multiple repos, each with independent access control. The remote-wide identity directory is open (anyone can register); repo access is gated per repo by admins.

### Event streaming
When a client pushes a commit, the server:
1. Verifies the request signature, then validates permissions
2. Writes the commit to the log
3. Emits events to all WebSocket subscribers watching the affected paths or event types

Clients connect to the event stream via `store watch` and maintain a persistent WebSocket connection. Agents use this to trigger work reactively rather than polling.

Event delivery is filtered against **live** permissions, re-evaluated per event — not against a snapshot taken when the stream opened. So a mid-stream `revoke` immediately stops delivery for newly-unauthorized paths, and a new `grant` begins delivery, with no reconnect needed.

### Client-server communication
*An internal protocol between the CLI and the server, not a public API.*
- **HTTP** — all read/write operations (push, pull, log, permissions, identity directory); every request carries a per-request signature
- **WebSocket** — event subscriptions only (`watch`, `changes` streaming); the connection request is signed at handshake
- The open storage format means the server's data can be inspected, exported, or migrated without proprietary tooling

---

## Differentiators

No appropriate agent data store offers all: **portability + versioning + file-level access control + real-time events + non-blocking writes**

### 1. Real-time file-level notifications
Enables any system or agent built on top of AgentStore to become real-time reactive. Agents can listen to specific file changes or global changes, and knowledge systems serving agents can resynthesize knowledge.

As agents become a part of every workflow and human / computer interaction, we will need every system to provide real time notifications. Currently many systems either have no notifications or limited capabilities. But agents are only as valuable as the recency of the data they have.

In particular, we will see more and more agent libraries like GBrain which will aim to synthesize agent memory and data across large datasets (even entire enterprises) and real time notifications are critical for this.

### 2. Portability
Agent users want to own their memory. This isn't our first rodeo - we've been locked into both consumer and enterprise systems. Agents will be by far the stickiest - they will be a part of every interaction and know almost everything about us. We will want to own our memory so we can switch providers and so that we can control our privacy.

Portability means: open-source client/server, self-hostable, exportable artifact history, open storage format.

### 3. Versioning and auditability
Every change is tracked and retained - full history, file-level granularity, and explicit staging over what gets committed. Human-oriented stores version weakly by comparison. For agents - whose output is probabilistic - we need auditing and rollback. In addition, prompts and skills are considered code-like by most users and need code-like versioning. However, unlike code, prompts and skills have no cross-file dependencies that break when each file is versioned on its own, so file level versioning is enough; they do not need repo level atomic snapshots.

### 4. File-level access control
While many agent users currently rely on git for working file and memory storage, we will soon want to allow multi-agent, multi-user environments for these workspaces and memories. Without file-level access control it will be logistically challenging to set up these environments.

### 5. Non-blocking writes
Source control platforms coordinate writes at the repo level - a push to a branch someone else has advanced is rejected and must be pulled, merged, and retried, even when the two writers touched completely different files. This is the right thing for code, where a change in one file can break another and the whole set must land atomically. Agent knowledge work has no such cross-file dependencies, so that coordination buys nothing and just creates contention at the datastore level. Non-blocking, file-level writes are critical for multi-user/agent environments.

### 6. Git-shaped interface
Theoretically not required but a significant advantage given agents' familiarity. Strong interface familiarity for both humans and agents. It also allows stronger control than alternative human oriented storage services like Google Drive or OneDrive.

---

## Use cases

### Real-time knowledge layer synthesis
Agent frameworks like GBrain synthesize raw knowledge files into a structured knowledge graph. Without real-time notifications, this runs on a schedule because checking for updates requires polling the file system.

With AgentStore, this synthesis event-driven. The synthesis framework subscribes to relevant paths with `store watch`. The moment a source file is committed, AgentStore emits an event. The agent wakes, calls `store log --cursor <last_cursor> --json` to get only the delta, and updates only the affected parts of the knowledge graph. The framework author decides exactly when and how often synthesis runs — triggered by real data changes, and can be selective based on what data was changed.

### Secure multi-agent collaboration
In a shared git repository, access is all-or-nothing at the repo level. Any agent with push access can read and write every file. This makes it unsafe to give an agent access to a repo that contains files it shouldn't see — salary data, legal strategy, confidential customer notes.

AgentStore grants access at the file and folder level. A customer research agent can have read access to `/customers/sanitized` and write access to `/research/output` without being able to see `/finance` or `/legal`. Each agent operates with least privilege, and the boundaries are enforced by the server, not by convention.

### Reduced coordination overhead in concurrent multi-agent workflows
In git, even when two agents modify completely different files, the second to push must pull, merge, and push again, because git's conflict detection is repository-level, not file-level. The contention is artificial: the changes don't actually conflict, they just share a branch ref. At agent scale this becomes a real bottleneck. Every push is serialized through that one ref, so the more agents push, the more pushes get rejected and retried, and each retry isn't free (the agent has to at least inspect why it was rejected before trying again). An unlucky agent can keep losing the race while others make progress. That is the **write starvation** problem.

AgentStore uses file-level optimistic concurrency control. When an agent pushes a commit, the server checks only whether the specific files in that commit have been modified since the agent's base version. If Bob is committing `b.md` and Alice committed `a.md`, Bob's push succeeds — the change to `a.md` is irrelevant. Bob is only blocked if another agent also committed `b.md` during its window. In a well-structured multi-agent system where agents own their output paths, true conflicts are rare. Write starvation is eliminated.

---

## Target users

**Primary (early adoption):**
- Agent framework and infra builders (Hermes-style harnesses, GBrain style frameworks)
- Advanced AI users currently using Git as knowledge memory with one or multiple agents
- AI FDEs building multi-agent workflows over sensitive or segmented data

**Secondary (after traction):**
- Small AI-native teams with shared knowledge bases
- Enterprise teams evaluating agent-native infra

---

## The reference demo

*Build to this demo. If it works end-to-end, the MVP is done.*

> Two agents and one human share a non-code knowledge workspace. One agent has read access to `/customers/sanitized` but not `/finance`. Another agent has write access to `/strategy` but not `/customers`. A human edits a file. AgentStore emits a real-time event. A graph layer calls `store log --cursor <last_cursor> --json` and updates only the delta. Everything runs self-hosted and can be exported.

This demo makes the category legible. It shows all four MVP requirements in one scenario:
1. File/folder access control (two agents with different permissions)
2. Real-time file events (reactive trigger on human edit)
3. Git-shaped CLI (familiar workflow)
4. Portable self-hosted server (no cloud dependency)

---

## Implementation language — Go

AgentStore is implemented in Go. The choice is deliberate and worth documenting.

**Why Go fits this project:**
- **Single binary deployment.** The server and CLI compile to a single binary per platform with no runtime dependency. A self-hosted user downloads one file and runs it — no Python environment, no Node runtime, no JVM. This is the portability story made concrete.
- **Built-in concurrency model.** Go's goroutines and channels are designed for exactly the workload AgentStore's server handles: many simultaneous WebSocket connections, concurrent reads, event fan-out to multiple subscribers. Agent workloads generate an order of magnitude more reads and writes than human workloads — Go handles this without the complexity of manual threading.
- **Fast compilation.** Tight iteration loop during development.
- **Strong standard library.** HTTP server, WebSocket, file I/O, crypto, and SHA-256 hashing are all in stdlib — minimal external dependencies.
- **Precedent in the category.** Gitea (self-hosted git server) and the GitHub CLI are both Go.

---

## v0.1 scope (OSS launch)

**Constraints**
- Text files only (`.txt`, `.md`, `.json`, `.yaml`, `.toml`, `.csv`, and similar plain-text formats)
- Maximum file size: 100KB (configurable per server deployment)
- Maximum repo size: 1GB (configurable per server deployment)

**Features**
- CLI (add, commit, push, pull, reset, merge, status, diff, log, watch, grant, register, bind, principals, admin)
- Local client
- Open storage format (SQLite for metadata, content-addressed object store for file content)
- Self-hosted server with WebSocket event router
- Keypair authentication (ed25519, sign-every-request) with an open identity directory
- Basic file-level access control (per-path read/write/owner grants) plus the repo `admin` role
- Commit log
- Server log

---

## Roadmap (post-v0.1)

Deferred from v0.1 to keep the initial release minimal:

- Snapshots
- Revert
- Propose / review workflow
- Principal groups
- Personal Access Tokens (PATs)
- Repo-level branches
- Pack files
- Export to git