# Testing

How the AgentStore test suite is organized, how to run it, and the rules to follow
when you change the CLI. This covers both the in-code Go tests and the CLI
**coverage gate**.

## Philosophy

From [`AGENTS.md`](../AGENTS.md): integration tests over mocks where the system
boundary matters (storage, events, permissions); tests reflect real usage
patterns, not implementation details. In practice that means most behavior is
proven through a real store, a real server socket, and the real CLI command tree
rather than stubs.

## Layers

### 1. Unit tests — pure logic

Close to the code under test, no server.

| Package | Covers |
|---------|--------|
| `internal/store` | object store + dedupe, commit log + monotonic seq, staging/commit, path validation (`path_test.go`), path access control grants (`access_test.go`) |
| `internal/merge` | 3-way merge + conflict markers; fixtures in `internal/merge/testdata` |
| `internal/canonical` | canonical serialization (stable hashing of commits/objects) |
| `internal/identity` | ed25519 keypair handling, signing |
| `internal/storage/sqlite` | the SQLite storage backend |

### 2. CLI tests — `internal/cli`

| File | Covers |
|------|--------|
| `cli_test.go` | local command flows (add → commit → log → checkout) against a temp repo via `testutil.NewRepo` |
| `config_test.go` | the `config` command (global/local TOML get/set, `--list`) |
| `manifest_test.go` + `manifest_data_test.go` | the **drift guard** for the coverage gate (see below) |

### 3. Server / integration tests — `internal/server` (the bulk)

These drive a real server. Two harnesses, both in `package server_test`:

- **`testServer(t)`** — in-process via `httptest.NewServer(srv.Handler())`. Fast,
  loopback handler; used by most tests.
- **`realServer(t)`** — a genuine TCP bind on `0.0.0.0:0`, so the non-loopback
  `Serve` path runs. This is the only thing that exercises the **watch WebSocket
  upgrade**, the signed handshake, and event framing over a real socket
  (`serve_test.go`).

Coverage by file:

| File | Covers |
|------|--------|
| `server_test.go` | OCC (non-overlapping vs conflict), push validation (traversal/hash/missing-object/malformed-delete), repo-name validation, `pull` fast-forward of a modified file (`TestPullFastForwardModifiedFile`) |
| `access_test.go` | permission-filtered clone, read-only denial, non-member denial, grant-pattern validation, principal removal cascade |
| `watch_test.go` | live events + recovery, revoke mid-stream, `--events` filter + cursor resumability |
| `hub_test.go` | event fan-out, path filtering, permission filtering, slow-consumer isolation |
| `mirror_test.go` | `push --mirror` repo move, history pagination, username-collision auto-rename, target-limit + failure rollback |
| `identity_test.go` | register/bind identity flow, directory browse, unsigned-request rejection |
| `checkout_test.go` | server-side repo rewind to a seq, staged-new-file handling |
| `reset_test.go` | discard unpushed commits (single/multiple/new-file/log-absorption) |
| `limits_test.go` | file-size and binary-content limits |

## Shared helpers

- **`internal/testutil`** — `NewRepo(t)` returns a fully initialized local repo in
  a `t.TempDir()` (store + index, with cleanup), plus `WriteFile`/`ReadFile`.
  Note: a test using `NewRepo` must not call `t.Parallel()` if it also changes the
  working directory.
- **`internal/server/server_test.go`** — `testServer`, `realServer`,
  `registerUser`, `publicKeyLine`, `openRepo`, `writeFile`/`readFile`.
- Test files use the **external** `_test` package (`package store_test`,
  `package server_test`, …) so they exercise the public API.

## The CLI coverage gate (cross-cutting)

Separate from per-package tests, the gate proves that **every `store` command and
flag is exercised at least once**. It has two halves:

1. **Drift guard** — `internal/cli/manifest_test.go`. Walks the real cobra tree
   (`cli.Root()`) and asserts it matches the unit list in
   `manifest_data_test.go` (`prdOrder` + `commandGroup`), which also generates
   `coverage-demo/manifest.json`. Each bare command also carries its positional-arg
   arity (`args_min`/`args_max`), derived by probing the real `Args` validator, so
   changing a command's arity (e.g. `ExactArgs(1)` → `RangeArgs(1,2)`) makes the
   committed manifest stale. Runs as part of `go test`. **Add or remove a
   command/flag, or change a command's arity, and this test goes red.**
2. **Coverage gate** — `coverage-demo/run.sh` drives the CLI against two loopback
   servers and asserts every manifest unit was actually invoked. Runs as its own
   CI job. See [`coverage-demo/README.md`](../coverage-demo/README.md).
3. **Docs binding** — `internal/cli/docs_test.go` parses the CLI-reference blocks
   of `README.md` and the PRD and resolves each line against `cli.Root()`:
   - `TestREADMEMatchesCLI` — the README may be a subset, but must not document a
     command or flag the CLI doesn't expose (catches phantoms like `watch --since`).
   - `TestPRDMatchesCLI` — the PRD must document every command and reference no
     phantom command/flag (flag *coverage* is the drift guard's job).
   - `TestDocArityMatchesCLI` — a flag-free signature line must imply the same
     positional arity as the command (the `init <url> [<directory>]` guard).
4. **Skill content** — `internal/skill/drift_test.go`. The exported agent skill
   under `internal/skill/content/` (`SKILL.md` + `reference/cli.md`) is generated
   from `templates/SKILL.md.tmpl` and `cli.Root()` (see `internal/skill/gen`,
   shared via `internal/skill/skillgen`). The test re-renders and compares against
   the files on disk, so adding a command — or editing the template — without
   running `go generate ./internal/skill` goes red. Runs as part of `go test`.

### Contract: when you change the CLI surface

Adding, removing, or renaming a command or flag is a six-step change:

1. `go test ./internal/cli -run 'Manifest|README|PRD|DocArity'` → it fails, naming
   the new/removed unit or the doc that drifted.
2. Update `prdOrder` (and `commandGroup` if it's a new command) in
   `internal/cli/manifest_data_test.go`.
3. Regenerate the manifest: `UPDATE_MANIFEST=1 go test ./internal/cli -run Manifest`.
4. Document the command in the README and PRD CLI-reference sections (and keep a
   flag-free signature line's positional args matching the command's arity).
5. Add a `step`/`mark` in `coverage-demo/driver.sh` that exercises the new unit,
   then run `coverage-demo/run.sh` to confirm full coverage.
6. Regenerate the embedded skill so its CLI reference includes the change:
   `go generate ./internal/skill`.

Skipping step 5 leaves the gate red; skipping steps 2–3 leaves the manifest drift
test red; skipping step 4 leaves the docs-binding tests red; skipping step 6
leaves the skill-content drift test red.

## Running tests

```sh
go test ./...                                   # everything (fast, in-process)
go test -race ./...                             # what CI runs
go vet ./...
go test ./internal/cli -run Manifest            # just the drift guard
coverage-demo/run.sh                            # the full command-coverage gate
```

`coverage-demo/run.sh` additionally needs `jq`, `sqlite3`, `ssh-keygen`, and
`curl` on PATH (it builds and drives the real binary against live servers).

## CI

[`.github/workflows/ci.yml`](../.github/workflows/ci.yml):

- **test** — `go vet` + `go test -race ./...` on an `ubuntu-latest` + `macos-latest`
  matrix (Linux reference + real arm64 macOS). The drift guard runs here.
- **build** — cross-compiles `./cmd/store` for linux/darwin × amd64/arm64.
- **coverage-demo** — runs `coverage-demo/run.sh` on `ubuntu-latest`; fails on any
  uncovered command or flag.

## Where to add a new test

- Logic in a package (merge rule, hashing, path resolution) → a `_test.go` in that
  package, unit style.
- A behavior that crosses the client/server boundary (auth, push/pull, events,
  access control, mirror) → `internal/server`, using `testServer` (or `realServer` if it
  needs a real socket / WebSocket).
- A local CLI flow (no server) → `internal/cli` via `testutil.NewRepo`.
- A new command or flag → follow the **CLI surface contract** above (drift guard +
  manifest + coverage driver), in addition to a behavior test for what it does.
