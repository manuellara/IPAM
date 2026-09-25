---
applyTo: "internal/db/queries/**"
---

# sqlc query conventions

(This scope is hand-written query files only. Generated output in
`internal/db/*.go` is covered by `db-generated.instructions.md`. Migration
schema files are covered by `migrations.instructions.md`.)

- Split by domain, one file per area: `users.sql`, `subnets.sql`,
  `requests.sql`, `naming.sql`, `decommissions.sql`, `audit.sql`,
  `servers.sql`, `maintenance.sql`, `api_keys.sql`, etc. sqlc compiles
  every file in this directory into a single generated package regardless
  of the split — this is purely for readability, so don't consolidate back
  into one file.
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
- Nullable columns generate as pointers (`*string`, `*int64`). Views
  that need a plain `string`/`bool` dereference-with-fallback at the
  controller boundary (`label := ""; if row.Label != nil { label =
  *row.Label }`) — don't push pointer types down into templ
  view-model structs.

## Aggregate-query gotchas (SQLite + sqlc)

Hit repeatedly while building `ListSubnetsWithCounts` (`subnets.sql`) —
check for these whenever a query joins more than one table and
aggregates:

1. **Multiple `LEFT JOIN`s + `COUNT`**: joining more than one table
   fans out rows before aggregation, so a bare `COUNT(*)` or
   `COUNT(x.id)` overcounts. Always `COUNT(DISTINCT joined_table.id)`,
   per joined table.
2. **`GROUP_CONCAT` returns SQL `NULL`**, not `''`, when a group has
   zero matching rows. sqlc will generate a non-nullable `string`
   field for it, and the scan panics at runtime on any such row
   (`converting NULL to string is unsupported`). Wrap it:
   `COALESCE(GROUP_CONCAT(col), '')`.
3. **`COALESCE(GROUP_CONCAT(...), '')` alone can still infer as
   `interface{}`** in sqlc's type inferencer — too complex an
   expression for it to pin down a type. Force it with an explicit
   cast around the whole expression: `CAST(COALESCE(GROUP_CONCAT(col),
   '') AS TEXT)`.
4. **`GROUP_CONCAT(DISTINCT col, sep)` is illegal in SQLite** — a
   DISTINCT aggregate takes exactly one argument, so a custom
   separator can't be combined with DISTINCT. Use the default
   separator (`,`) in SQL and reformat for display in Go if a nicer
   one is needed (e.g. `strings.ReplaceAll(raw, ",", ", ")`), rather
   than fighting SQL for it.

Reference (`subnets.sql`):

```sql
-- name: ListSubnetsWithCounts :many
SELECT
    s.id,
    s.cidr,
    s.label,
    s.active,
    COUNT(DISTINCT sr.id) AS reserved_count,
    COUNT(DISTINCT ia.id) AS used_count,
    CAST(COALESCE(GROUP_CONCAT(DISTINCT sem.site_code || '/' || sem.env_code), '') AS TEXT) AS site_envs
FROM subnets s
LEFT JOIN subnet_reserved_ips sr ON sr.subnet_id = s.id
LEFT JOIN ip_allocations ia ON ia.subnet_id = s.id AND ia.released_at IS NULL
LEFT JOIN site_env_subnet_map sem ON sem.subnet_id = s.id AND sem.active = 1
GROUP BY s.id
ORDER BY s.cidr;

-- name: CountActiveAllocationsForSubnet :one
-- Powers the "N active allocations" deactivate-warning on the edit
-- form. Don't reuse ListSubnetsWithCounts's used_count for this — it's
-- shaped for the list page, not a single-subnet check.
SELECT COUNT(*) FROM ip_allocations WHERE subnet_id = ? AND released_at IS NULL;
```
