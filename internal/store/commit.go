package store

import (
	"context"
	"database/sql"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/guyweissman/agentstore/internal/canonical"
)

const mainBranch = "main"

// CommitRecord is the input to WriteCommit.
type CommitRecord struct {
	Message   string
	AuthorID  string
	CreatedAt int64    // Unix ms; if 0, set to now
	Parents   []string // hex commit IDs (decoded to raw bytes for hashing)
	Files     []CommitFileRecord
}

// CommitFileRecord is one file entry in a CommitRecord.
type CommitFileRecord struct {
	Path            string
	ObjectHash      string // "" for a deletion
	Size            int64
	ChangeType      string // "added", "modified", "deleted"
	BasedOnCommitID string // "" for newly added files; used by push for OCC check
}

// validateCommitFiles enforces the structural invariant shared by every commit
// write path (local and server-side): a commit has at least one file, and each
// file's ChangeType is consistent with its ObjectHash (set for added/modified,
// empty for deleted). This is what keeps a malformed "deleted-with-content" entry
// from ever reaching the store.
func validateCommitFiles(files []CommitFileRecord) error {
	if len(files) == 0 {
		return fmt.Errorf("commit must contain at least one file change")
	}
	for _, f := range files {
		switch f.ChangeType {
		case "added", "modified":
			if f.ObjectHash == "" {
				return fmt.Errorf("%s: %s requires a non-empty ObjectHash", f.Path, f.ChangeType)
			}
		case "deleted":
			if f.ObjectHash != "" {
				return fmt.Errorf("%s: deleted entry must have empty ObjectHash", f.Path)
			}
		default:
			return fmt.Errorf("%s: unknown ChangeType %q", f.Path, f.ChangeType)
		}
	}
	return nil
}

// WriteCommit writes the commit to the store in a single transaction and returns the commit ID.
// The object store must already contain every object referenced by the commit
// (object-before-metadata write ordering).
func (s *Store) WriteCommit(rec CommitRecord) (string, error) {
	if err := validateCommitFiles(rec.Files); err != nil {
		return "", err
	}

	if rec.CreatedAt == 0 {
		rec.CreatedAt = time.Now().UnixMilli()
	}

	// Build parents as raw 32-byte slices for the commit ID hash.
	parentBytes := make([][]byte, len(rec.Parents))
	for i, p := range rec.Parents {
		b, err := hex.DecodeString(p)
		if err != nil || len(b) != 32 {
			return "", fmt.Errorf("invalid parent commit id %q", p)
		}
		parentBytes[i] = b
	}

	// Build canonical files for hashing.
	canonFiles := make([]canonical.CommitFile, len(rec.Files))
	for i, f := range rec.Files {
		var objHash []byte
		if f.ObjectHash != "" {
			var err error
			objHash, err = hex.DecodeString(f.ObjectHash)
			if err != nil || len(objHash) != 32 {
				return "", fmt.Errorf("invalid object hash for %s", f.Path)
			}
		}
		canonFiles[i] = canonical.CommitFile{Path: f.Path, ObjectHash: objHash}
	}

	commitID := canonical.CommitID(canonical.CommitContent{
		Message:   rec.Message,
		AuthorID:  rec.AuthorID,
		CreatedAt: rec.CreatedAt,
		Parents:   parentBytes,
		Files:     canonFiles,
	})

	ctx := context.Background()
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()

	// seq is NULL for locally-created commits; the server assigns it on push.
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO commits (id, seq, message, author_id, created_at) VALUES (?, NULL, ?, ?, ?)`,
		commitID, rec.Message, rec.AuthorID, rec.CreatedAt,
	); err != nil {
		return "", fmt.Errorf("insert commit: %w", err)
	}

	for i, parentID := range rec.Parents {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO commit_parents (commit_id, parent_id, ord) VALUES (?, ?, ?)`,
			commitID, parentID, i,
		); err != nil {
			return "", fmt.Errorf("insert parent: %w", err)
		}
	}

	now := rec.CreatedAt
	for _, f := range rec.Files {
		if err := upsertFile(ctx, tx, f.Path, now, f.ChangeType); err != nil {
			return "", err
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO commit_files (commit_id, path, object_hash, size, change_type, based_on_commit_id)
			 VALUES (?, ?, ?, ?, ?, ?)`,
			commitID, f.Path, nullableString(f.ObjectHash), f.Size, f.ChangeType,
			nullableString(f.BasedOnCommitID),
		); err != nil {
			return "", fmt.Errorf("insert commit_files: %w", err)
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO file_branch_heads (file_path, branch, commit_id) VALUES (?, ?, ?)
			 ON CONFLICT(file_path, branch) DO UPDATE SET commit_id = excluded.commit_id`,
			f.Path, mainBranch, commitID,
		); err != nil {
			return "", fmt.Errorf("update file_branch_heads: %w", err)
		}
	}

	return commitID, tx.Commit()
}

// LatestCommitID returns the ID of the most recent non-absorbed commit (by rowid).
// Absorbed commits (seq=-1) are skipped so they can never become a parent.
func (s *Store) LatestCommitID() (string, error) {
	var id string
	err := s.DB.QueryRowContext(context.Background(),
		`SELECT id FROM commits WHERE (seq IS NULL OR seq > 0) ORDER BY rowid DESC LIMIT 1`).Scan(&id)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return id, err
}

// MaxConfirmedSeq returns the highest server-confirmed seq the client has seen,
// counting both readable commits and redacted stubs, so the sync cursor advances
// past stubs and they are not re-fetched on the next pull.
func (s *Store) MaxConfirmedSeq() (int64, error) {
	var seq sql.NullInt64
	err := s.DB.QueryRowContext(context.Background(), `
		SELECT MAX(seq) FROM (
			SELECT seq FROM commits WHERE seq > 0
			UNION ALL
			SELECT seq FROM redacted_commits
		)`).Scan(&seq)
	if err != nil {
		return 0, err
	}
	return seq.Int64, nil
}

// RecordRedactedStub records the seq of a commit the local principal cannot read.
func (s *Store) RecordRedactedStub(seq int64) error {
	_, err := s.DB.ExecContext(context.Background(),
		`INSERT OR IGNORE INTO redacted_commits (seq) VALUES (?)`, seq)
	return err
}

// ConfirmSeq updates a commit's seq to the server-assigned value after a successful push.
func (s *Store) ConfirmSeq(commitID string, seq int64) error {
	_, err := s.DB.ExecContext(context.Background(),
		`UPDATE commits SET seq = ? WHERE id = ?`, seq, commitID)
	return err
}

// UnpushedCommit returns the most recent local commit with seq=NULL, or nil if none.
// "Most recent" means highest rowid — i.e. the head of the local branch, which is
// what should be sent to the server on push.
func (s *Store) UnpushedCommit() (*Commit, error) {
	var id string
	err := s.DB.QueryRowContext(context.Background(),
		`SELECT id FROM commits WHERE seq IS NULL ORDER BY rowid DESC LIMIT 1`).Scan(&id)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return s.GetCommit(id)
}

// AbsorbUnpushedCommits marks every seq=NULL commit except exceptID as absorbed.
// The absorbed sentinel is -rowid (unique per row) to avoid UNIQUE conflicts
// when multiple commits are absorbed at once.
func (s *Store) AbsorbUnpushedCommits(exceptID string) error {
	_, err := s.DB.ExecContext(context.Background(),
		`UPDATE commits SET seq = -rowid WHERE seq IS NULL AND id != ?`, exceptID)
	return err
}

// AbsorbAllUnpushedCommits marks all seq=NULL commits as absorbed.
// Uses -rowid as the sentinel so each row gets a unique negative seq.
func (s *Store) AbsorbAllUnpushedCommits() error {
	_, err := s.DB.ExecContext(context.Background(),
		`UPDATE commits SET seq = -rowid WHERE seq IS NULL`)
	return err
}

// Reset discards all unpushed commits and restores file_branch_heads to the
// last server-confirmed state for each affected file. Returns the list of
// affected store paths so the caller can restore the working tree.
// Does nothing and returns nil if there are no unpushed commits.
func (s *Store) Reset() ([]string, error) {
	chain, err := s.UnpushedChain()
	if err != nil {
		return nil, err
	}
	if len(chain) == 0 {
		return nil, nil
	}

	// Collect affected paths before absorbing.
	affected := make(map[string]struct{})
	for _, c := range chain {
		for _, f := range c.Files {
			affected[f.Path] = struct{}{}
		}
	}

	if err := s.AbsorbAllUnpushedCommits(); err != nil {
		return nil, err
	}

	// Repoint file_branch_heads for each affected path.
	var paths []string
	for path := range affected {
		var commitID string
		err := s.DB.QueryRowContext(context.Background(), `
			SELECT c.id FROM commits c
			JOIN   commit_files cf ON cf.commit_id = c.id
			WHERE  cf.path = ? AND c.seq > 0
			ORDER  BY c.seq DESC LIMIT 1`, path).Scan(&commitID)

		if err == sql.ErrNoRows {
			// File was only added in unpushed commits — it never reached the server.
			// Remove the head row so FileHead returns nil.
			if _, err := s.DB.ExecContext(context.Background(),
				`DELETE FROM file_branch_heads WHERE file_path = ? AND branch = ?`,
				path, mainBranch); err != nil {
				return nil, fmt.Errorf("reset head %s: %w", path, err)
			}
		} else if err == nil {
			if _, err := s.DB.ExecContext(context.Background(), `
				INSERT INTO file_branch_heads (file_path, branch, commit_id) VALUES (?, ?, ?)
				ON CONFLICT(file_path, branch) DO UPDATE SET commit_id = excluded.commit_id`,
				path, mainBranch, commitID); err != nil {
				return nil, fmt.Errorf("reset head %s: %w", path, err)
			}
		} else {
			return nil, fmt.Errorf("reset head %s: %w", path, err)
		}
		paths = append(paths, path)
	}
	return paths, nil
}

// UnpushedChain returns the unpushed commits reachable from the newest
// unpushed commit, in push order (oldest first). This is the set that push
// must send to the server. Stale commits not in this chain (e.g. a pre-merge
// rejected commit whose parent chain diverged) are NOT included; they will be
// absorbed by AbsorbAllUnpushedCommits after a successful push.
func (s *Store) UnpushedChain() ([]*Commit, error) {
	head, err := s.UnpushedCommit() // newest unpushed (rowid DESC)
	if err != nil || head == nil {
		return nil, err
	}

	var chain []*Commit
	visited := make(map[string]bool)

	var walk func(id string) error
	walk = func(id string) error {
		if visited[id] {
			return nil
		}
		visited[id] = true

		c, err := s.GetCommit(id)
		if err != nil {
			return nil // not found locally — stop walking this branch
		}
		// Seq==0 means seq IS NULL (unconfirmed). Seq<0 is absorbed. Seq>0 is confirmed.
		// Only follow the chain through unconfirmed commits.
		if c.Seq != 0 {
			return nil
		}
		// Walk parents first so the chain comes out oldest-first.
		for _, parentID := range c.Parents {
			if err := walk(parentID); err != nil {
				return err
			}
		}
		chain = append(chain, c)
		return nil
	}

	if err := walk(head.ID); err != nil {
		return nil, err
	}
	return chain, nil
}

// WriteRemoteCommit writes a commit received from elsewhere, preserving its seq
// (seq == 0 auto-assigns the next seq, used when the server itself calls this).
// Idempotent: skips if the commit ID already exists locally. When expectedID is
// empty the commit ID is recomputed from the canonical content — the authoritative
// path used when the server accepts a push over the full submitted file set. When
// expectedID is non-empty it is trusted verbatim: a permission-filtered clone/pull
// receives only a subset of a commit's files, so it cannot reproduce the canonical
// hash (which covers files it may never see) and must keep the server's commit ID.
// Per-file content integrity is verified independently against each object hash.
func (s *Store) WriteRemoteCommit(rec CommitRecord, seq int64, expectedID string) (string, error) {
	if err := validateCommitFiles(rec.Files); err != nil {
		return "", err
	}

	for _, p := range rec.Parents {
		if b, err := hex.DecodeString(p); err != nil || len(b) != 32 {
			return "", fmt.Errorf("invalid parent commit id %q", p)
		}
	}

	var commitID string
	if expectedID != "" {
		if b, err := hex.DecodeString(expectedID); err != nil || len(b) != 32 {
			return "", fmt.Errorf("invalid commit id %q", expectedID)
		}
		commitID = expectedID
	} else {
		parentBytes := make([][]byte, len(rec.Parents))
		for i, p := range rec.Parents {
			parentBytes[i], _ = hex.DecodeString(p)
		}
		canonFiles := make([]canonical.CommitFile, len(rec.Files))
		for i, f := range rec.Files {
			var objHash []byte
			if f.ObjectHash != "" {
				var err error
				objHash, err = hex.DecodeString(f.ObjectHash)
				if err != nil || len(objHash) != 32 {
					return "", fmt.Errorf("invalid object hash for %s", f.Path)
				}
			}
			canonFiles[i] = canonical.CommitFile{Path: f.Path, ObjectHash: objHash}
		}
		commitID = canonical.CommitID(canonical.CommitContent{
			Message:   rec.Message,
			AuthorID:  rec.AuthorID,
			CreatedAt: rec.CreatedAt,
			Parents:   parentBytes,
			Files:     canonFiles,
		})
	}

	ctx := context.Background()
	// Idempotent: skip if already present.
	var existing string
	err := s.DB.QueryRowContext(ctx, `SELECT id FROM commits WHERE id = ?`, commitID).Scan(&existing)
	if err == nil {
		return commitID, nil // already have it
	}
	if err != sql.ErrNoRows {
		return "", err
	}

	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()

	// Auto-assign seq when the caller passes 0 (server-side push acceptance).
	if seq == 0 {
		if err := tx.QueryRowContext(ctx,
			`SELECT COALESCE(MAX(seq), 0) + 1 FROM commits WHERE seq IS NOT NULL`).Scan(&seq); err != nil {
			return "", fmt.Errorf("assign seq: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO commits (id, seq, message, author_id, created_at) VALUES (?, ?, ?, ?, ?)`,
		commitID, seq, rec.Message, rec.AuthorID, rec.CreatedAt,
	); err != nil {
		return "", fmt.Errorf("insert remote commit: %w", err)
	}
	for i, parentID := range rec.Parents {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO commit_parents (commit_id, parent_id, ord) VALUES (?, ?, ?)`,
			commitID, parentID, i,
		); err != nil {
			return "", fmt.Errorf("insert parent: %w", err)
		}
	}
	now := rec.CreatedAt
	for _, f := range rec.Files {
		if err := upsertFile(ctx, tx, f.Path, now, f.ChangeType); err != nil {
			return "", err
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO commit_files (commit_id, path, object_hash, size, change_type, based_on_commit_id)
			 VALUES (?, ?, ?, ?, ?, ?)`,
			commitID, f.Path, nullableString(f.ObjectHash), f.Size, f.ChangeType,
			nullableString(f.BasedOnCommitID),
		); err != nil {
			return "", fmt.Errorf("insert commit_files: %w", err)
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO file_branch_heads (file_path, branch, commit_id) VALUES (?, ?, ?)
			 ON CONFLICT(file_path, branch) DO UPDATE SET commit_id = excluded.commit_id`,
			f.Path, mainBranch, commitID,
		); err != nil {
			return "", fmt.Errorf("update file_branch_heads: %w", err)
		}
	}
	return commitID, tx.Commit()
}

// upsertFile maintains the files registry row for the given path.
func upsertFile(ctx context.Context, tx *sql.Tx, path string, now int64, changeType string) error {
	switch changeType {
	case "added":
		// INSERT or reset-on-recreate: set created_at, clear deleted_at.
		_, err := tx.ExecContext(ctx, `
			INSERT INTO files (path, created_at, deleted_at) VALUES (?, ?, NULL)
			ON CONFLICT(path) DO UPDATE SET
				created_at = CASE WHEN files.deleted_at IS NOT NULL THEN excluded.created_at ELSE files.created_at END,
				deleted_at = NULL`,
			path, now)
		return err
	case "modified":
		// Row must already exist; nothing to update.
		_, err := tx.ExecContext(ctx,
			`INSERT OR IGNORE INTO files (path, created_at, deleted_at) VALUES (?, ?, NULL)`,
			path, now)
		return err
	case "deleted":
		_, err := tx.ExecContext(ctx,
			`UPDATE files SET deleted_at = ? WHERE path = ?`, now, path)
		return err
	}
	return fmt.Errorf("unknown change_type %q", changeType)
}

func nullableString(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}
