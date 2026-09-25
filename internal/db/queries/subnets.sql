-- name: ListSubnetsWithCounts :many
-- Three LEFT JOINs (reserved IPs, active allocations, active site+env
-- mappings) produce a cross product of matching rows per subnet. Each
-- aggregate below is DISTINCT on its own join's identifying value, so
-- extra rows introduced by the OTHER joins never inflate a given
-- aggregate. Do not change these to plain COUNT(*)/GROUP_CONCAT(*) --
-- the numbers will look plausible but be wrong.
--
-- site_envs is wrapped in CAST(... AS TEXT): sqlc's type inferencer
-- can't determine a concrete type for COALESCE(GROUP_CONCAT(...), '')
-- on its own and falls back to interface{} (loses type safety). The
-- explicit CAST forces a clean string inference instead. COALESCE
-- handles the NULL GROUP_CONCAT returns when a subnet has zero matching
-- mapping rows. GROUP_CONCAT(DISTINCT ...) can only take one argument in
-- SQLite -- a custom separator is NOT allowed alongside DISTINCT, so
-- this comes back comma-separated with no space; formatted for display
-- (", ") in Go instead, see displaySiteEnvs in the admin view.
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

-- name: ListActiveSubnets :many
-- Used for CIDR overlap validation on create/edit -- only ACTIVE subnets
-- are checked, per subnets.active semantics: a retired range can be
-- legitimately reused by a new subnet without being falsely blocked by
-- its own retired history.
SELECT id, cidr FROM subnets WHERE active = 1;

-- name: GetSubnet :one
SELECT * FROM subnets WHERE id = ?;

-- name: CreateSubnet :one
INSERT INTO subnets (cidr, label, active)
VALUES (?, ?, 1)
RETURNING *;

-- name: UpdateSubnet :exec
UPDATE subnets SET cidr = ?, label = ?, active = ? WHERE id = ?;

-- name: CountActiveAllocationsForSubnet :one
-- Used for the deactivate warning: how many currently-active allocations
-- would be "orphaned" (still valid, but on a subnet no longer accepting
-- new ones) if this subnet is soft-retired. Informational only --
-- does not block deactivation.
SELECT COUNT(*) FROM ip_allocations WHERE subnet_id = ? AND released_at IS NULL;