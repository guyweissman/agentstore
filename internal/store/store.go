// Package store manages the local repository: store.db metadata + objects/ content store.
package store

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/guyweissman/agentstore/internal/brand"
	"github.com/guyweissman/agentstore/internal/storage/sqlite"
)

// StubPrincipalID is the local identity placeholder used until M3 adds real auth.
// It is inserted into every new store by Init and replaced by a real principal at M3.
const StubPrincipalID = "principal_00000000000000000000000000000001"
const stubUsername = "local"

// Store is a handle to a local repository store (store.db + objects/).
type Store struct {
	Root    string // working directory (the directory that contains .agentstore/)
	dir     string // absolute path to .agentstore/
	DB      *sql.DB
	Objects *ObjectStore
}

// Init creates a new repository store at repoRoot and seeds the stub principal.
// Used by the local store engine and the test harness, where a placeholder
// identity is needed before real principals exist (M3).
func Init(repoRoot string) (*Store, error) {
	return initStore(repoRoot, true)
}

// InitBare creates a new repository store with NO seeded principal. The server
// uses this so it can seed the real authenticated caller as the first admin and
// owner of /*, without a leftover stub principal in the repo.
func InitBare(repoRoot string) (*Store, error) {
	return initStore(repoRoot, false)
}

func initStore(repoRoot string, seedStub bool) (*Store, error) {
	repoRoot, err := filepath.Abs(repoRoot)
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(repoRoot, brand.StoreDir)
	if err := os.MkdirAll(filepath.Join(dir, brand.ObjectsDir), 0o755); err != nil {
		return nil, fmt.Errorf("create store dir: %w", err)
	}
	db, err := sqlite.Open(filepath.Join(dir, brand.StoreDB))
	if err != nil {
		return nil, err
	}
	if err := createSchema(db); err != nil {
		db.Close()
		return nil, err
	}
	s := &Store{
		Root:    repoRoot,
		dir:     dir,
		DB:      db,
		Objects: newObjectStore(filepath.Join(dir, brand.ObjectsDir)),
	}
	if seedStub {
		if err := s.seedStub(); err != nil {
			s.Close()
			return nil, err
		}
	}
	return s, nil
}

// Open opens an existing repository store rooted at repoRoot.
func Open(repoRoot string) (*Store, error) {
	repoRoot, err := filepath.Abs(repoRoot)
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(repoRoot, brand.StoreDir)
	dbPath := filepath.Join(dir, brand.StoreDB)
	if _, err := os.Stat(dbPath); errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("not an agentstore repo: %s", repoRoot)
	}
	db, err := sqlite.Open(dbPath)
	if err != nil {
		return nil, err
	}
	return &Store{
		Root:    repoRoot,
		dir:     dir,
		DB:      db,
		Objects: newObjectStore(filepath.Join(dir, brand.ObjectsDir)),
	}, nil
}

// FindRoot walks up from startDir looking for .agentstore/store.db.
func FindRoot(startDir string) (string, error) {
	dir, err := filepath.Abs(startDir)
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, brand.StoreDir, brand.StoreDB)); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("not inside an agentstore repo (no %s found)", brand.StoreDB)
		}
		dir = parent
	}
}

// Close releases the database connection.
func (s *Store) Close() error {
	return s.DB.Close()
}

// Dir returns the path to the .agentstore/ directory.
func (s *Store) Dir() string { return s.dir }

// seedStub inserts the stub principal and makes it a repo admin.
// Idempotent — safe to call on an already-seeded store.
func (s *Store) seedStub() error {
	now := nowMS()
	_, err := s.DB.Exec(`
		INSERT OR IGNORE INTO principals (id, username, public_key, created_at)
		VALUES (?, ?, '', ?)`,
		StubPrincipalID, stubUsername, now)
	if err != nil {
		return fmt.Errorf("seed stub principal: %w", err)
	}
	_, err = s.DB.Exec(`
		INSERT OR IGNORE INTO repo_roles (principal_id, role, granted_by, created_at)
		VALUES (?, 'admin', ?, ?)`,
		StubPrincipalID, StubPrincipalID, now)
	return err
}
