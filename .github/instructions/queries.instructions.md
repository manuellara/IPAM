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

## Reference: site_env_subnet_map queries (Cycle 3)

`ListSiteEnvSubnetMapWithDetails` follows the exact same gotcha #1 as
`ListSubnetsWithCounts` above (two `LEFT JOIN`s → `COUNT(DISTINCT ...)`
per joined table), since it computes each mapping's subnet
utilization the same way:

```sql
-- name: ListSiteEnvSubnetMapWithDetails :many
SELECT
    m.id,
    m.site_code,
    m.env_code,
    m.active,
    ns.id   AS scheme_id,
    ns.name AS scheme_name,
    s.id    AS subnet_id,
    s.cidr  AS subnet_cidr,
    s.label AS subnet_label,
    COUNT(DISTINCT sr.id) AS reserved_count,
    COUNT(DISTINCT ia.id) AS used_count
FROM site_env_subnet_map m
JOIN naming_schemes ns ON ns.id = m.naming_scheme_id
JOIN subnets s ON s.id = m.subnet_id
LEFT JOIN subnet_reserved_ips sr ON sr.subnet_id = s.id
LEFT JOIN ip_allocations ia ON ia.subnet_id = s.id AND ia.released_at IS NULL
GROUP BY m.id
ORDER BY ns.name, m.site_code, m.env_code;

-- name: ListActiveSiteEnvTokenValues :many
-- All active site+env token values across every scheme, in one shot --
-- meant to be embedded as JSON on the mapping form and filtered
-- client-side (Alpine) by scheme_id + token as the scheme dropdown
-- changes, not re-fetched per selection.
SELECT scheme_id, token, code, label
FROM naming_scheme_token_values
WHERE active = 1 AND token IN ('site', 'env')
ORDER BY scheme_id, token, code;
```

Two mappings that point at the same `subnet_id` will report the same
`reserved_count`/`used_count`/free-address total — that's correct, not
a bug: the free-address pool belongs to the subnet, not to any one
mapping, so mappings sharing a subnet legitimately share its capacity.

## Reference: IP allocation queries (Cycle 3)

Backing `internal/allocation.Allocate` — see
`docs/workflows.md`'s "IP Allocation Logic" section for the full
design (why these run inside an already-open transaction, the
`_txlock=immediate` DSN change, the two distinct error cases).

```sql
-- name: ListReservedIPsForSubnet :many
SELECT ip_address FROM subnet_reserved_ips WHERE subnet_id = ?;

-- name: ListActiveAllocatedIPsForSubnet :many
SELECT ip_address FROM ip_allocations WHERE subnet_id = ? AND released_at IS NULL;

-- name: CreateIPAllocation :one
INSERT INTO ip_allocations (subnet_id, ip_address, request_id, server_id)
VALUES (?, ?, ?, ?)
RETURNING *;
```

No aggregate gotchas here — single-table queries, no joins.

## Reference: reserved-IP admin queries (IPAM-39)

Backing `/admin/subnets/{id}/reserved-ips`. Distinct from
`ListReservedIPsForSubnet` above (allocation-time check, `ip_address`
only) — these carry `id`/`reason`/`created_at` for the admin list/delete
UI.

```sql
-- name: ListSubnetReservedIPsForAdmin :many
-- ORDER BY id, not created_at. A range reservation inserts every row in
-- one transaction, and SQLite's datetime('now') only has second
-- resolution -- rows from the same range submission tie on created_at,
-- and ties fall back to whatever order the (subnet_id, ip_address)
-- unique index happens to return them in, which is a LEXICOGRAPHIC
-- string sort on ip_address, not numeric (e.g. "10.1.11.10" sorts
-- before "10.1.11.5"). id is the autoincrement PK and reflects actual
-- insertion order, which is address order (ExpandReserveRange returns
-- addresses ascending, inserted in that order) -- don't change this
-- back to created_at.
SELECT id, ip_address, reason, created_at
FROM subnet_reserved_ips
WHERE subnet_id = ?
ORDER BY id;

-- name: CreateSubnetReservedIP :one
INSERT INTO subnet_reserved_ips (subnet_id, ip_address, reason)
VALUES (?, ?, ?)
RETURNING *;

-- name: DeleteSubnetReservedIP :exec
-- Scoped by subnet_id too, not just id -- belt-and-suspenders against a
-- crafted delete for a row under a different subnet.
DELETE FROM subnet_reserved_ips WHERE id = ? AND subnet_id = ?;
```

## Reference: allocation export queries (IPAM-41)

Backing `GET /admin/allocations/export.csv`. Two separate named queries
(system-wide vs. `?subnet_id=`-scoped), not one query with a dynamic/
optional `WHERE` -- matches the existing pattern of
`CountActiveAllocationsForSubnet` staying separate from
`ListSubnetsWithCounts` rather than parameterizing one query for both
shapes.

```sql
-- name: ListActiveAllocationsForExport :many
-- ORDER BY sub.cidr, ia.id -- NOT ia.ip_address. Same lexicographic-
-- string-sort trap as ListSubnetReservedIPsForAdmin (IPAM-39):
-- ip_address is TEXT, so sorting by it directly gives wrong ordering
-- ("10.1.11.10" before "10.1.11.5"). id reflects allocation order.
SELECT
    sub.cidr AS subnet_cidr,
    ia.ip_address,
    s.hostname,
    s.source,
    u.display_name AS requester,
    ia.allocated_at
FROM ip_allocations ia
JOIN subnets sub ON sub.id = ia.subnet_id
LEFT JOIN servers s ON s.id = ia.server_id
LEFT JOIN requests r ON r.id = ia.request_id
LEFT JOIN users u ON u.id = r.requester_id
WHERE ia.released_at IS NULL
ORDER BY sub.cidr, ia.id;

-- name: ListActiveAllocationsForSubnetExport :many
SELECT
    sub.cidr AS subnet_cidr,
    ia.ip_address,
    s.hostname,
    s.source,
    u.display_name AS requester,
    ia.allocated_at
FROM ip_allocations ia
JOIN subnets sub ON sub.id = ia.subnet_id
LEFT JOIN servers s ON s.id = ia.server_id
LEFT JOIN requests r ON r.id = ia.request_id
LEFT JOIN users u ON u.id = r.requester_id
WHERE ia.released_at IS NULL AND ia.subnet_id = sqlc.arg(subnet_id)
ORDER BY ia.id;
```

`hostname`, `source`, and `requester` are all nullable (`*string`) --
`servers`/`requests`/`users` are `LEFT JOIN`ed because admin-direct and
CSV-import allocations have no `request_id`, and even request-sourced
ones may have no matching `servers` row yet in edge cases. Dereference
with the usual fallback-to-`""` pattern at the CSV-writing boundary, not
in the query.

## Reference: naming scheme queries (IPAM-16/17)

Backing the not-yet-built `/admin/naming-schemes/{id}/tokens` admin UI
(IPAM-18) and the sequence-generation logic (IPAM-17, still being
designed). So far only the token-value insert is settled:

```sql
-- name: CreateNamingSchemeTokenValue :one
-- UPPER(?) on code, not a rejection -- site/env/app/role codes are
-- silently normalized to uppercase regardless of what the admin typed,
-- same convention as site_code/env_code elsewhere in the schema. This
-- runs before trg_token_value_length_insert evaluates NEW.code, so the
-- trigger's exact-length check sees the already-uppercased value --
-- order of operations works out fine in SQLite (UPPER() doesn't change
-- length for the ASCII codes this table stores). Don't duplicate an
-- uppercase check in Go (internal/naming.ValidateTokenCode) -- that
-- function is length-only by design; casing is the query's job.
INSERT INTO naming_scheme_token_values (scheme_id, token, code, label)
VALUES (?, ?, UPPER(?), ?)
RETURNING *;
```

If an `UpdateNamingSchemeTokenValue` query is added later (editing a
code in place, not just toggling `active`), wrap its `code` argument in
`UPPER(?)` too, for the same reason.

IPAM-17's sequence-generation queries (`naming_sequences` lock/read/
increment, run inside the approval transaction) aren't scoped yet --
this section will grow once that design is settled.
