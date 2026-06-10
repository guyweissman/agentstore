// Package sqlite provides a storage.DB backed by a pure-Go SQLite driver.
// Using modernc.org/sqlite (cgo-free) preserves trivial cross-compilation
// to a single static binary per GOOS/GOARCH with no C toolchain.
package sqlite

import (
	"context"
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite" // registers the "sqlite" driver with database/sql
)

const driverName = "sqlite"

// Open opens the SQLite database at path, enables WAL mode, and returns the handle.
// The caller is responsible for calling Close when done.
func Open(path string) (*sql.DB, error) {
	db, err := sql.Open(driverName, path)
	if err != nil {
		return nil, fmt.Errorf("sqlite open %s: %w", path, err)
	}
	if err := db.PingContext(context.Background()); err != nil {
		db.Close()
		return nil, fmt.Errorf("sqlite ping %s: %w", path, err)
	}
	// WAL mode: allows concurrent readers during a write.
	// Persists on the database file once set, so one call is sufficient.
	if _, err := db.ExecContext(context.Background(), "PRAGMA journal_mode=WAL"); err != nil {
		db.Close()
		return nil, fmt.Errorf("sqlite wal mode %s: %w", path, err)
	}
	// Foreign key enforcement is off by default in SQLite.
	if _, err := db.ExecContext(context.Background(), "PRAGMA foreign_keys=ON"); err != nil {
		db.Close()
		return nil, fmt.Errorf("sqlite foreign keys %s: %w", path, err)
	}
	return db, nil
}
