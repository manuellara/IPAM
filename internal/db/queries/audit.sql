-- name: CreateAuditLog :exec
INSERT INTO audit_log (actor_user_id, action, target_type, target_id, detail)
VALUES (
    sqlc.arg(actor_user_id),
    sqlc.arg(action),
    sqlc.arg(target_type),
    sqlc.arg(target_id),
    sqlc.arg(detail)
);
