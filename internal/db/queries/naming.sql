-- name: ListNamingSchemes :many
SELECT id, name, naming_mode, template FROM naming_schemes ORDER BY id;

-- name: GetNamingScheme :one
SELECT id, name, naming_mode, template, token_length, seq_length, total_length
FROM naming_schemes WHERE id = sqlc.arg(id);

-- name: ListNamingSchemeTokenValues :many
-- ORDER BY token, code -- no lexicographic-sort trap here (unlike
-- ip_address/reserved-ips): code is a fixed-length, non-numeric 3-char
-- abbreviation, so plain string ordering is correct, not just convenient.
SELECT id, token, code, label, active
FROM naming_scheme_token_values
WHERE scheme_id = sqlc.arg(scheme_id)
ORDER BY token, code;

-- name: CreateNamingSchemeTokenValue :one
-- UPPER(?) on code -- see queries.instructions.md's
-- "Reference: naming scheme queries (IPAM-16/17)" section.
INSERT INTO naming_scheme_token_values (scheme_id, token, code, label)
VALUES (sqlc.arg(scheme_id), sqlc.arg(token), UPPER(sqlc.arg(code)), sqlc.arg(label))
RETURNING *;

-- name: DeactivateNamingSchemeTokenValue :exec
-- Deactivate, never delete -- a request can still reference an inactive
-- code's historical value. Scoped by scheme_id too, same
-- belt-and-suspenders pattern as DeleteSubnetReservedIP.
UPDATE naming_scheme_token_values
SET active = 0
WHERE id = sqlc.arg(id) AND scheme_id = sqlc.arg(scheme_id);

-- name: ListActiveSiteEnvTokenValues :many
-- Load active site and env tokens for client-side filtering by scheme and token.
SELECT scheme_id, token, code, label
FROM naming_scheme_token_values
WHERE active = 1 AND token IN ('site', 'env')
ORDER BY scheme_id, token, code;

-- name: GetSiteEnvSubnetMap :one
SELECT * FROM site_env_subnet_map WHERE id = sqlc.arg(id);

-- name: GetActiveSiteEnvSubnetMap :one
-- Request submission requires an active mapping for this scheme/site/env.
SELECT subnet_id FROM site_env_subnet_map
WHERE site_code = sqlc.arg(site_code)
    AND env_code = sqlc.arg(env_code)
    AND naming_scheme_id = sqlc.arg(naming_scheme_id)
    AND active = 1;

-- name: CreateSiteEnvSubnetMap :one
INSERT INTO site_env_subnet_map (site_code, env_code, naming_scheme_id, subnet_id, active)
VALUES (sqlc.arg(site_code), sqlc.arg(env_code), sqlc.arg(naming_scheme_id), sqlc.arg(subnet_id), 1)
RETURNING *;

-- name: UpdateSiteEnvSubnetMap :exec
UPDATE site_env_subnet_map
SET site_code = sqlc.arg(site_code), env_code = sqlc.arg(env_code),
    naming_scheme_id = sqlc.arg(naming_scheme_id), subnet_id = sqlc.arg(subnet_id),
    active = sqlc.arg(active)
WHERE id = sqlc.arg(id);

-- name: ListSiteEnvSubnetMapWithDetails :many
-- Include scheme and subnet details for the mapping list page.
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