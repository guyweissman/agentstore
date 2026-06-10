// Package storage defines the database abstraction used throughout AgentStore.
// All query code depends on this interface so the underlying backend (SQLite today,
// PostgreSQL later) can be swapped without rewriting query logic.
package storage

import (
	"context"
	"database/sql"
)

// Querier covers the query methods shared by *sql.DB and *sql.Tx.
// Pass a Querier to any function that must work inside or outside a transaction.
type Querier interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// DB is the full database handle — supports Querier plus transaction control.
type DB interface {
	Querier
	BeginTx(ctx context.Context, opts *sql.TxOptions) (*sql.Tx, error)
	PingContext(ctx context.Context) error
	Close() error
}
