# AgentStore — Architecture

Technical decisions for implementation. See [`agentstore-prd.md`](agentstore-prd.md) for product context.

---

## Storage format

AgentStore uses a two-part storage design: a **content-addressed object store** for file content, and a **SQLite database** for metadata (versions, commits, access control, principals).

This mirrors how git stores objects but makes the metadata queryable rather than walking a tree.

### Repository layout on disk

The store is the same two parts in both places, `store.db` plus the `objects/` tree:

```
<store>/
  store.db          # SQLite: all metadata
  objects/          # content-addressed file content
    ab/
      cdef1234...   # file content stored by SHA-256 hash
```

**Server layout.** A server hosts one or more repositories. Each repository is an independent directory (its own complete `store`) under the server's data directory. The server holds no working tree; it only serves the store.

```
<data-dir>/
  server.toml       # deployment config (see Server configuration)
  server.db         # server-level state: the identity directory
  my-repo/          # one repo = one store (store.db + objects/)
  another-repo/
```

**Client layout.** A clone keeps the store inside a hidden `.agentstore/` directory and checks the files out into the working directory alongside it (git's model). The working files are what the agent reads and edits; `.agentstore/` is the machinery.

```
my-project/                 # working directory (visible files the agent edits)
  strategy/icp.md
  notes/research.md
  .agentstore/              # hidden: the store
    store.db                # replicated repo metadata (permission-filtered)
    objects/                # content-addressed file content
    index.db                # local working state (staging, status, merge); never pushed
    config                  # local config (origin, identity, defaults); never pushed
```

A clone's store is **permission-filtered**: it contains only the metadata and objects for files the principal can read. This is why only an admin's clone (full read access) is a complete, migration-ready copy. The server's copy is always complete.

There is no `HEAD` file. The tip of the commit log is derived (`max(seq)` in the `commits` table), and the current content of the repo is the set of per-file heads in `file_branch_heads`, not a single commit. HEAD remains a derived concept in the CLI (`clone` checks out the latest per-file state; `log` has a tip), but it is not a stored pointer.

**Local client state (the index).** The client tracks pending work in a separate `.agentstore/index.db`, kept distinct from `store.db` (the replicated repo metadata). It is its own file for three reasons: `store.db` keeps the same shape on client and server (the server has no index), `clone` and `push --mirror` copy `store.db` and `objects/` but never the index, so local working state physically cannot leak, and it mirrors git keeping `.git/index` separate from the object store. It is still SQLite, so staging stays transactional. The index backs `add`, `status`, `diff`, `commit`, and `merge --abort`, is purely local and per-clone (never pushed), and holds three things:

- **Staged changes** — what the next `commit` will contain. One row per staged path: the staged `object_hash` (null for a staged deletion), the `change_type` (`added` / `modified` / `deleted`), and the file's `based_on_commit_id` (the commit the change was derived from). That `based_on_commit_id` is exactly what `push` sends per file for the OCC check, so the index is where it originates.
- **Working-tree status** — `status` and `diff` compare three states per path: the committed head (resolved via `file_branch_heads` → `commit_files`), the staged entry if any, and the actual file on disk. To avoid rehashing every file on each `status`, the index may cache per-path stat info (size, mtime) and rehash only when it changes (the same optimization git's index uses); v0.1 may start by rehashing and add the cache later.
- **Merge in progress** — after a conflicting `pull`, the index records the merge-in-progress marker (the second parent for the resulting merge commit) and a per-path unresolved flag. `commit` then produces a two-parent merge commit; `merge --abort` clears this state and restores the last committed working tree.

A fresh `clone` starts with this state empty: clean working tree, nothing staged, no merge in progress.

**Unpushed commit lifecycle.** Local commits (those not yet accepted by the server) have `seq = NULL` in the `commits` table. A successful `push` confirms them by setting `seq` to the server-assigned value. Commits that are superseded — the pre-merge rejected commit that is folded into a merge commit — are *absorbed* by setting `seq = -rowid` (a unique negative value per row). Absorbed commits are excluded from `log`, from the unpushed chain, and from `LatestCommitID`; their content remains intact in the object store but they do not appear in history. `reset` absorbs the entire unpushed chain and restores `file_branch_heads` to the last confirmed state, then restores the working tree to match.

**Time-travel reads (`checkout` and rewind).** The "state of path X at point Y" query is shared by single-file `checkout`, whole-repo `checkout .`, and the three-way merge base lookup. It resolves the reference (a commit ID or a `seq`) to a position in the linear history, then, for each path, takes its most recent change at or before that position; the path is present (restored to that change's content) unless that change is a `deletion` or the path has no change at or before the position, in which case it is absent. The position ordering is `seq` for confirmed commits, with local unconfirmed commits (`seq = NULL`) ordered after all confirmed ones by `rowid`; absorbed commits (`seq < 0`) are excluded. Whole-repo `checkout .` is the rewind primitive: it reconstructs the full file set at the target position, writes it to the working tree, and stages every difference from the current head (modifications, restorations, and deletions of files added since). It is **non-destructive** — the rewind is committed as an ordinary new commit and accepted through the normal push path, so history is append-only and `log --cursor` consumers see the rewind as a forward delta rather than a gap. Destructive history rewriting (force-push) is explicitly out of scope for v0.1.

**Every repo is server-hosted.** There is no local-only repo. `init` creates an empty repo on a server (which may be a local loopback instance) and checks it out; `clone` checks out an existing one. Both set `origin` and need the server only at creation/checkout and at push/pull, ordinary `add`/`commit` work stays offline. Keeping the server as the sole `seq`/auth/event authority avoids a second "local is its own authority" mode. The create-repo operation is also what produces the empty target the move flow (`push --mirror`) expects.

### Object store

Identical to git's object store:

- Content is hashed as SHA-256 of `blob <size>\0<content>` (the same header format git uses, with SHA-256 instead of SHA-1)
- Stored at `objects/<hash[0:2]>/<hash[2:]>`
- Objects are immutable and never overwritten
- Identical content across files or versions is stored once (deduplication is free)
- The object store can be read with standard git tooling if SHA-256 object format is supported

### SQLite schema

#### `principals`
Members of this repository — a synced snapshot of each member's identity, copied in from the remote's directory when an admin adds them (`principals add`). This snapshot is what makes a repo self-contained and portable: moving it to a new remote seeds that remote's directory from these rows. Grants and roles key on `id` (stable across remotes), never on `username` or `public_key`.

| Column | Type | Notes |
|---|---|---|
| id | TEXT | `principal_<uuid>` — stable identity, assigned at registration, carried into every repo |
| username | TEXT | Display label; unique within a remote (may collide across remotes — resolved by auto-rename on move) |
| public_key | TEXT | OpenSSH-format ed25519 public key — the snapshot used to seed a new remote's directory on move |
| created_at | INTEGER | Unix timestamp (ms) |

#### `repo_roles`
The repo-level control plane, orthogonal to path grants. A principal holding the `admin` role governs membership (`principals add`/`remove`), the admin role itself, and carries full data access. There can be many path-`owner`s but few admins; `init` creates the first admin and the last admin cannot be removed. Because the last admin always exists and carries full data access, the admin role doubles as the data-plane recovery authority: an admin may grant or revoke any permission on any path (including to or from themselves), so no path — not even `/*` — can be orphaned with no owner.

| Column | Type | Notes |
|---|---|---|
| principal_id | TEXT | FK → principals.id |
| role | TEXT | `admin` (the only role in v0.1) |
| granted_by | TEXT | FK → principals.id |
| created_at | INTEGER | Unix timestamp (ms) |

#### `files`
Registry of all file paths that have ever existed in the repository.

| Column | Type | Notes |
|---|---|---|
| path | TEXT | Primary key, e.g. `/strategy/icp.md` |
| created_at | INTEGER | Unix timestamp (ms), creation time of the *current* incarnation; reset on recreate |
| deleted_at | INTEGER | Unix timestamp (ms) of the *most recent* deletion; null if the path currently exists |

**Current-state registry, not a lifecycle log.** `path` is the primary key, so there is exactly one row per path. The two timestamps describe the *current incarnation* of the path: `created_at` is when it was last (re)created and `deleted_at` is when it was last deleted (null if it currently exists). A path that is created, deleted, then created again keeps its single row: the recreate is an **upsert** (reset `created_at`, clear `deleted_at` back to null) rather than an insert. The full create/delete/recreate lifecycle, including the first-ever appearance, lives in `commit_files` (ordered by `commits.seq`), which records each episode as a distinct row; `files` is the convenience registry and the FK target, not the history.

#### `file_branch_heads`
Tracks the commit holding the most recent accepted content of each file on each branch. Used for OCC checks and fast content lookup. In v0.1 only `main` exists — the client has no command to create branches and the server will reject any request to do so.

| Column | Type | Notes |
|---|---|---|
| file_path | TEXT | FK → files.path |
| branch | TEXT | Branch name; `main` in v0.1 |
| commit_id | TEXT | FK → commits.id — the commit holding this file's current content on this branch; updated atomically on each accepted push |
| PRIMARY KEY | | `(file_path, branch)` |

**On delete, the row is kept.** Deleting a file does not remove its `file_branch_heads` row; the row stays and `commit_id` points at the delete commit (whose `commit_files` entry has `change_type = deleted` and a null `object_hash`). So "this path exists on this branch but is currently deleted" is a head lookup, not a log scan, and a later recreate just re-points the same row. Readers must check the head commit's `change_type` / null `object_hash` to tell deleted from present.

#### `commits`
The commit log. Each commit groups one or more file versions into a logical change.

| Column | Type | Notes |
|---|---|---|
| id | TEXT | SHA-256 hex of commit content (64 chars) — content identity |
| seq | INTEGER | Per-repo monotonic sequence number, server-assigned at accept time — order identity and the sync cursor; unique, strictly increasing |
| message | TEXT | |
| author_id | TEXT | FK → principals.id |
| created_at | INTEGER | Unix timestamp (ms) |

A commit has three distinct identifiers, each with one job: `id` (SHA-256) is **content identity**, `seq` is **order identity**, `created_at` is **wall-clock time**. `seq` is *not* part of the hashed commit content — it is assigned by the server when the push is accepted, at the already-serialized write point, so allocation adds no new contention.

**Why a sequence number** (a deliberate divergence from git). Git needs no such counter for two reasons: it has no live events, and it is a single-root DAG whose topology already implies order. AgentStore has neither property. It needs a resumable position for live consumers, and under file-level OCC commits are accepted concurrently and independently — so *acceptance order*, not parent topology, is the order a streaming consumer must follow. `seq` records exactly that. It is preferred over the alternatives: `created_at` can tie at any finite resolution (needs client-side dedupe); the content hash has no inherent order (resolving "since X" would require first locating X); SQLite's implicit `rowid` does not port to PostgreSQL and is not a stable public contract. An explicit column maps cleanly to both backends (a counter in SQLite, `BIGSERIAL`/identity in PostgreSQL).

#### `commit_parents`
Records the parent commit(s) of each commit. Normal commits have one row. The first commit has zero rows. Merge commits (produced when a push conflict is resolved) have two rows.

| Column | Type | Notes |
|---|---|---|
| commit_id | TEXT | FK → commits.id — the commit being described |
| parent_id | TEXT | FK → commits.id — a parent of that commit |
| order | INTEGER | `0` for first parent, `1` for second (merge); preserves display order |
| PRIMARY KEY | | `(commit_id, parent_id)` |

#### `commit_files`
The file changes contained in each commit. A row here is a file's content at one point in its history — the model's "version" concept, stored as the (commit, path) pair that introduced it rather than as a separate table.

| Column | Type | Notes |
|---|---|---|
| commit_id | TEXT | FK → commits.id |
| path | TEXT | FK → files.path |
| object_hash | TEXT | SHA-256 of content; null when `change_type` is `deleted` |
| size | INTEGER | Bytes |
| change_type | TEXT | `added`, `modified`, `deleted` |
| PRIMARY KEY | | `(commit_id, path)` |

#### `grants`
Access control rules. v0.1 supports two match forms only: an **exact path** (`/strategy/icp.md`) and a **prefix** (`/strategy/*`, matching everything beneath `/strategy`). Interior wildcards like `/customers/*/sanitized` are not supported — full glob syntax is deferred post-v0.1.

| Column | Type | Notes |
|---|---|---|
| principal_id | TEXT | FK → principals.id |
| path_pattern | TEXT | Exact path or prefix, e.g. `/strategy/icp.md` or `/strategy/*` |
| permission | TEXT | `read`, `write`, `owner` |
| granted_by | TEXT | FK → principals.id |
| created_at | INTEGER | Unix timestamp (ms) |
| PRIMARY KEY | | `(principal_id, path_pattern)` |

This table holds **current** grants only. A `revoke` deletes the row; a downgrade updates `permission` in place. The history of grant/revoke actions is not kept here, it is emitted to the server log (see the control-plane audit log). A queryable, replicated grant history in the database is a possible future addition for stronger compliance, but is not a v0.1 goal.

**Permission model.** Three levels: `read` < `write` < `owner`. `owner` includes read and write and may grant or revoke on its path; a path may have multiple owners. A repo `admin` may also grant or revoke any permission on any path (including to or from themselves) — the data-plane recovery authority that keeps any path from being orphaned. The principal who runs `init` is granted `owner` on `/*`.

Grants are **hierarchical**: a grant on a folder path applies to everything beneath it, and a principal's effective permission on a path is the **maximum** level across all active grants whose `path_pattern` matches that path or any ancestor folder. There are no negative/deny grants in v0.1; to keep a subtree private, do not grant its parent.

There is **exactly one grant row per `(principal, path_pattern)`**, enforced by the primary key. `grant` sets the level on a pattern: re-granting a different level updates the row in place, so a grant can raise or lower that grant. `revoke` deletes the row entirely; it is not a downgrade (to lower a level, use `grant`). Both `grant` and `revoke` operate on the **exact pattern given**, not on effective access: revoking `/strategy/icp.md` does not remove access conferred by a `/strategy/*` grant, and granting a lower level on a child path does not override a higher level inherited from an ancestor (the maximum still wins). To change inherited access, adjust the ancestor grant. Every `grant`/`revoke` is emitted to the server log for accountability.

Grants are **live control-plane state, not versioned.** `grant`/`revoke` are applied directly on the server (not staged or committed) and take effect immediately. Enforcement always evaluates the *current* grants at request time, so a revoke is **retroactive across all versions** — a revoked principal loses access to history, not just future commits. The table records current state only; the audit of who changed what and when lives in the server log.

**Scope of retroactive revoke.** "Retroactive" means the *server* will never serve that data to the revoked principal again — every read is re-checked against current grants. It does **not** and cannot mean the principal loses access to content they already cloned: a file already written to their disk is theirs, and their local `log` reads local SQLite. This is inherent to any portable, file-native store without content encryption + key rotation (a deferred concern). The guarantee v0.1 makes is precise: no future server response will include revoked content.

**Retroactive grant and incremental `pull` (forward-only).** Grants are equally retroactive in the *granting* direction — once a principal is granted read on a path, the server will serve that path's history on the next read. But `pull` is deliberately a **forward-only** delta: it fetches commits with `seq` greater than the client's sync cursor (`max` seq already seen). A grant that opens up *historical* commits — those with `seq` below the cursor — is therefore **not** backfilled by an incremental `pull`; the client already advanced past those positions. The v0.1 contract is explicit: **to pick up newly-granted historical access, re-clone.** A fresh `clone` fetches from `seq 0` permission-filtered against current grants, so it materializes everything now readable, history included. This keeps the cursor a single, forward-only, resumable position — the property `log --cursor` and `watch` consumers (e.g. graph layers) depend on — rather than introducing a second reconciliation path that re-scans history on every pull. The common onboarding case (a *new* member) is unaffected: they clone fresh and get all readable history. Heads-based reconciliation that would let an existing clone pick up newly-granted paths in place is a possible non-breaking future enhancement, but is intentionally not v0.1.

**Delete and revoke are independent.** Grants are keyed to paths, not to content, so deleting files never touches grants. There is no "delete a folder" operation — a folder is just a path prefix, and emptying it leaves any grant on that prefix intact. This is deliberate: folder grants must survive ordinary file churn (an edit is often a delete + re-add). To remove access, `revoke` — do not rely on deletion. The consequence to be aware of is that a grant on a path that has been emptied will silently apply again to any file later created under that path.

#### `directory` (remote-level, not per-repo)
The remote's open identity directory — the live authentication source. Unlike every other table here, it is shared across all repos a remote hosts, not stored inside any one repo; it lives in the server-level `server.db` (see Server configuration). A principal self-registers (`register`), which mints a fresh `principal_id`; registration is open (having an identity grants access to nothing). To verify a request, the server looks up the row for the **asserted** `principal_id` and checks the signature against its `public_key` — the id is the identity, the key is its proof. A `rekey` updates the row here, so rotation is a single per-remote operation effective across every repo on the remote. The directory is public on the open `GET /_directory` plane: with a `?username=` it resolves one username to its `(principal_id, public_key)` and backs `bind` (which points local config at an existing identity for a key you hold — the counterpart to `register`, used after a move seeds your preserved id here); with no parameter it enumerates every registered principal (public fields only) and backs `principals list --remote`.

| Column | Type | Notes |
|---|---|---|
| principal_id | TEXT | `principal_<uuid>`, primary key — stable across remotes |
| username | TEXT | Unique within this remote |
| public_key | TEXT | OpenSSH-format ed25519 public key — the live key used to verify request signatures |
| created_at | INTEGER | Unix timestamp (ms) |

### Design notes

**Why SQLite?** It's self-contained (one file, no server process for the metadata layer), fast for the query patterns AgentStore needs (log queries, permission lookups, changes-since), and trivially portable and inspectable. For a self-hosted deployment, it also means zero database infrastructure to manage.

**SQLite WAL mode is required.** SQLite must be run in [WAL (Write-Ahead Logging) mode](https://www.sqlite.org/wal.html). In default journal mode, reads block writes and writes block reads. WAL mode allows concurrent reads during a write, which is important for agent workloads where many agents may be reading while another is committing. The single-writer constraint remains — simultaneous writes still queue — but read throughput is unaffected.

**Read consistency.** WAL preserves snapshot isolation: a read transaction sees one consistent committed snapshot as of when it began, never a half-applied write. A push is applied as a single SQLite transaction (the `commits` row, its `commit_files` rows, and the `file_branch_heads` updates together), so readers observe a commit all-or-nothing, even one spanning many files. This is SQLite atomicity, not the file-level OCC, which governs write *acceptance*, not read consistency. Two disciplines are required to keep this guarantee:

- **Single read transaction for consistent multi-table reads.** Any read that must be internally consistent (for example, reconstructing repo state from `file_branch_heads` and then resolving `commit_files`) runs inside one read transaction, so it draws from a single snapshot. Splitting it across transactions can stitch together two snapshots if a push lands in between.
- **Object-before-metadata write ordering.** The content object is written and fsync'd to the object store *before* the `commit_files` row that references its `object_hash` is inserted. Combined with object immutability (objects are never overwritten or deleted in v0.1), this guarantees that any `object_hash` resolved from a committed snapshot always finds its object on disk. The SQLite transaction does not cover the `objects/` directory, so this ordering is what keeps the two stores consistent. (A future GC must be ordered against live references.)

**PostgreSQL upgrade path.** For high-concurrency deployments (many agents committing simultaneously), SQLite's single-writer limit becomes a bottleneck. The storage layer should be abstracted behind an interface from day one so that PostgreSQL can be substituted without rewriting query logic. PostgreSQL supports true concurrent writes and is the natural upgrade target. This is a post-v0.1 concern but the abstraction should be in place from the start.

**Why content-addressed objects?** Immutability is free — you can never accidentally corrupt a version by overwriting it. Deduplication is free. The object store is also independently inspectable and can be verified by recomputing hashes.

**Pack files (post-v0.1).** The flat object store accumulates one file per version — fine for v0.1 but stresses filesystem inodes at scale under agent workloads with frequent commits. Git solves this with pack files: loose objects are periodically bundled into a single binary file with an index for fast lookup. Pack file support is a named post-v0.1 optimization; the object store interface should be designed to accommodate it without changing callers.

**Canonical encoding primitives.** Used wherever AgentStore hashes content into an identity: integers are big-endian (lengths and counts `uint32`, timestamps `uint64` ms); strings are UTF-8 prefixed with a `uint32` length; hashes are raw 32 bytes inside a preimage (hex only when stored or displayed); lists are a `uint32` count followed by each element; and every preimage starts with a fixed ASCII domain/version tag, which versions the format and gives domain separation. Length-prefixing everything makes the encoding canonical: exactly one byte string per logical value, with no delimiter ambiguity.

**Commit ID format.** The commit `id` is `lowercase_hex(SHA-256(canonical commit content))`, computed once at creation and stored. It is the commit's content identity, the same idea as git's commit SHAs (SHA-256 rather than SHA-1), so an id is a cryptographic proof of the exact state of every file the commit touches. Displayed as the full 64-char hex string, shortened to 8 chars in CLI output (git convention). This is a hash, not a signature: cryptographic *signing* of commits for provenance (`commit -S` / `verify`) is a separate, deferred feature (see PRD roadmap), and when it lands it signs over these same canonical bytes. The serialization is needed in v0.1 regardless, because every commit must have a stable, reproducible id, including across a `push --mirror` / re-clone where ids and `seq` are preserved.

The commit-content preimage that is hashed, using the primitives above:

```
"agentstore-commit-v1\n"                 # domain/version tag, literal ASCII
message      : uint32 len || UTF-8 bytes
author_id    : uint32 len || UTF-8 bytes
created_at   : uint64 BE                   # Unix ms, set by the client at commit time
parents      : uint32 count || N × 32 raw bytes   # in order (first parent, then second); NOT sorted
files        : uint32 count || N × file-entry      # SORTED by path bytes, ascending
```

Each `file-entry`:

```
path        : uint32 len || UTF-8 bytes
op          : 1 byte  (0x01 = set, 0x00 = delete)
object_hash : 32 raw bytes  if op == set ; omitted if op == delete
```

- **`seq` is excluded** (server-assigned after acceptance, so it is order identity, not content identity).
- **`size` is excluded** as redundant (the object hash determines the content and thus its size).
- **`change_type` collapses to set/delete:** added versus modified is a function of prior history, not of this commit's content, so only "sets path to hash" versus "deletes path" is part of the identity.
- **Parents are in order** (first versus second parent is semantically meaningful); **files are sorted by path** so input order cannot change the id.

**Path normalization.** Path strings are normalized to Unicode NFC on input (when a path enters via `add`), so the stored path is already NFC and this serialization stays stable across platforms. macOS hands back NFD and Linux NFC, so without normalization the same path would hash differently on different machines. File *content* bytes remain verbatim (see Platform); this rule applies to path strings only.

**File-level optimistic concurrency control.** AgentStore replaces git's repo-level fast-forward check with a file-level version check. On push, the client specifies for each file the commit it based its change on (`based_on_commit_id`). The server checks atomically: is `based_on_commit_id` still the `commit_id` recorded in `file_branch_heads` for that file and branch? If yes for **every** file in the commit, the push is accepted and each file's `file_branch_heads` row is updated to the new commit. If **any** file has been modified since the client's base, the **entire commit is rejected** — none of its files are applied — and the error names each conflicting file *and the current head commit that beat it* (so the client can fetch "theirs," merge, and retry). The commit is the atomic unit of acceptance, consistent with commit-atomic event delivery.

This is not the same as git's repo-level check, and it does not reintroduce write starvation. Conflict is scoped to the files *within a commit*, not the whole repo: Bob's commit of `b.md` is unaffected by Alice's commit of `a.md`. All-or-nothing applies only across the files of a *single* commit — so a logical change stays coherent (its message and provenance describe a set of files that all landed together) without ever blocking on another agent's unrelated files.

The linear commit log is preserved. `log --since` / `log --cursor` continue to work as before. The only change is the push-acceptance policy: repo HEAD no longer determines whether a push is valid — per-file head commits do.

File-level OCC eliminates write starvation in high-concurrency agent workloads where agents work on non-overlapping files. See the use cases section of the PRD for the write starvation scenario this solves.

**Conflict resolution — pull, merge, re-push.** When a push is rejected, the pusher reconciles by pulling — which fetches the new heads and runs a per-file textual three-way merge — resolves any conflicts, then commits a merge commit and pushes again. End to end:

1. `commit` locally → each file records its `based_on_commit_id`.
2. `push` → rejected if any file's base ≠ the current head; the error carries the conflicting head commit id(s).
3. `pull` (= fetch + merge) → for each file: **only the remote changed** → fast-forward the working copy; **only the local changed** → nothing to do; **both changed** → a three-way merge with **base** = content at the local `based_on_commit_id`, **ours** = the local version, **theirs** = the new head. Non-overlapping line hunks merge automatically and stage as resolved; overlapping hunks write git-style `<<<<<<< / ======= / >>>>>>>` markers into the working file and flag it **unresolved**.
4. Resolve in the working file, then `add` to clear the unresolved flag (edit + `add`, exactly like git).
5. `commit` → a **merge commit with two parents** (the local commit + the head merged against; see the `parents` table). Each merged file's new base is that head.
6. `push` → bases now match heads and the commit is accepted; if a file moved again in the interim, the loop repeats (ordinary optimistic retry).

The per-file base is explicit (`based_on_commit_id`), so unlike git there is no merge-base graph walk, and a merge only ever touches the files changed on both sides. The merge runs client-side; both "theirs" (the new head) and "base" are already addressable by hash in the content-addressed object store after fetch, so no scratch checkout is needed — only a small amount of local state (a merge-in-progress marker recording the second parent, and a per-file unresolved flag). Edge cases: a **modify/delete** conflict (one side edits, the other deletes) has no textual merge — it is resolved by choosing a whole side (`add` keeps the edit, `rm` accepts the deletion); `pull` follows git and **aborts** if uncommitted local changes would be overwritten (commit first); binary merge is out of scope under the text-only file constraint. `store merge --abort` discards an in-progress merge and restores the last committed state. The default reconciliation is a merge commit; **rebase** (single-parent, linear) and a standalone **fetch** (inspect before merging) are noted post-v0.1 options in the PRD roadmap.

**Two-tier identity (live source vs. portable snapshot).** The `directory` (remote-level) is the live authentication source; the per-repo `principals` table is a synced snapshot embedded for portability. While a repo is hosted on a remote, signature verification uses the directory. Moving a repo to a new remote — whose directory is empty — seeds that directory from the repo's `principals` snapshot, so grants (which key on the stable `principal_id`) never dangle. The two can drift only across a move with a stale snapshot, resolved by one `rekey` on the new remote. Username is the sole field that can collide across remotes (a display label); `principal_id` and `public_key` cannot.

**Moving a repo (portability mechanics).** A repo is relocated by mirroring its full state to a new, empty server with `push <remote> --mirror`, an admin-only operation. Mechanics:

- **Roster on every clone.** The member roster — `(principal_id, username, public_key)`, all public — is replicated into every local clone, so any clone can seed a new directory. The sensitive metadata (`grants`, `repo_roles`, the commit log, object content) is permission-filtered on clone, so only an **admin's** clone is a complete, migration-ready copy. This is why the migrator must be an admin.
- **`push --mirror` transmits the store verbatim.** It uploads all objects, the full commit history, `grants`, `repo_roles`, and the roster — **preserving `commits.id` and `seq` exactly**, rather than re-accepting commits and re-allocating `seq`. Because IDs are content-addressed (SHA-256) and `seq` is preserved, the migrated history is byte-identical and every `log --cursor` consumer resumes without a gap. The command refuses a non-empty target.
- **Directory seed on first serve.** On receiving the mirror into an empty repo, the new remote seeds its `directory` from the roster: merge by `principal_id` (known principals matched, new ones inserted); a `username` that collides with a *different* principal already in that remote's directory is auto-renamed. The migrating admin is recognized as admin from the `repo_roles` rows that travelled in the mirror — the bootstrap analog of `init`. The mirror response reports the signer's resulting `(principal_id, username)` plus any auto-renames, so the admin learns their (possibly renamed) name and can tell affected members which username to `bind`.
- **The mirror self-authenticates against the payload roster — no pre-registration.** `verifyMirror` finds the signer's asserted `principal_id` in the payload roster, verifies the request signature against *that roster entry's* public key, and confirms the same principal holds an `admin` role in the payload `repo_roles`. Trust is bootstrapped entirely from the payload, which is sound because the target must be **empty**: a self-declared admin can only create a brand-new repo it owns — the authority `init` already grants — and can never reach an existing repo. The admin must **not** pre-register on the target: registration mints a *fresh* `principal_id` for the same key, which would leave the directory with two ids per key (the new one plus the roster-seeded original) and point the admin's local config at the wrong one. Instead `push --mirror` writes the admin's local config for the new remote automatically, reusing the source key and the preserved `principal_id` from the response.
- **The mirror push is the only remaining admin-gated step; members `bind`, then re-clone.** `push --mirror` is admin-only because only an admin holds a complete copy. The mirror creates the repo slot at the target itself when the name is free, and refuses a name already in use; the target must not be pre-created with `init`, because the mirror carries its own roster, grants, and roles (an `init`'d repo would have seeded a different admin and owner). `origin` itself is local per-clone config, not a server-shared pointer, so there is nothing central to repoint — designating the new home is a coordination step (the admin announces the URL). Each other member then runs `bind` once against the new remote — proving key ownership and pointing local config at their **preserved** `principal_id` (seeded into the directory by the mirror), rather than `register`, which would mint a new id — and re-clones from the new location. `bind` resolves any auto-renamed username; re-clone guarantees a consistent start. Editing a checkout's local origin in place would technically work (identical IDs) but is not the prescribed v0.1 path.
- **Authentication is by asserted id, key as proof.** Every signed request names a `principal_id`; the server verifies the signature against *that* principal's directory `public_key` and accepts only on a match. The id is the identity; the key is its proof, never its source. This is what makes a key safely back a returning identity after a move (`bind` resolves your preserved id and your signature proves it is yours) while making it impossible to claim another principal's id without their private key. The open `GET /_directory` plane returns only public directory fields: a `?username=` lookup serves `bind`, while the no-parameter form enumerates the directory for `principals list --remote`. **Known limitation — identity fragmentation:** if a member independently self-registered on the destination (a different id, same key) before the repo arrived, the move seeds their original id alongside it, so one person holds two ids on that server. Auth stays unambiguous (per asserted id) but the two cannot be merged — each id's authored commits are hash-bound to it. `bind <username>` lets the member pick which id to act as; unification is out of scope.
- **A move is a copy.** The source remote is untouched and goes stale; v0.1 does not support two live masters (no replication). A `delete repo` workflow is a future addition.

**Required indexes.** The delta-sync query (`log --cursor` / `watch`) is the hottest read path — agents call it frequently to sync their knowledge state. The following indexes are required from the start, not added later as an optimization:

| Table | Index columns | Query it serves |
|---|---|---|
| `commits` | `seq` (unique) | `log --cursor <token>`, `watch` resume — the hot sync path |
| `commits` | `created_at` | `log --since <date>` |
| `commits` | `author_id` | `log --author <principal>` |
| `commit_files` | `commit_id` | join from commits to files (covered by PK prefix) |
| `commit_files` | `path` | `log <path>`, per-file history |
| `commit_parents` | `parent_id` | walk children of a commit |
| `grants` | `principal_id, path_pattern` | access control permission lookups on every operation |
| `repo_roles` | `principal_id` | admin-role check on control-plane operations |
| `directory` | `username` (unique) | registration uniqueness + lookup; `principal_id` is the PK |

**Future extension — repo-level branches.** Branches are planned as repo-wide named lines, not per-file. The schema is designed to accommodate them: `file_branch_heads` is keyed `(file_path, branch)`, so a branch is the set of per-file heads sharing a branch name, and `commit_parents` already records the commit DAG and merge commits. Because file-level OCC means there is no single whole-repo commit to point at, a branch is necessarily represented as a set of per-file heads rather than one ref. Adding branches means introducing a `branches` registry that records each branch's fork point, and allowing the client to create branches and the server to accept and merge them; merges are accepted through the same file-level OCC as ordinary pushes. In v0.1 only `main` exists. Branches are planned for a future version; file-level OCC and per-file versioning cover current use cases.

**Future extension — snapshots.** Promoted checkpoints that label a position in the commit log (a `seq`) for graph layers are a post-v0.1 feature (see the PRD roadmap). When added, they introduce a `snapshots` table (`id`, `seq` → the log position at snapshot time (FK → `commits.seq`), optional `label`, `created_by`, `created_at`). A snapshot references `seq` rather than a commit because its whole purpose is `log --cursor <snapshot>` (the delta is `seq > snapshot.seq`), and because file-level OCC means there is no single HEAD commit to point at; the commit at that position is derivable via `commits.seq` if needed. It is purely additive — snapshots reference existing log positions and change nothing about the commit log or object store — so it is deferred rather than built into v0.1.

**Future extension — commit signing and verification.** Signed commits and `verify` complete AgentStore's git interface (`git commit -S` / `git verify-commit`) but are deferred from v0.1 — see the PRD roadmap. When added, signing is purely additive and slots into the existing schema: a nullable `signature` column on `commits` (base64 over the commit content), a `keys` table mapping principals to registered public keys (`id`, `principal_id`, `public_key`, `algorithm` — `ed25519` default or `rsa` — `created_at`, `revoked_at`), and a later `path_config` table (`path_pattern`, `require_signature`, `updated_by`, `updated_at`) for the enforcement increment that can *require* signed commits on a path. None of this changes the commit log, object store, or `seq` cursor. Keypairs are generated with standard tooling (`ssh-keygen`/`openssl`) and the private key path lives in client config; the server only ever holds public keys.

**Future extension — Personal Access Tokens (PATs).** A bearer-token credential as an alternative to v0.1's keypair authentication, deferred to v0.2 (see the PRD roadmap). Purely additive: a per-repo `tokens` table (`principal_id`, `token_hash` salted, `label`, `created_at`, `expires_at` nullable, `revoked_at`, `last_used_at`) and a second branch in the request-auth path (present a token instead of a signature). The server stores only the hash. Changes nothing about keypair auth or the directory.

---

## Client configuration

Mirrors git's two-level config: a global file for identity, a per-repo file for remote and repo-level defaults. Both are plain TOML files.

### Global config — `~/.agentstore/config`

Applies across all repositories on the machine. Identity is per-remote — a principal registers a username + keypair with each remote, so this file maps each remote to the local identity and private key used for it (mirrors per-host SSH config).

```toml
[remote "https://store.acme.com"]
username = "alice"                        # username registered in this remote's directory
key_path = "~/.agentstore/id_ed25519"     # private key path; signs every request, never leaves the machine
```

### Per-repo config — `.agentstore/config`

Lives in each clone's `.agentstore/` directory and is **local to that clone**, like git's `.git/config`. It is not committed and not replicated: `clone` and `push --mirror` copy only `store.db` and `objects/`, never this file. It holds this clone's named remotes (including `origin`) and the principal this clone belongs to; each clone sets its own, and nothing here travels to the server or to other clones (the move flow relies on `origin` being per-clone local).

```toml
[remotes.origin]
url = "https://agentstore.example.com/my-repo"   # v0.1: repos live at the server root, /<repo-name>

[identity]
principal_id = "principal_<uuid>"                # which principal this clone authors commits as
```

**No per-file default grants in v0.1.** An earlier design carried a `new_file_grants` default (auto-grant applied to newly added files). It is intentionally **not** in v0.1: grants are hierarchical, so granting a folder prefix (`/strategy/*`) already covers every file created under it, now and later — a per-file default adds little. It also fits poorly with the model: `add` is offline while grants are server-side control-plane state (they would have to be applied at push time), and a creator with only `write` on a path cannot grant on it. A shared, enforced default would belong as a future *server-side* policy, not a local convenience file (see the PRD roadmap's inherited-grant warning).

### Config resolution order

The two files hold disjoint settings (global = identity, per-repo = remotes + this clone's principal), so there is no overlap to resolve. Were a setting ever defined at both levels, the per-repo config would win, mirroring git (`repo > global > system`).

| Setting | Global | Per-repo |
|---|---|---|
| Per-remote identity (username + key path) | ✓ | — |
| Remote URL(s) | — | ✓ |
| This clone's principal_id | — | ✓ |

---

## Server configuration

Server-level state and configuration live under the server's data directory, alongside the per-repo stores but separate from any one repo:

```
<data-dir>/
  server.toml        # deployment configuration (this section)
  server.db          # server-level state: the identity directory
  <repo>/            # per-repo store (store.db + objects/)
  <repo>/
```

**Server-level store (`server.db`).** Holds state that spans repos, currently just the identity `directory` table (see schema). It is deliberately separate from each repo's `store.db` so a repo directory stays a self-contained, portable unit: moving a repo never drags server-wide state with it.

**Configuration file (`server.toml`).** Deployment policy and defaults, a plain TOML file like the client config. Resolution order is built-in defaults, overridden by `server.toml`, overridden by CLI flags. Config is a file rather than a table so a deployment is configured by editing one readable file and restarting, with no privileged write path into a database and nothing to migrate.

```toml
[server]
# Bind address. Defaults to loopback; set a routable address to serve a network.
# The --addr launch flag overrides this.
addr = "127.0.0.1:8080"

[limits]
max_file_size_bytes = 102400        # 100 KB per file
max_repo_size_bytes = 1073741824    # 1 GB per repository
allowed_file_types  = ["text/*"]    # MIME allowlist; v0.1 is text only

[auth]
request_freshness_seconds = 300     # two-sided window for the signed request timestamp (±5 min)
```

**Launch flags.** `server start` accepts `--data-dir` (where the stores and `server.toml` live) and `--addr` (bind address). `--data-dir` is bootstrap, it locates `server.toml` itself, so it cannot come from the file. `--addr` on the command line takes precedence over the file. The pidfile used by `server stop` lives in the data directory.

Changing limits or the bind address takes effect on restart; the graceful drain on `server stop` / SIGTERM makes a restart safe.

---

## Server API

This section pins the cross-cutting and non-trivial parts of the protocol. The per-command endpoint catalog is deliberately not enumerated: each remaining CLI command maps to one endpoint whose semantics are already fully defined by the CLI spec and the data model, so the mapping is mechanical and is left to implementation. v0.1 is a single client and server, so the wire format is not yet a third-party contract to freeze on paper.

### Transport
HTTP with JSON bodies for request/response, and a single persistent WebSocket for `watch` (see Event protocol). The choice is for tooling simplicity: greppable, curl-able, no protobuf/gRPC toolchain, consistent with the git-shaped, single-binary design. One server process serves many repos; the repo is named in the path (`/<repo>/...`).

### Authentication envelope
Every request, including the WebSocket handshake, is **signed per request** with the principal's ed25519 key (generated outside AgentStore with standard tooling such as `ssh-keygen`; the server only ever holds public keys). The client holds only its private key, no session token, no stored bearer credential, which fits the stateless CLI: each invocation signs the request it is making. The server verifies the signature against that principal's `public_key` in the `directory` (`server.db`). There are no server-stored secrets.

Per-request signing is preferred over a session token because there is no persistent session to attach one to: every call but `watch` is a discrete HTTP request, so a token would add an issuance round-trip and server-side session state for no benefit. The one long-lived connection, the `watch` WebSocket, signs only its initial handshake request and is then trusted for the connection's lifetime; its individual event messages are not re-signed.

**Transport trust posture (v0.1).** TLS is assumed for any networked deployment and is the channel's confidentiality, integrity, and primary replay defense. The per-request signature adds authenticity and integrity at the application layer (and works even on plain loopback as defense-in-depth). v0.1 deliberately carries **no nonce / replay cache**; it relies on TLS plus a freshness window. Hardening against replay when the transport is untrusted (a per-request nonce and server-side replay cache) is a versioned, additive upgrade, see *Forward compatibility* below and the PRD roadmap.

**Signed preimage.** The signature is computed over this canonical form, using the encoding primitives from the Design notes:

```
"agentstore-request-v1\n"                # domain/version tag, literal ASCII
principal_id   : uint32 len || UTF-8 bytes
method         : uint32 len || ASCII, uppercase     # "GET", "POST", ...
request_target : uint32 len || UTF-8 bytes          # endpoint path + query string exactly as sent
timestamp      : uint64 BE                            # Unix ms (timezone-free; absolute instant)
body_sha256    : 32 raw bytes                         # SHA-256 of the raw body (digest of empty input if none)
```

The client sends `principal_id`, the protocol version, the `timestamp`, and the base64 `signature` as request headers. The server rebuilds the preimage from the **actual** method, target, and body it received plus the header-supplied `principal_id`, `timestamp`, and version, looks up the public key, and verifies. Ed25519 signs the preimage directly (it hashes internally), so only the body is pre-hashed, to keep the preimage small. Binding `method` + `request_target` + `body_sha256` means a captured signature cannot be redirected to a different endpoint, repo (the repo is in the path), or payload.

**Freshness check.** The server rejects a request whose `timestamp` falls outside a window of the current time, applied **two-sided** (too old, stale or replayed; too far in the future, client clock ahead). Default **±5 minutes**, configurable in `server.toml`. Because the timestamp is absolute Unix ms there is no timezone to coordinate; the only variable is clock skew, which the window absorbs. A clock off by more than the window fails closed with an explicit "timestamp outside the acceptable window, check your clock" error; the fix is NTP on the client.

**Forward compatibility.** The envelope is versioned two ways: the domain tag in the signed preimage (`agentstore-request-v1`) and a declared protocol-version header. The server dispatches verification on the declared version, and writing it as a version dispatch from day one (even with only v1) means later schemes are additive. Adding a nonce is exactly this: a v2 preimage with a `nonce` field plus a server replay cache. The version tag is part of the signed bytes, so a v2 signature cannot be stripped down and replayed as v1 (it would not verify). During a transition the server can accept v1 and v2 together so old clients keep working; the replay benefit is only realized once a server policy requires the minimum version (the deferred high-security mode).

`register` is the one call open to any identity (it establishes an identity; having one grants access to nothing). A bearer-token credential (PAT) as an alternative to per-request signing is a separate future option (see roadmap).

### Authorization and permission filtering
Authorization is evaluated from grants at request time against current state, so it is retroactive (see the permission model). Read endpoints (`clone`, `pull`, `log`, `show`, `watch`) are permission-filtered: a principal receives only the objects, commits, and events for paths it can read. Write endpoints check the relevant permission or role before applying.

### Permission-filtered history
A commit can span files with different grants, so the behavior for **mixed-visibility commits** is defined explicitly:

- **Commit visibility.** A commit is visible to a principal if it can read **at least one** file the commit touches. A commit none of whose files the principal can read is not shown as a normal entry (it becomes a stub, below).
- **Within a visible commit, only readable files are shown.** The changes for paths the principal cannot read are **silently omitted**, not redacted placeholders, not counted. Objects and `file_branch_heads` are likewise delivered only for readable files.
- **Author and message are shown in full.** They are commit-level and cannot be path-filtered. This is a deliberate, documented leak: **commit messages are not access-controlled, so do not reference restricted content in them.** (Redacting the message for partly-visible commits is a possible future hardening.)
- **The commit id is real but not locally verifiable under filtering.** The id hashes over all of the commit's files, including hidden ones, so a principal cannot recompute it from the visible subset and can infer that hidden content exists from the mismatch (never what it is). This is inherent to content-addressed ids plus filtering.

**Fully-inaccessible commits become stubs.** A commit none of whose files a principal can read is delivered to that principal as a minimal **stub**: `{ seq, redacted: true }` and nothing else (no id, author, message, paths, or timestamp). Stubs are generated per principal at serve time; the server stores the full log and filters on read, so there is no server-side schema change. The client stores a redacted row to keep its local log contiguous.

The purpose of stubs is **verifiable completeness.** With them, a principal's `seq` space is contiguous: every `seq` is either a visible commit or a stub, so the client can assert it has accounted for everything up to HEAD, and a *true* gap (a missing `seq` with no stub) is unambiguously an error rather than "something I am not allowed to see." Stubs live in the authoritative log path (`log --cursor`, clone history); the live `watch` stream stays advisory and filtered (real events only, gaps tolerated). So completeness is a property of the log, not the live channel, and live fan-out is not inflated by stubs.

**The accepted leak, on purpose:** stubs reveal the existence, count, and `seq`-position of commits a principal cannot see, never their content, author, timing, or paths. That is the price of a gap-free, verifiable history. A future high-secrecy mode could collapse stubs back into true gaps, trading completeness for hiding even the existence of inaccessible activity.

### Create repo (`init`)
An authenticated request to the server root creates a new, empty repo at `/<name>` (v0.1 repos live at the server root). The server refuses a name that already exists, creates the repo store (`store.db` + `objects/`), records the caller as the first `admin` and grants them `owner` on `/*`, and seeds the repo's `principals` snapshot and the server `directory` with the caller's identity. v0.1 lets any registered principal create a repo.

### Push
Push is the only endpoint with protocol beyond a mechanical mapping, because it carries the file-level OCC contract (see the OCC design note):

- The client uploads the commit's objects first, then submits the commit: message, author, parents, and for each file its `path`, `object_hash`, `change_type`, and `based_on_commit_id` (the per-file base).
- The server validates atomically: for every file, `based_on_commit_id` must still equal the current `file_branch_heads` commit. If all match, the commit is assigned its `seq`, the heads are updated, and it is accepted. If any file fails, the entire commit is rejected and nothing is applied.
- Because objects are written before the referencing metadata (see Read consistency), a rejected push leaves only unreferenced objects, never dangling metadata.
- The rejection response names each conflicting file and the current head commit that beat it, so the client can fetch theirs, merge, and retry.

### Error model
Errors use one consistent JSON shape (a stable `code`, a human `message`, and optional structured `detail`). The push rejection is the error clients parse programmatically: its `detail` carries the list of conflicting `(path, current_head_commit_id)` pairs.

### Worked example: a push

A single illustrative request, the most complex command. The client has already uploaded the new objects (object-before-metadata), and now submits a **merge commit** (two parents) that modifies one file and deletes another. Hashes, ids, and the signature are truncated and not real.

```
POST /acme-strategy/commits HTTP/1.1
Host: store.acme.example
X-AgentStore-Proto: 1
X-AgentStore-Principal: principal_4a8f2c1e
X-AgentStore-Timestamp: 1717286400123
X-AgentStore-Signature: bm90LWEtcmVhbC1zaWduYXR1cmU=
Content-Type: application/json

{
  "message": "Merge ICP edits; drop stale segment note",
  "created_at": 1717286400000,
  "parents": [
    "92af3b1c4d8e...e1",
    "7c2d9a0b5f3a...4b"
  ],
  "files": [
    {
      "path": "/strategy/icp.md",
      "change_type": "modified",
      "object_hash": "ab12cd34ef...90",
      "based_on_commit_id": "7c2d9a0b5f3a...4b"
    },
    {
      "path": "/strategy/segments-old.md",
      "change_type": "deleted",
      "object_hash": null,
      "based_on_commit_id": "5e1f8b22c7d4...aa"
    }
  ]
}
```

Notes that the example is meant to make concrete:

- **Two timestamps, different jobs.** The `X-AgentStore-Timestamp` header (`...400123`) is the *request* time, checked against the freshness window. The body's `created_at` (`...400000`) is the *commit* time that feeds the commit id. They are independent.
- **Author is not in the body.** The commit is attributed to the authenticated signer (`principal_4a8f2c1e`); the server fills `author_id` from the verified identity, so it can't be forged in the payload.
- **`parents` has two entries**, so this is a merge commit (the local commit plus the head it merged against). A normal commit would have one; the first commit in a repo, zero.
- **The deleted file carries `object_hash: null`** and still carries a `based_on_commit_id`, the version it was last seen at, which the OCC check validates like any other file.
- **`based_on_commit_id` is per-file and is OCC metadata, not part of the commit id.** It tells the server which version each change was based on; it is not in the hashed commit content.

How the server processes it:

1. **Verify the envelope.** Rebuild the `agentstore-request-v1` preimage from `principal_id`, method `POST`, target `/acme-strategy/commits`, the header `timestamp`, and `SHA-256(body)`; verify the signature against the principal's public key in the directory; reject if the timestamp is outside the freshness window.
2. **Authorize.** Check the signer has `write` on `/strategy/icp.md` and `/strategy/segments-old.md`.
3. **File-level OCC.** For each file, confirm `based_on_commit_id` still equals the current `file_branch_heads`. If any differs, reject the whole commit.
4. **Commit.** Recompute the commit id from the canonical content (message, author = signer, `created_at`, parents, and the `(path, op, object_hash)` set, with `change_type` mapped to set/delete), assign `seq`, update the heads, and emit the events.

On an OCC conflict, step 3 fails and the response is the rejection error, for example:

```
HTTP/1.1 409 Conflict
Content-Type: application/json

{
  "code": "push_conflict",
  "message": "1 file changed since your base; pull and retry",
  "detail": {
    "conflicts": [
      { "path": "/strategy/icp.md", "current_head_commit_id": "d4e5f6a7...c2" }
    ]
  }
}
```

### Endpoint surface (deferred)
The mapping below is for orientation only; request and response schemas are settled during implementation.

| CLI | Server operation |
|---|---|
| `init` | create an empty repo at `/<name>`; caller becomes first admin + owner of `/*` |
| `clone` / `pull` | fetch permission-filtered objects and metadata (heads, commits) |
| `push` | submit commit, file-level OCC validation, accept or reject (above) |
| `log` / `show` | permission-filtered query over `commits` (the `seq` cursor) |
| `grant` / `revoke` | mutate `grants` (current state; audited to the server log) |
| `register` / `bind` / `principals` | `directory` operations in `server.db` (`register` mints an id; the open `GET /_directory` plane serves both `bind`'s `?username=` lookup and `principals list --remote`'s no-param enumeration) |
| `push --mirror` | bootstrap a repo onto an empty server; self-authenticated against the payload roster's admin (no pre-registration) |
| `watch` | WebSocket event stream (see Event protocol) |

---

## Event protocol

This section defines both the **client-facing contract** of the event stream and the **server-side delivery mechanism** (an in-memory fan-out hub, best-effort in v0.1). The one piece left for later is cross-instance fan-out for horizontally-scaled deployments, covered under *Scope* at the end.

### Transport
Clients open a persistent WebSocket via `store watch`. Historical queries use `log` over the HTTP API; `watch` is the live counterpart. Both are permission-filtered: a principal only receives events and log entries for paths it can read. `watch` emits JSON only; `log` is human-readable by default (like git) with a `--json` flag for the event/commit JSON shapes below.

### Event types
Four events, all derived from accepted commits:

```
file.created      # a commit added a file
file.modified     # a commit changed a file's content
file.deleted      # a commit removed a file
commit.pushed     # a commit was accepted into the store
```

The `file.*` events come from each row's `change_type` in `commit_files`. There is no `folder.changed` event — watching a path is hierarchical and already covers its subtree. Permission changes are not events (control-plane state, re-evaluated per request). A rejected push is a synchronous error to the pusher, not a broadcast event.

### Event JSON

File events (`file.created` / `file.modified` / `file.deleted`):
```json
{
  "cursor": 4257,
  "type": "file.modified",
  "timestamp": 1717286400123,
  "path": "/strategy/icp.md",
  "commit_id": "92af3b1c4d...",
  "author_id": "principal_4a8f...",
  "object_hash": "ab12cd...",
  "size": 2048
}
```
`object_hash` and `size` are null for `file.deleted`. Events carry metadata and paths only — never file content; a consumer fetches content separately if it needs it.

`commit.pushed` (commit-level summary):
```json
{
  "cursor": 4257,
  "type": "commit.pushed",
  "timestamp": 1717286400123,
  "commit_id": "92af3b1c4d...",
  "author_id": "principal_4a8f...",
  "message": "Update ICP from customer calls",
  "paths": ["/strategy/icp.md"]
}
```

### Cursor contract
`cursor` is the commit's `seq` — opaque to clients, which never parse it. A client resumes with `watch --cursor <token>` / `log --cursor <token>`, and the server returns commits with `seq > token`. Because the bound is strict, there are no boundary ties, no missed commits, and no client-side dedupe.

A single commit emits several events (one `file.*` per file, then a terminal `commit.pushed`), **all sharing that commit's `cursor`**. Delivery is therefore **commit-atomic**: the events of a commit arrive as a group, `commit.pushed` last as the "end of commit" marker, and a client advances its stored cursor only after consuming the whole group. The cursor cannot resume mid-commit by design — the commit is the atomic unit.

**Cursor advancement is not gated on `commit.pushed` when `--events` filters it out.** Because every event of a commit carries the same `seq`, a client that filters the stream (e.g. `watch --events file.modified`) advances its recovery cursor on whatever event it does receive for that commit. The terminal `commit.pushed` is a convenience marker, not the only carrier of the cursor — filtering it must not stall resumability.

**Live gaps vs. completeness (v0.1, best-effort).** A per-subscriber gap in the live `seq` sequence is *normal and expected*: a commit none of whose files the subscriber can read produces no live event (the advisory stream is not padded with stubs), and a slow-subscriber drop removes a whole group. The live stream therefore **cannot** distinguish "filtered" from "dropped," and clients must **not** treat a live gap as an error. Completeness is the job of the **authoritative log path** (`log --cursor`), where inaccessible commits appear as stubs so the `seq` space is contiguous and verifiable. A consumer that needs guaranteed completeness reads `log --cursor`; the live stream is a low-latency advisory hint. On reconnect, the client resumes from its last cursor and the server replays the gap from the durable log (best-effort catch-up), which closes the common disconnect case; a mid-stream drop that also needs recovery is closed by a `log --cursor` read. This is the deliberate v0.1 split — server-enforced reliable live delivery (acked positions, redelivery) is a documented post-v0.1 addition that builds on the same cursor.

### Delivery guarantee — best-effort in v0.1
The live stream is **best-effort**: the server pushes events to currently-connected subscribers and keeps no per-subscriber delivery state, no acknowledgements, and no redelivery queue. What makes this safe rather than lossy is the split between two channels with different roles:

- The **live stream (`watch`) is advisory** — a fast-path "something changed near here, here's the payload."
- The **durable commit log (`log --cursor`) is authoritative** — the permanent record, retrievable by `seq` at any time.

So a dropped live event never loses knowledge of a change: the commit is durably in SQLite and the client recovers it from the log. A well-behaved client (one that follows the reconnect-with-cursor pattern) is therefore *eventually complete* even though the live channel is at-most-once. Server-enforced reliable delivery (acked positions, redelivery, delivery receipts — for SLA/audit use cases) is a post-v0.1 additive feature that builds on the same `seq` cursor without changing the event format or the client contract.

### Fan-out mechanism
Because delivery is best-effort, the live path is a pure in-memory pub/sub hub and **never reads the database** — catch-up reads happen only on the separate `log --cursor` query path, paced by the client. The two paths are independent.

- **Build once, fan out many.** When a push is accepted, the writer already holds the commit and its `commit_files` in memory, so it constructs the event group directly — zero extra reads — and publishes it to the hub *after* the SQLite commit succeeds (never emit for an unpersisted commit). Because the single writer is serialized, events are published in `seq` order, so subscribers receive them ordered with no reordering logic.
- **One hub goroutine owns the subscriber registry.** Register / unregister / publish all arrive over channels, so the registry is owned by a single goroutine — no mutex. The accept path hands the event group to the hub and returns immediately, so fan-out never blocks the writer.
- **Each connection = two goroutines + a bounded send buffer.** A reader goroutine detects disconnects and control frames; a writer goroutine drains a bounded buffered channel to the WebSocket. The hub fans out with a non-blocking send: if a subscriber's buffer is full, the event group is dropped for that subscriber and the hub moves on.

### Drop-and-reconcile (the best-effort policy)
When a subscriber is too slow and its send buffer fills, the hub **drops that commit's entire event group** for that subscriber — atomically, never a partial commit — and never retries. The client recovers via the cursor:

- Every event carries the commit's strictly-increasing `seq` as `cursor`, so a drop shows up as a **gap** — e.g. the client processed `4257`, then the next event it receives is `4260`.
- On detecting the jump, the client pulls the missing range from the durable log (`log --cursor 4257 --to-cursor 4260`), applies it in order, then resumes the live stream at `4260`.

This keeps server memory bounded and never blocks the writer, while the cursor doubles as both the delivery position and the gap detector. It depends on the three contract properties above: a strictly-increasing per-commit cursor, commit-atomic delivery (so a gap is always a clean commit boundary), and clients treating the live stream as advisory.

A `watch` that resumes from a position (`--cursor` / `--since`) is the same idea applied at connect time: the server does a best-effort catch-up read from the cursor, then attaches the connection to the live hub. Any seam between "end of catch-up" and "start of live" is just another gap the client reconciles — there is no need for a perfectly seamless handoff, which is exactly the complexity best-effort lets us avoid.

### Permission filtering reflects current grants
Filtering (path-prefix ∩ event-type ∩ permission) is applied per subscriber at publish time. Permission must be evaluated against **current** grants, not a snapshot taken at subscribe time — a revoke is immediate and retroactive, so a freshly-revoked principal must stop receiving events (path names alone can be sensitive). An in-memory grants cache, invalidated on grant/revoke, avoids a per-event database lookup.

A commit none of whose files a subscriber can read produces **no live event** for that subscriber (the advisory stream is not padded with stubs). The subscriber's seq space is made contiguous on the authoritative log path instead, where inaccessible commits appear as stubs, see Server API: Permission-filtered history. For a partly-visible commit, the subscriber receives the `file.*` events for readable paths and a `commit.pushed` whose `paths` is filtered to those.

### Scope — single process
The in-memory hub assumes one server process, which is the v0.1 deployment (SQLite, single writer). Horizontal scaling across multiple server instances would need cross-instance pub/sub (e.g. Redis or NATS) so an event accepted on one instance reaches subscribers on another. This pairs with the PostgreSQL/multi-writer upgrade path and is a deferred concern.

---

## Server log

The server writes a general application log (startup/shutdown, request errors, push rejections, connection churn) to stdout/stderr in the usual way. v0.1 adds no dedicated logging infrastructure beyond this.

**Control-plane audit lines.** Identity and access control actions — `register`, `principals add/remove`, `grant`/`revoke`, admin-role changes, `rekey` — are emitted into this same log as **structured lines** (JSON per line: `actor`, `action`, `target`, `ts`). This is for accountability and is deliberately *not* a queryable store: it is distinct from the versioned commit ledger (which records data changes) and from the event stream (which notifies subscribers). The `grants` table records only *current* permission state, so this log is the sole record of permission *history* (who granted or revoked what, and when) in v0.1. A queryable, replicated grant history in the database is a possible future addition for stronger compliance.

Structuring the lines now keeps them greppable and lets a future `store audit` query surface promote them into a table without re-instrumenting the server.

---

## Platform

**Target platforms (v0.1): macOS and Linux**, on both amd64 and arm64. Ubuntu is the reference Linux distribution (what CI builds and tests against); other modern distros should work but are not the tested target. Windows is not a v0.1 target. It is a planned follow-on, and the known gaps to close are signal handling (there is no real `SIGTERM`, so the graceful drain and `server stop` need a Windows-specific path), file-mode key protection (`chmod 0600` is meaningless under Windows ACLs), and the filesystem-semantics hazards noted below.

**Pure-Go SQLite.** AgentStore uses a cgo-free SQLite driver (`modernc.org/sqlite`) rather than the cgo-based `mattn/go-sqlite3`. This preserves Go's trivial cross-compilation: a single static binary per `GOOS`/`GOARCH` with no C toolchain to manage. It is marginally slower than the cgo driver, an acceptable trade for the portability and build simplicity that match the single-binary, self-hostable design.

**OS-independent logical paths.** A repo's paths belong to the data model, not the host filesystem, so they are defined uniformly regardless of OS:

- **Forward-slash separators** (`/strategy/icp.md`). The host separator is used only when materializing the working tree, via `path/filepath`.
- **Case-sensitive.** `/Notes.md` and `/notes.md` are distinct paths in the model. Case-insensitive host filesystems (the default on macOS) cannot materialize both at once; this is a known limitation, the same class of problem git has, addressed when it becomes relevant.
- **Bytes stored verbatim.** No line-ending normalization (no autocrlf), so a file's content hash is identical across platforms.

Windows-only path hazards (reserved names such as `CON`/`NUL`, illegal characters, trailing dots) are deferred along with the rest of Windows support.
