-- name: GetOIDCConfig :one
SELECT * FROM oidc_config WHERE id = 1;

-- name: GetLDAPConfig :one
SELECT * FROM ldap_config WHERE id = 1;
