package auth

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/manuellara/ipam/internal/db"
)

// ErrLoginLocked is returned by CheckLoginLockout when the identifier is
// currently locked out, wrapping how much longer the lockout lasts.
type ErrLoginLocked struct {
	RetryAfter time.Duration
}

func (e *ErrLoginLocked) Error() string {
	return "account temporarily locked due to repeated failed login attempts"
}

// CheckLoginLockout returns ErrLoginLocked if identifier/authMethod is
// currently locked out. Call this BEFORE attempting the actual
// password/bind check, so a locked-out attempt never does the expensive
// (argon2/LDAP bind) work.
//
// locked_until is stored as RFC3339 (not SQLite's datetime('now') style,
// unlike every other timestamp column in this schema) because it's a
// value computed and formatted entirely in Go (time.Now().Add(backoff)),
// not something SQLite generates -- RFC3339 round-trips cleanly with
// time.Parse/time.Format without hand-matching SQLite's string format.
func CheckLoginLockout(ctx context.Context, q *db.Queries, identifier, authMethod string) error {
	attempt, err := q.GetLoginAttempt(ctx, db.GetLoginAttemptParams{
		Identifier: identifier,
		AuthMethod: authMethod,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if attempt.LockedUntil == nil {
		return nil
	}

	lockedUntil, err := time.Parse(time.RFC3339, *attempt.LockedUntil)
	if err != nil {
		return err
	}
	if remaining := time.Until(lockedUntil); remaining > 0 {
		return &ErrLoginLocked{RetryAfter: remaining}
	}
	return nil
}

// RecordLoginFailure increments the failure count for identifier/authMethod
// and computes the next lockout window via exponential backoff.
func RecordLoginFailure(ctx context.Context, q *db.Queries, identifier, authMethod string) error {
	attempt, err := q.GetLoginAttempt(ctx, db.GetLoginAttemptParams{
		Identifier: identifier,
		AuthMethod: authMethod,
	})
	failureCount := int64(1)
	if err == nil {
		failureCount = attempt.FailureCount + 1
	} else if !errors.Is(err, sql.ErrNoRows) {
		return err
	}

	var lockedUntil *string
	if d := loginBackoffDuration(failureCount); d > 0 {
		s := time.Now().Add(d).Format(time.RFC3339)
		lockedUntil = &s
	}

	return q.RecordLoginFailure(ctx, db.RecordLoginFailureParams{
		Identifier:   identifier,
		AuthMethod:   authMethod,
		FailureCount: failureCount,
		LockedUntil:  lockedUntil,
	})
}

// ResetLoginAttempts clears any failure history for identifier/authMethod,
// called after a successful login.
func ResetLoginAttempts(ctx context.Context, q *db.Queries, identifier, authMethod string) error {
	return q.ResetLoginAttempts(ctx, db.ResetLoginAttemptsParams{
		Identifier: identifier,
		AuthMethod: authMethod,
	})
}

// loginBackoffDuration returns the lockout duration for a given failure
// count: no penalty for the first 2 failures, then doubling from 5s,
// capped at 5 minutes.
func loginBackoffDuration(failureCount int64) time.Duration {
	if failureCount < 3 {
		return 0
	}
	const base = 5 * time.Second
	const max = 5 * time.Minute
	exp := failureCount - 3
	d := base * time.Duration(int64(1)<<uint(exp))
	if d > max {
		return max
	}
	return d
}