# Coverage demo

A demo whose job is **exhaustiveness**: it invokes every `store` command
and every flag at least once, and proves it. It is a *coverage test* with a live
viewer bolted on.

Two things are gated in CI:

1. **Drift guard** — `internal/cli/manifest_test.go` walks the real cobra command
   tree (`cli.Root()`) and asserts it matches `manifest.json` exactly. Add a flag
   to the CLI without updating the demo and `go test ./...` goes red. Regenerate
   the manifest with `UPDATE_MANIFEST=1 go test ./internal/cli -run Manifest`.
2. **Coverage gate** — `run.sh` drives the CLI and asserts every manifest unit
   was actually exercised, failing on any gap.

## Run it headless (what CI runs)

```sh
./run.sh
```

Builds the binary into `.run/bin/store`, runs the driver against two loopback
servers, then prints a grouped ✓/✗ report and exits non-zero if any command or
flag was missed. The EXIT trap drains both servers. Requires `go`, `jq`,
`sqlite3`, `ssh-keygen`, `curl`.

## How coverage is tracked

`driver.sh` runs steps in **dependency** order (not PRD order). Each invocation
that counts emits one NDJSON line to `.run/events.ndjson` via `step`/`mark` in
`lib.sh`, tagged with the manifest ids it covers — so display order (the
checklist) and execution order (the driver) stay decoupled. The same human-
readable output goes to stdout for the terminal pane.

## Notes / known issues

- **`store merge` (bare)** exits non-zero by design — there is no merge in
  progress when it runs. The command is still exercised; only *missing* units
  fail the gate, not non-zero exits.
- Permission-filtered (partial) clones cannot `pull` commits whose parents touch
  paths they never received; the `pull` coverage therefore uses a full owner
  clone.
