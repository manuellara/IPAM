----------------------------------------------------------------------
-- DROP: AUDIT LOG
----------------------------------------------------------------------
DROP INDEX IF EXISTS audit_log_created_idx;
DROP INDEX IF EXISTS audit_log_actor_idx;
DROP TABLE IF EXISTS audit_log;

----------------------------------------------------------------------
-- DROP: DECOMMISSION REQUESTS
----------------------------------------------------------------------
DROP INDEX IF EXISTS decommission_requests_open_unique;
DROP TABLE IF EXISTS decommission_requests;

----------------------------------------------------------------------
-- DROP: IP ALLOCATIONS
----------------------------------------------------------------------
DROP INDEX IF EXISTS ip_allocations_request_idx;
DROP INDEX IF EXISTS ip_allocations_subnet_idx;
DROP INDEX IF EXISTS ip_allocations_active_unique;
DROP TABLE IF EXISTS ip_allocations;

----------------------------------------------------------------------
-- DROP: REQUESTS
----------------------------------------------------------------------
DROP INDEX IF EXISTS requests_status_idx;
DROP INDEX IF EXISTS requests_requester_idx;
DROP TABLE IF EXISTS requests;

----------------------------------------------------------------------
-- DROP: NAMING SCHEMES, TOKEN VALUES, SEQUENCES
----------------------------------------------------------------------
DROP TRIGGER IF EXISTS trg_token_value_length_update;
DROP TRIGGER IF EXISTS trg_token_value_length_insert;
DROP TABLE IF EXISTS naming_sequences;
DROP TABLE IF EXISTS naming_scheme_token_values;
DROP TABLE IF EXISTS naming_schemes;

----------------------------------------------------------------------
-- DROP: SITE+ENV -> SUBNET MAPPING
----------------------------------------------------------------------
DROP TABLE IF EXISTS site_env_subnet_map;

----------------------------------------------------------------------
-- DROP: SUBNETS + RESERVED IPS
----------------------------------------------------------------------
DROP TABLE IF EXISTS subnet_reserved_ips;
DROP TABLE IF EXISTS subnets;

----------------------------------------------------------------------
-- DROP: USERS, ROLES, USER_ROLES, SESSIONS
----------------------------------------------------------------------
DROP INDEX IF EXISTS sessions_expiry_idx;
DROP TABLE IF EXISTS sessions;
DROP TABLE IF EXISTS user_roles;
DROP TABLE IF EXISTS roles;
DROP TABLE IF EXISTS users;
