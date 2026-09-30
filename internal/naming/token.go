package naming

import "fmt"

// ValidateTokenCode checks that code is exactly length characters long.
// This mirrors the DB-level enforcement in trg_token_value_length_insert/
// _update (migrations/000001_init.up.sql) as a fast, friendly pre-check --
// it lets the admin form (POST /admin/naming-schemes/{id}/tokens) show a
// proper inline validation error instead of a raw SQL constraint failure.
// The trigger is the last-resort guarantee; this is not a replacement for
// it, just a better first line of defense.
func ValidateTokenCode(code string, length int) error {
	if len(code) != length {
		return fmt.Errorf("code must be exactly %d characters (got %d)", length, len(code))
	}
	return nil
}
