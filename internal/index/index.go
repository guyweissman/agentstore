// Package index manages .agentstore/index.db — the local working state that
// is never pushed to the server: staged changes, merge-in-progress marker.
package index

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/guyweissman/agentstore/internal/brand"
	"github.com/guyweissman/agentstore/internal/storage/sqlite"
)

// Index is a handle to index.db.
type Index struct {
	db *sql.DB
}

// StagedEntry is one row from the staged table.
type StagedEntry struct {
	Path            string
	ObjectHash      string // "" for a staged deletion
	ChangeType      string // "added", "modified", "deleted"
	BasedOnCommitID string // "" if file was not in HEAD when staged
}

// MergeState records an in-progress merge after a conflicting pull.
type MergeState struct {
	SecondParentCommitID string
}

// Open opens an existing index.db from storeDir (.agentstore/).
func Open(storeDir string) (*Index, error) {
	db, err := sqlite.Open(filepath.Join(storeDir, brand.IndexDB))
	if err != nil {
		return nil, err
	}
	if err := createIndexSchema(db); err != nil {
		db.Close()
		return nil, err
	}
	return &Index{db: db}, nil
}

// Init creates (or opens) index.db in storeDir with the schema applied.
// Safe to call on an already-initialized index.
func Init(storeDir string) (*Index, error) {
	return Open(storeDir)
}

// Close releases the database connection.
func (i *Index) Close() error { return i.db.Close() }

// Stage upserts a staged entry. For a deletion, set ObjectHash = "".
func (i *Index) Stage(e StagedEntry) error {
	var objHash interface{}
	if e.ObjectHash != "" {
		objHash = e.ObjectHash
	}
	var basedOn interface{}
	if e.BasedOnCommitID != "" {
		basedOn = e.BasedOnCommitID
	}
	_, err := i.db.ExecContext(context.Background(), `
		INSERT INTO staged (path, object_hash, change_type, based_on_commit_id) VALUES (?, ?, ?, ?)
		ON CONFLICT(path) DO UPDATE SET
			object_hash = excluded.object_hash,
			change_type = excluded.change_type,
			based_on_commit_id = excluded.based_on_commit_id`,
		e.Path, objHash, e.ChangeType, basedOn)
	return err
}

// Unstage removes the staged entry for path, if any.
func (i *Index) Unstage(path string) error {
	_, err := i.db.ExecContext(context.Background(),
		`DELETE FROM staged WHERE path = ?`, path)
	return err
}

// Entries returns all staged entries.
func (i *Index) Entries() ([]StagedEntry, error) {
	rows, err := i.db.QueryContext(context.Background(),
		`SELECT path, COALESCE(object_hash, ''), change_type, COALESCE(based_on_commit_id, '')
		 FROM staged ORDER BY path`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []StagedEntry
	for rows.Next() {
		var e StagedEntry
		if err := rows.Scan(&e.Path, &e.ObjectHash, &e.ChangeType, &e.BasedOnCommitID); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// Get returns the staged entry for path, or nil if not staged.
func (i *Index) Get(path string) (*StagedEntry, error) {
	var e StagedEntry
	var objHash, basedOn sql.NullString
	err := i.db.QueryRowContext(context.Background(),
		`SELECT path, object_hash, change_type, based_on_commit_id FROM staged WHERE path = ?`,
		path).Scan(&e.Path, &objHash, &e.ChangeType, &basedOn)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	e.ObjectHash = objHash.String
	e.BasedOnCommitID = basedOn.String
	return &e, nil
}

// Clear removes all staged entries (called after a successful commit).
func (i *Index) Clear() error {
	_, err := i.db.ExecContext(context.Background(), `DELETE FROM staged`)
	return err
}

// SetMergeState records an in-progress merge. Replaces any prior state.
func (i *Index) SetMergeState(ms MergeState) error {
	_, err := i.db.ExecContext(context.Background(), `
		INSERT INTO merge_state (id, second_parent_commit_id) VALUES (1, ?)
		ON CONFLICT(id) DO UPDATE SET second_parent_commit_id = excluded.second_parent_commit_id`,
		ms.SecondParentCommitID)
	return err
}

// GetMergeState returns the current merge state, or nil if no merge is in progress.
func (i *Index) GetMergeState() (*MergeState, error) {
	var ms MergeState
	err := i.db.QueryRowContext(context.Background(),
		`SELECT second_parent_commit_id FROM merge_state WHERE id = 1`).Scan(&ms.SecondParentCommitID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return &ms, err
}

// ClearMergeState removes the merge-in-progress marker.
func (i *Index) ClearMergeState() error {
	_, err := i.db.ExecContext(context.Background(), `DELETE FROM merge_state WHERE id = 1`)
	return err
}

// SetUnresolved marks path as having unresolved conflict markers.
func (i *Index) SetUnresolved(path string) error {
	_, err := i.db.ExecContext(context.Background(),
		`INSERT OR IGNORE INTO merge_conflicts (path) VALUES (?)`, path)
	return err
}

// ClearUnresolved marks path as resolved.
func (i *Index) ClearUnresolved(path string) error {
	_, err := i.db.ExecContext(context.Background(),
		`DELETE FROM merge_conflicts WHERE path = ?`, path)
	return err
}

// UnresolvedPaths returns all paths still marked as having conflict markers.
func (i *Index) UnresolvedPaths() ([]string, error) {
	rows, err := i.db.QueryContext(context.Background(),
		`SELECT path FROM merge_conflicts ORDER BY path`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var paths []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		paths = append(paths, p)
	}
	return paths, rows.Err()
}

func createIndexSchema(db *sql.DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS staged (
			path                TEXT PRIMARY KEY,
			object_hash         TEXT,
			change_type         TEXT NOT NULL,
			based_on_commit_id  TEXT
		)`,
		`CREATE TABLE IF NOT EXISTS merge_state (
			id                      INTEGER PRIMARY KEY CHECK (id = 1),
			second_parent_commit_id TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS merge_conflicts (
			path TEXT PRIMARY KEY
		)`,
	}
	for _, stmt := range stmts {
		if _, err := db.Exec(stmt); err != nil {
			return fmt.Errorf("index schema: %w", err)
		}
	}
	return nil
}
