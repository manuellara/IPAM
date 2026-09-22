-- name: GetLocalAdminUser :one
SELECT * FROM users WHERE auth_source = 'local' LIMIT 1;

-- name: CreateLocalAdminUser :one
INSERT INTO users (display_name, auth_source, password_hash, active)
VALUES ('administrator', 'local', sqlc.arg(password_hash), 1)
RETURNING *;

-- name: UpdateUserPasswordHash :exec
UPDATE users
SET password_hash = sqlc.arg(password_hash)
WHERE id = sqlc.arg(id);

-- name: GetUserRoleNames :many
SELECT r.name FROM roles r
JOIN user_roles ur ON ur.role_id = r.id
WHERE ur.user_id = sqlc.arg(user_id);

-- name: GetOIDCUser :one
SELECT * FROM users
WHERE auth_source = 'oidc' AND oidc_subject = sqlc.arg(oidc_subject)
LIMIT 1;

-- name: CreateOIDCUser :one
INSERT INTO users (display_name, email, auth_source, oidc_subject, active)
VALUES (sqlc.arg(display_name), sqlc.arg(email), 'oidc', sqlc.arg(oidc_subject), 1)
RETURNING *;

-- name: GetLDAPUser :one
SELECT * FROM users
WHERE auth_source = 'ldap' AND ldap_dn = sqlc.arg(ldap_dn)
LIMIT 1;

-- name: CreateLDAPUser :one
INSERT INTO users (display_name, email, auth_source, ldap_dn, active)
VALUES (sqlc.arg(display_name), sqlc.arg(email), 'ldap', sqlc.arg(ldap_dn), 1)
RETURNING *;

-- name: AssignViewerRole :exec
INSERT OR IGNORE INTO user_roles (user_id, role_id)
SELECT sqlc.arg(user_id), id FROM roles WHERE name = 'viewer';

-- name: GetLoginAttempt :one
SELECT * FROM login_attempts
WHERE identifier = sqlc.arg(identifier) AND auth_method = sqlc.arg(auth_method)
LIMIT 1;

-- name: RecordLoginFailure :exec
INSERT INTO login_attempts (identifier, auth_method, failure_count, locked_until, last_attempt_at)
VALUES (sqlc.arg(identifier), sqlc.arg(auth_method), sqlc.arg(failure_count), sqlc.arg(locked_until), datetime('now'))
ON CONFLICT (identifier, auth_method) DO UPDATE SET
	failure_count = excluded.failure_count,
	locked_until = excluded.locked_until,
	last_attempt_at = datetime('now');

-- name: ResetLoginAttempts :exec
DELETE FROM login_attempts
WHERE identifier = sqlc.arg(identifier) AND auth_method = sqlc.arg(auth_method);
