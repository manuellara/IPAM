-- name: GetOIDCConfig :one
SELECT * FROM oidc_config WHERE id = 1;

-- name: GetLDAPConfig :one
SELECT * FROM ldap_config WHERE id = 1;

-- name: UpdateOIDCConfig :exec
UPDATE oidc_config
SET enabled = sqlc.arg(enabled), issuer_url = sqlc.arg(issuer_url), client_id = sqlc.arg(client_id),
	client_secret = COALESCE(NULLIF(sqlc.arg(client_secret), ''), client_secret),
	redirect_url = sqlc.arg(redirect_url)
WHERE id = 1;

-- name: UpdateLDAPConfig :exec
UPDATE ldap_config
SET enabled = sqlc.arg(enabled), server = sqlc.arg(server), port = sqlc.arg(port),
	base_dn = sqlc.arg(base_dn), bind_dn = sqlc.arg(bind_dn),
	bind_password = COALESCE(NULLIF(sqlc.arg(bind_password), ''), bind_password),
	user_filter = sqlc.arg(user_filter)
WHERE id = 1;
