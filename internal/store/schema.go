package store

import (
	"database/sql"
	"fmt"
)

func createSchema(db *sql.DB) error {
	stmts := []string{
		// principals: repo members. M1 seeds a stub; real identities arrive at M3.
		`CREATE TABLE IF NOT EXISTS principals (
			id         TEXT PRIMARY KEY,
			username   TEXT NOT NULL,
			public_key TEXT NOT NULL DEFAULT '',
			created_at INTEGER NOT NULL
		)`,

		// repo_roles: admin role for the control plane.
		// granted_by is a historical attribution (no FK): the granter may later be
		// removed from the repo while this record persists.
		`CREATE TABLE IF NOT EXISTS repo_roles (
			principal_id TEXT NOT NULL REFERENCES principals(id),
			role         TEXT NOT NULL,
			granted_by   TEXT NOT NULL,
			created_at   INTEGER NOT NULL,
			PRIMARY KEY (principal_id, role)
		)`,

		// commits: the commit log.
		// seq is NULL for local commits not yet accepted by the server; updated to
		// the server-assigned value after a successful push.
		// author_id is a historical attribution (no FK): authorship is a permanent
		// fact about the commit and must survive the author's removal from the repo.
		`CREATE TABLE IF NOT EXISTS commits (
			id         TEXT PRIMARY KEY,
			seq        INTEGER UNIQUE,
			message    TEXT NOT NULL,
			author_id  TEXT NOT NULL,
			created_at INTEGER NOT NULL
		)`,

		// commit_parents: one row per parent. Normal = 1, initial = 0, merge = 2.
		`CREATE TABLE IF NOT EXISTS commit_parents (
			commit_id TEXT NOT NULL REFERENCES commits(id),
			parent_id TEXT NOT NULL REFERENCES commits(id),
			ord       INTEGER NOT NULL,
			PRIMARY KEY (commit_id, parent_id)
		)`,

		// files: one row per path ever seen; current-state registry.
		`CREATE TABLE IF NOT EXISTS files (
			path       TEXT PRIMARY KEY,
			created_at INTEGER NOT NULL,
			deleted_at INTEGER
		)`,

		// file_branch_heads: per-file head commit on each branch (main only in v0.1).
		`CREATE TABLE IF NOT EXISTS file_branch_heads (
			file_path TEXT NOT NULL REFERENCES files(path),
			branch    TEXT NOT NULL,
			commit_id TEXT NOT NULL REFERENCES commits(id),
			PRIMARY KEY (file_path, branch)
		)`,

		// commit_files: files changed in each commit. (commit_id, path) is the version.
		// based_on_commit_id is the per-file OCC base sent to the server on push;
		// NULL for newly added files that had no prior server head.
		`CREATE TABLE IF NOT EXISTS commit_files (
			commit_id          TEXT NOT NULL REFERENCES commits(id),
			path               TEXT NOT NULL REFERENCES files(path),
			object_hash        TEXT,
			size               INTEGER NOT NULL DEFAULT 0,
			change_type        TEXT NOT NULL,
			based_on_commit_id TEXT,
			PRIMARY KEY (commit_id, path)
		)`,

		// redacted_commits: seq positions of commits the local principal cannot read
		// at all (delivered as stubs). Stored to keep the local seq space contiguous
		// (verifiable completeness) and to advance the sync cursor past stubs.
		`CREATE TABLE IF NOT EXISTS redacted_commits (
			seq INTEGER PRIMARY KEY
		)`,

		// grants: access control path grants. Enforcement arrives at M3; no grants are seeded in M1.
		// granted_by is a historical attribution (no FK): the granter may later be removed.
		`CREATE TABLE IF NOT EXISTS grants (
			principal_id TEXT NOT NULL REFERENCES principals(id),
			path_pattern TEXT NOT NULL,
			permission   TEXT NOT NULL,
			granted_by   TEXT NOT NULL,
			created_at   INTEGER NOT NULL,
			PRIMARY KEY (principal_id, path_pattern)
		)`,

		// --- Required indexes (hot query paths; present from the start per architecture) ---

		// seq is the sync cursor; must be unique and fast.
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_commits_seq ON commits(seq)`,
		`CREATE INDEX IF NOT EXISTS idx_commits_created_at ON commits(created_at)`,
		`CREATE INDEX IF NOT EXISTS idx_commits_author_id  ON commits(author_id)`,

		// Per-file history queries.
		`CREATE INDEX IF NOT EXISTS idx_commit_files_path      ON commit_files(path)`,
		// commit_id direction is covered by the PK prefix, but path direction needs its own index.

		// Walking the commit DAG (children of a commit).
		`CREATE INDEX IF NOT EXISTS idx_commit_parents_parent ON commit_parents(parent_id)`,

		// Access control permission lookups (every operation checks grants).
		`CREATE INDEX IF NOT EXISTS idx_grants_principal_path ON grants(principal_id, path_pattern)`,

		// Admin-role check on control-plane operations.
		`CREATE INDEX IF NOT EXISTS idx_repo_roles_principal ON repo_roles(principal_id)`,
	}

	for _, stmt := range stmts {
		if _, err := db.Exec(stmt); err != nil {
			return fmt.Errorf("schema: %w\nSQL: %s", err, stmt)
		}
	}
	return nil
}
