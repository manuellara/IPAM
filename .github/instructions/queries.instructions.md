---
applyTo: "internal/db/queries/**"
---

# sqlc query conventions

(This scope is hand-written query files only. Generated output in
`internal/db/*.go` is covered by `db-generated.instructions.md`. Migration
schema files are covered by `migrations.instructions.md`.)

- Split by domain, one file per area: `users.sql`, `subnets.sql`,
  `requests.sql`, `naming.sql`, `decommissions.sql`, `audit.sql`, etc.
  sqlc compiles every file in this directory into a single generated
  package regardless of the split — this is purely for readability, so
  don't consolidate back into one file.
- Target the `sqlite` engine (set in `sqlc.yaml`) — don't write
  PostgreSQL-only syntax (e.g. `RETURNING` works in modern SQLite, but
  avoid Postgres-only functions or `::type` casts).
- Any query touching `ip_allocations` for "is this IP free" must respect
  `released_at IS NULL` — a released row is historical, not a currently-held
  allocation. Never write a query that treats a released row as active.
- Any query touching `naming_sequences` for allocation must be written to
  run inside a transaction with row locking in mind (`BEGIN IMMEDIATE` at
  the Go call site) — the query itself should select-for-update-equivalent
  the single row by `scheme_id + computed_prefix` before the increment.
- After adding or changing a query here, run `sqlc generate` — don't
  hand-edit anything under `internal/db/*.go`.
