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
  bullet.
- SQLite has no cross-row `CHECK` constraint. Any validation that depends on
  another table's data (e.g. "this code's length must match its scheme's
  configured token_length") needs a `BEFORE INSERT`/`BEFORE UPDATE` trigger
  with `RAISE(ABORT, ...)`, following the pattern already used for
  `naming_scheme_token_values`.
- For "soft delete" / reusable-after-release semantics (e.g. an IP that can
  be reallocated after a decommission), use a **partial unique index**
  (`WHERE released_at IS NULL` / `WHERE status = 'pending'`), not a plain
  `UNIQUE` constraint — a plain constraint would permanently block reuse
  once a row exists. See `ip_allocations_active_unique` and
  `decommission_requests_open_unique` in the initial migration for the
  established pattern.
- Timestamps are stored as `TEXT` via `datetime('now')` — stay consistent
  with this rather than introducing a different time representation.
- Booleans are `INTEGER` (`0`/`1`) — SQLite has no native boolean type.
