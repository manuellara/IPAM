-- name: ListNamingSchemes :many
SELECT id, name, naming_mode FROM naming_schemes ORDER BY name;

-- name: CreateNamingSchemeTokenValue :one
INSERT INTO naming_scheme_token_values (scheme_id, token, code, label)
VALUES (sqlc.arg(scheme_id), sqlc.arg(token), UPPER(sqlc.arg(code)), sqlc.arg(label))
RETURNING *;

-- name: ListActiveSiteEnvTokenValues :many
-- Load active site and env tokens for client-side filtering by scheme and token.
SELECT scheme_id, token, code, label
FROM naming_scheme_token_values
WHERE active = 1 AND token IN ('site', 'env')
ORDER BY scheme_id, token, code;

-- name: GetSiteEnvSubnetMap :one
SELECT * FROM site_env_subnet_map WHERE id = sqlc.arg(id);

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