-- name: CreateRequest :one
INSERT INTO requests (requester_id, naming_scheme_id, site_code, env_code, app_code, role_code, manual_name, status)
VALUES (sqlc.arg(requester_id), sqlc.arg(naming_scheme_id), sqlc.arg(site_code), sqlc.arg(env_code), sqlc.arg(app_code), sqlc.arg(role_code), sqlc.arg(manual_name), 'pending')
RETURNING *;