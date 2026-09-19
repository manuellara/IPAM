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
