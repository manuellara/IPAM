package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/alexedwards/argon2id"
	"github.com/manuellara/ipam/internal/db"
)

const minAdminPasswordLen = 12

// EnsureLocalAdmin ensures that a local admin user exists with the specified password.
// If the local admin user does not exist, it will be created.
// If the local admin user exists but has no password or the password does not match,
// it will be updated with the specified password.
func EnsureLocalAdmin(ctx context.Context, q *db.Queries, password string) error {
	if len(password) < minAdminPasswordLen {
		return fmt.Errorf("ADMIN_PASSWORD must be at least %d characters", minAdminPasswordLen)
	}

	admin, err := q.GetLocalAdminUser(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		hash, err := argon2id.CreateHash(password, argon2id.DefaultParams)
		if err != nil {
			return fmt.Errorf("hash admin password: %w", err)
		}
		_, err = q.CreateLocalAdminUser(ctx, &hash)
		return err
	}
	if err != nil {
		return fmt.Errorf("lookup local admin: %w", err)
	}

	if admin.PasswordHash == nil {
		hash, err := argon2id.CreateHash(password, argon2id.DefaultParams)
		if err != nil {
			return fmt.Errorf("hash admin password: %w", err)
		}
		return q.UpdateUserPasswordHash(ctx, db.UpdateUserPasswordHashParams{
			ID:           admin.ID,
			PasswordHash: &hash,
		})
	}

	match, err := argon2id.ComparePasswordAndHash(password, *admin.PasswordHash)
	if err != nil {
		return fmt.Errorf("compare admin password: %w", err)
	}
	if !match {
		hash, err := argon2id.CreateHash(password, argon2id.DefaultParams)
		if err != nil {
			return fmt.Errorf("hash admin password: %w", err)
		}
		return q.UpdateUserPasswordHash(ctx, db.UpdateUserPasswordHashParams{
			ID:           admin.ID,
			PasswordHash: &hash,
		})
	}
	return nil
}