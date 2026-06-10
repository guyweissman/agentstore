# AGENTS.md

Coding guidelines for this repository. Read this before making changes.

## Project context

AgentStore is an open-source, portable, file-native datastore for agent knowledge work. The specs in `project_spec/` are the source of truth; read them before implementing.

- [`project_spec/agentstore-prd.md`](project_spec/agentstore-prd.md) — **start here** product vision, the full CLI surface, permissions model, and the post-v0.1 roadmap.
- [`project_spec/architecture.md`](project_spec/architecture.md) — technical decisions: storage format, SQLite schema, OCC and conflict resolution, canonical serializations, the server API and auth envelope, the event protocol, and platform targets.
- [`project_spec/schema.html`](project_spec/schema.html) — a visual of the database schema (open in a browser).
- [`project_spec/testing.md`](project_spec/testing.md) — how the test suite is organized (unit, CLI, server/integration, and the CLI coverage gate), how to run it, and the contract to follow when you change the CLI surface.

Naming: the project is **AgentStore** (docs/marketing), the CLI command is `store`, and the Git repository and Go module remain `agentstore`. Brand strings live in `internal/brand`; protocol identifiers (e.g. `agentstore-request-v1`) are deliberately separate and must stay stable per the build plan's "Naming and the brand/protocol split."

## Repository structure

```
project_spec/   # Product specs and vision documents (not code)
```

## Language and runtime

Go. See the Implementation Language section of the PRD for rationale.

## Code style

- Follow the conventions of Go
- No comments unless the WHY is non-obvious (hidden constraint, subtle invariant, workaround for a specific bug)
- Do not explain what code does — name things well instead

## Testing

- Integration tests over mocks where the system boundary matters (storage, events, permissions)
- Tests should reflect real usage patterns, not implementation details
- See [`project_spec/testing.md`](project_spec/testing.md) for the full layout, how to run each layer, and the **CLI surface contract**: changing a command or flag requires updating the manifest drift guard and the coverage driver, not just adding a behavior test.

### The CLI surface is enumerated in several places — keep them in sync

The cobra command tree (`internal/cli/*.go`) is the source of truth. The same surface is restated in several downstream places; most are kept honest by a test that fails the moment they drift, so you rarely sync by hand — you run the test, it tells you what's stale, you fix that one thing.

| Where | Kind | What keeps it honest |
| --- | --- | --- |
| `internal/cli/manifest_data_test.go` (`prdOrder`, `commandGroup`) | hand-maintained | `manifest_test.go` drift guard |
| `coverage-demo/manifest.json` | generated from `prdOrder` | `manifest_test.go` (regenerate with `UPDATE_MANIFEST=1`) |
| `coverage-demo/driver.sh` | hand-maintained | `coverage-demo/run.sh` coverage gate |
| `README.md` / `project_spec/agentstore-prd.md` CLI reference | hand-maintained | `internal/cli/docs_test.go` |
| `internal/skill/content/` (`SKILL.md`, `reference/cli.md`) | **generated** — do not hand-edit | `internal/skill/drift_test.go`; regenerate with `go generate ./internal/skill` |

For generated rows (`coverage-demo/manifest.json`, `internal/skill/content/`), don't edit the output — change the source (the template, `prdOrder`, or the command) and regenerate. Nothing in the build runs `go generate`, so the drift test is what enforces it. The full step-by-step is the **CLI surface contract** in `project_spec/testing.md`.

## Commits

- Commit messages describe why, not what
- Keep commits focused — one logical change per commit

## What not to do

- Do not add features beyond the current task
- Do not add error handling for scenarios that cannot happen
- Do not introduce abstractions until there are at least three concrete cases
