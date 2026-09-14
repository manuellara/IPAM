-- name: ListRoleNamesByUserID :many
SELECT roles.name
FROM roles
JOIN user_roles ON user_roles.role_id = roles.id
WHERE user_roles.user_id = ?
ORDER BY roles.name ASC;
