package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// Commit is a commit record as read from the store.
type Commit struct {
	ID        string
	Seq       int64 // 0 means not yet server-confirmed (seq IS NULL in DB)
	Message   string
	AuthorID  string
	CreatedAt int64 // Unix ms
	Parents   []string
	Files     []CommitFile
}

// CommitFile is a file entry in a Commit.
type CommitFile struct {
	Path            string
	ObjectHash      string // "" for a deleted file
	Size            int64
	ChangeType      string // "added", "modified", "deleted"
	BasedOnCommitID string // "" for new files; used for OCC on push
}

// FileHead is the current head state for a file on main.
type FileHead struct {
	CommitID   string
	ObjectHash string // "" if the file is deleted in HEAD
	Size       int64
	ChangeType string
}

// FileHead returns the current HEAD for path on main, or nil if the file has never existed.
func (s *Store) FileHead(path string) (*FileHead, error) {
	var commitID, objectHash, changeType sql.NullString
	var size sql.NullInt64
	err := s.DB.QueryRowContext(context.Background(), `
		SELECT fbh.commit_id, cf.object_hash, cf.size, cf.change_type
		FROM   file_branch_heads fbh
		JOIN   commit_files cf ON cf.commit_id = fbh.commit_id AND cf.path = fbh.file_path
		WHERE  fbh.file_path = ? AND fbh.branch = ?`,
		path, mainBranch).Scan(&commitID, &objectHash, &size, &changeType)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("file head %s: %w", path, err)
	}
	return &FileHead{
		CommitID:   commitID.String,
		ObjectHash: objectHash.String,
		Size:       size.Int64,
		ChangeType: changeType.String,
	}, nil
}

// GetCommit returns a commit by its ID, including parents and files.
func (s *Store) GetCommit(id string) (*Commit, error) {
	// Prefix match: accept short IDs (at least 8 chars).
	query := `SELECT id, COALESCE(seq,0), message, author_id, created_at FROM commits WHERE id = ?`
	args := []any{id}
	if len(id) < 64 {
		query = `SELECT id, COALESCE(seq,0), message, author_id, created_at FROM commits WHERE id LIKE ? LIMIT 2`
		args = []any{id + "%"}
	}

	rows, err := s.DB.QueryContext(context.Background(), query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var c Commit
	found := false
	for rows.Next() {
		if found {
			return nil, fmt.Errorf("ambiguous commit prefix %q", id)
		}
		if err := rows.Scan(&c.ID, &c.Seq, &c.Message, &c.AuthorID, &c.CreatedAt); err != nil {
			return nil, err
		}
		found = true
	}
	if !found {
		return nil, fmt.Errorf("commit %q not found", id)
	}

	c.Parents, err = s.commitParents(c.ID)
	if err != nil {
		return nil, err
	}
	c.Files, err = s.commitFiles(c.ID)
	return &c, err
}

// LogFilter controls which commits LogCommits returns.
type LogFilter struct {
	Path     string // limit to commits touching this path (empty = all)
	Author   string // limit to this author_id (empty = all)
	Since    int64  // Unix ms lower bound on created_at (0 = none)
	To       int64  // Unix ms upper bound on created_at (0 = none)
	Cursor   int64  // return commits with seq > Cursor (0 = from the start)
	ToCursor int64  // return commits with seq <= ToCursor (0 = no upper bound)
	Limit    int    // max commits to return (0 = unlimited)
	Reverse  bool   // oldest-first if true
}

// LogCommits queries the commit log, returning commits matching the filter.
func (s *Store) LogCommits(f LogFilter) ([]*Commit, error) {
	args := []any{}
	// Exclude absorbed commits (seq=-1); they are local intermediates superseded
	// by a merge commit and should never appear in history.
	where := "(c.seq IS NULL OR c.seq > 0)"

	if f.Path != "" {
		where += " AND c.id IN (SELECT commit_id FROM commit_files WHERE path = ?)"
		args = append(args, f.Path)
	}
	if f.Author != "" {
		where += " AND c.author_id = ?"
		args = append(args, f.Author)
	}
	if f.Since != 0 {
		where += " AND c.created_at >= ?"
		args = append(args, f.Since)
	}
	if f.To != 0 {
		where += " AND c.created_at <= ?"
		args = append(args, f.To)
	}
	if f.Cursor != 0 {
		where += " AND c.seq > ?"
		args = append(args, f.Cursor)
	}
	if f.ToCursor != 0 {
		where += " AND c.seq <= ?"
		args = append(args, f.ToCursor)
	}

	order := "DESC"
	if f.Reverse {
		order = "ASC"
	}
	// NULL-seq (unconfirmed) commits get a sentinel value so they sort among the
	// newest; rowid breaks ties within the NULL group (preserves insertion order).
	query := fmt.Sprintf(
		`SELECT c.id, COALESCE(c.seq,0), c.message, c.author_id, c.created_at FROM commits c WHERE %s ORDER BY COALESCE(c.seq, 9223372036854775807) %s, c.rowid %s`,
		where, order, order)
	if f.Limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", f.Limit)
	}

	rows, err := s.DB.QueryContext(context.Background(), query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var commits []*Commit
	for rows.Next() {
		var c Commit
		if err := rows.Scan(&c.ID, &c.Seq, &c.Message, &c.AuthorID, &c.CreatedAt); err != nil {
			return nil, err
		}
		commits = append(commits, &c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Populate parents and files for each commit.
	for _, c := range commits {
		c.Parents, err = s.commitParents(c.ID)
		if err != nil {
			return nil, err
		}
		c.Files, err = s.commitFiles(c.ID)
		if err != nil {
			return nil, err
		}
	}
	return commits, nil
}

// historyScope returns a SQL predicate (over the commits alias "co") and its args
// selecting all NON-absorbed commits at or before the reference commit, under the
// single-client linear ordering where confirmed commits (seq>0) always precede
// local unconfirmed ones (seq IS NULL). Absorbed commits (seq<0) are excluded.
func historyScope(c *Commit, refRowid int64) (string, []any) {
	if c.Seq > 0 {
		// Reference is confirmed: only confirmed commits up to its seq.
		return "(co.seq > 0 AND co.seq <= ?)", []any{c.Seq}
	}
	// Reference is local unconfirmed: all confirmed commits, plus local
	// unconfirmed commits up to the reference's rowid.
	return "((co.seq > 0) OR (co.seq IS NULL AND co.rowid <= ?))", []any{refRowid}
}

// refRowid resolves the rowid of the given (already-resolved) commit.
func (s *Store) refRowid(commitID string) (int64, error) {
	var rowid int64
	err := s.DB.QueryRowContext(context.Background(),
		`SELECT rowid FROM commits WHERE id = ?`, commitID).Scan(&rowid)
	return rowid, err
}

// FileAtCommit returns the object hash for path as it existed at the given commit's
// position in history — the most recent version of the path at or before commitID.
// Returns ("", nil) if the path did not exist or was deleted by that point.
func (s *Store) FileAtCommit(commitID, path string) (objectHash string, err error) {
	c, err := s.GetCommit(commitID)
	if err != nil {
		return "", err
	}
	rowid, err := s.refRowid(c.ID)
	if err != nil {
		return "", fmt.Errorf("file at commit rowid: %w", err)
	}
	pred, args := historyScope(c, rowid)

	var hash sql.NullString
	var changeType string
	query := `
		SELECT cf.object_hash, cf.change_type
		FROM   commit_files cf
		JOIN   commits co ON co.id = cf.commit_id
		WHERE  cf.path = ? AND ` + pred + `
		ORDER  BY COALESCE(co.seq, 9223372036854775807) DESC, co.rowid DESC
		LIMIT  1`
	err = s.DB.QueryRowContext(context.Background(), query,
		append([]any{path}, args...)...).Scan(&hash, &changeType)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("file at commit: %w", err)
	}
	if changeType == "deleted" {
		return "", nil
	}
	return hash.String, nil
}

// FilesAtCommit returns path→object_hash for every file that exists (non-deleted)
// at the given commit's position in history. This is the whole-repo snapshot used
// by `checkout <commit> .`.
func (s *Store) FilesAtCommit(commitID string) (map[string]string, error) {
	c, err := s.GetCommit(commitID)
	if err != nil {
		return nil, err
	}
	rowid, err := s.refRowid(c.ID)
	if err != nil {
		return nil, fmt.Errorf("files at commit rowid: %w", err)
	}
	pred, args := historyScope(c, rowid)

	// For each path, take its most recent change at or before the reference,
	// then keep only paths whose latest state is not a deletion.
	query := `
		SELECT path, COALESCE(object_hash, '') FROM (
			SELECT cf.path AS path, cf.object_hash AS object_hash, cf.change_type AS change_type,
			       ROW_NUMBER() OVER (
			           PARTITION BY cf.path
			           ORDER BY COALESCE(co.seq, 9223372036854775807) DESC, co.rowid DESC
			       ) AS rn
			FROM   commit_files cf
			JOIN   commits co ON co.id = cf.commit_id
			WHERE  ` + pred + `
		) WHERE rn = 1 AND change_type != 'deleted'`

	rows, err := s.DB.QueryContext(context.Background(), query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make(map[string]string)
	for rows.Next() {
		var path, hash string
		if err := rows.Scan(&path, &hash); err != nil {
			return nil, err
		}
		result[path] = hash
	}
	return result, rows.Err()
}

// CommitIDBySeq returns the full commit ID for a server-confirmed seq.
func (s *Store) CommitIDBySeq(seq int64) (string, error) {
	var id string
	err := s.DB.QueryRowContext(context.Background(),
		`SELECT id FROM commits WHERE seq = ?`, seq).Scan(&id)
	if err == sql.ErrNoRows {
		return "", fmt.Errorf("no commit with seq %d", seq)
	}
	return id, err
}

// IsAdminOrRootOwner reports whether the principal holds the repo admin role or
// an owner grant on /*. This gates whole-repo operations like `checkout . `.
func (s *Store) IsAdminOrRootOwner(principalID string) (bool, error) {
	var n int
	if err := s.DB.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM repo_roles WHERE principal_id = ? AND role = 'admin'`,
		principalID).Scan(&n); err != nil {
		return false, err
	}
	if n > 0 {
		return true, nil
	}
	if err := s.DB.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM grants WHERE principal_id = ? AND path_pattern = '/*' AND permission = 'owner'`,
		principalID).Scan(&n); err != nil {
		return false, err
	}
	return n > 0, nil
}

// ListHeadPaths returns all paths that currently exist in HEAD (not deleted).
func (s *Store) ListHeadPaths() ([]string, error) {
	rows, err := s.DB.QueryContext(context.Background(), `
		SELECT fbh.file_path
		FROM   file_branch_heads fbh
		JOIN   commit_files cf ON cf.commit_id = fbh.commit_id AND cf.path = fbh.file_path
		WHERE  fbh.branch = ? AND cf.change_type != 'deleted'`,
		mainBranch)
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

// AllFileHeads returns the current head for every file on main (including deleted).
func (s *Store) AllFileHeads() ([]map[string]string, error) {
	rows, err := s.DB.QueryContext(context.Background(), `
		SELECT fbh.file_path, fbh.commit_id, COALESCE(cf.object_hash,''), cf.change_type
		FROM   file_branch_heads fbh
		JOIN   commit_files cf ON cf.commit_id = fbh.commit_id AND cf.path = fbh.file_path
		WHERE  fbh.branch = ?`, mainBranch)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []map[string]string
	for rows.Next() {
		var path, commitID, objectHash, changeType string
		if err := rows.Scan(&path, &commitID, &objectHash, &changeType); err != nil {
			return nil, err
		}
		result = append(result, map[string]string{
			"path": path, "commit_id": commitID,
			"object_hash": objectHash, "change_type": changeType,
		})
	}
	return result, rows.Err()
}

// PathsReferencingObject returns the distinct paths that have ever referenced
// the given object hash. Used to authorize object downloads.
func (s *Store) PathsReferencingObject(hash string) ([]string, error) {
	rows, err := s.DB.QueryContext(context.Background(),
		`SELECT DISTINCT path FROM commit_files WHERE object_hash = ?`, hash)
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

// SeqForCommit returns the seq assigned to a commit.
func (s *Store) SeqForCommit(id string) (int64, error) {
	var seq int64
	err := s.DB.QueryRowContext(context.Background(),
		`SELECT seq FROM commits WHERE id = ?`, id).Scan(&seq)
	return seq, err
}

func (s *Store) commitParents(id string) ([]string, error) {
	rows, err := s.DB.QueryContext(context.Background(),
		`SELECT parent_id FROM commit_parents WHERE commit_id = ? ORDER BY ord`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var parents []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		parents = append(parents, p)
	}
	return parents, rows.Err()
}

func (s *Store) commitFiles(id string) ([]CommitFile, error) {
	rows, err := s.DB.QueryContext(context.Background(),
		`SELECT path, COALESCE(object_hash,''), COALESCE(size,0), change_type, COALESCE(based_on_commit_id,'')
		 FROM   commit_files WHERE commit_id = ? ORDER BY path`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var files []CommitFile
	for rows.Next() {
		var f CommitFile
		if err := rows.Scan(&f.Path, &f.ObjectHash, &f.Size, &f.ChangeType, &f.BasedOnCommitID); err != nil {
			return nil, err
		}
		files = append(files, f)
	}
	return files, rows.Err()
}

// nowMS returns the current time in Unix milliseconds.
func nowMS() int64 {
	return time.Now().UnixMilli()
}
