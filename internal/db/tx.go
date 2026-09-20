package db

import (
	"context"
	"database/sql"
)

// WithTx runs fn inside a transaction, committing on success and rolling
// back on any error. Use for multi-step writes that must be atomic.
func WithTx(ctx context.Context, sqlDB *sql.DB, fn func(*Queries) error) error {
	tx, err := sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	q := New(tx)
	if err := fn(q); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}