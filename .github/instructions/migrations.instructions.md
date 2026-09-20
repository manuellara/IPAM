---
applyTo: "migrations/**"
---

# Migration conventions

- One pair per change: `NNNNNN_description.up.sql` / `NNNNNN_description.down.sql`
  (golang-migrate numeric-prefix convention). Sequence numbers increment from
  whatever already exists in `migrations/` — check the directory before
  picking the next number.
- The down migration must drop things in **reverse** dependency order from
  the up migration.
- SQLite does not enforce foreign keys by default and this cannot be set in
  a migration — `PRAGMA foreign_keys = ON;` belongs in the Go DB connection
  setup, not here. Don't add it to a migration file. The same is true for
  `PRAGMA journal_mode = WAL`, `PRAGMA busy_timeout`, and the connection
  pool size cap — see the repo-wide instructions' "DB connection settings"
  bullet. All of these are set via DSN query parameters
  (`?_foreign_keys=on&_journal_mode=WAL&_busy_timeout=5000`), not a
  post-open `PRAGMA` exec.
- SQLite has no cross-row `CHECK` constraint. Any validation that depends on
  another table's data (e.g. "this code's length must match its scheme's
  configured token_length", or "app_code/role_code are required only when
  the referenced naming scheme's naming_mode is 'generated'") needs a
  `BEFORE INSERT`/`BEFORE UPDATE` trigger with `RAISE(ABORT, ...)`,
  following the pattern already used for `naming_scheme_token_values` and
  `requests` (the `naming_mode` check).
- For "soft delete" / reusable-after-release semantics (e.g. an IP that can
  be reallocated after a decommission, or at most one open decommission
  request at a time), use a **partial unique index**
  (`WHERE released_at IS NULL` / `WHERE status = 'pending'`), not a plain
  `UNIQUE` constraint — a plain constraint would permanently block reuse
  once a row exists. See `ip_allocations_active_unique` and
  `decommission_requests_open_unique` in the initial migration for the
  established pattern.
- Timestamps are stored as `TEXT` via `datetime('now')` — stay consistent
  with this rather than introducing a different time representation.
- Booleans are `INTEGER` (`0`/`1`) — SQLite has no native boolean type.
- The local admin user (`id = 1`, `administrator`) and its `admin` role
  assignment are seeded directly in the initial migration, not created at
  runtime — this makes the whole thing atomic within the migration's
  transaction. `password_hash` starts `NULL` in the seed and is set on
  first boot by `EnsureLocalAdmin` from `ADMIN_PASSWORD`.
