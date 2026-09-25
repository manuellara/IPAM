----------------------------------------------------------------------
-- USERS, ROLES, USER_ROLES, SESSIONS
----------------------------------------------------------------------
CREATE TABLE users (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    display_name  TEXT NOT NULL,
    email         TEXT,
    auth_source   TEXT NOT NULL CHECK (auth_source IN ('local','oidc','ldap')),
    oidc_subject  TEXT UNIQUE,           -- OIDC "sub" claim, NULL for local/ldap users
    ldap_dn       TEXT UNIQUE,           -- LDAP entry DN, NULL for local/oidc users
    password_hash TEXT,                  -- argon2id encoded hash, local admin only
    active        INTEGER NOT NULL DEFAULT 1,
    created_at    TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE roles (
    id   INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL UNIQUE
);

CREATE TABLE user_roles (
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role_id INTEGER NOT NULL REFERENCES roles(id) ON DELETE CASCADE,
    PRIMARY KEY (user_id, role_id)
);

INSERT INTO roles (name) VALUES ('admin'), ('approver'), ('requester'), ('viewer');

-- Seed the local admin user. id=1 is deterministic here since this is the
-- first row ever inserted into users. password_hash starts NULL and is
-- set on first boot by EnsureLocalAdmin from ADMIN_PASSWORD -- everything
-- else (the user row, the admin role assignment) is seeded here instead
-- of at runtime, so there's no window where the user exists without its
-- role (this whole migration runs in one transaction).
INSERT INTO users (id, display_name, auth_source, password_hash, active)
VALUES (1, 'administrator', 'local', NULL, 1);

INSERT INTO user_roles (user_id, role_id)
SELECT 1, id FROM roles WHERE name = 'admin';

-- scs sessions table (sqlite3store expected schema)
CREATE TABLE sessions (
    token  TEXT PRIMARY KEY,
    data   BLOB NOT NULL,
    expiry REAL NOT NULL
);

CREATE INDEX sessions_expiry_idx ON sessions(expiry);

-- Login rate limiting, tracked per (identifier, auth_method) rather than
-- per source IP -- avoids locking out a whole office behind one NAT
-- gateway, at the cost of not blocking distributed username-enumeration
-- attacks. identifier is "administrator" (fixed) for local admin, or the
-- submitted username for LDAP. OIDC is exempt -- IPAM never sees a
-- password for that flow, the IdP owns its own lockout policy.
-- Exponential backoff, not a flat lockout: harder to weaponize (a flat
-- N-failures-then-locked-for-M-minutes policy lets an attacker
-- deliberately lock out the real admin by repeatedly failing their
-- login on purpose).
CREATE TABLE login_attempts (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    identifier      TEXT NOT NULL,
    auth_method     TEXT NOT NULL CHECK (auth_method IN ('local','ldap')),
    failure_count   INTEGER NOT NULL DEFAULT 0,
    locked_until    TEXT,   -- NULL if not currently locked
    last_attempt_at TEXT NOT NULL DEFAULT (datetime('now')),
    UNIQUE (identifier, auth_method)
);

----------------------------------------------------------------------
-- SUBNETS + RESERVED IPS
----------------------------------------------------------------------
CREATE TABLE subnets (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    cidr       TEXT NOT NULL UNIQUE,     -- e.g. "10.20.4.0/24", validated + overlap-checked in Go via net/netip
    label      TEXT,
    active     INTEGER NOT NULL DEFAULT 1,  -- soft-retire: 0 blocks NEW allocations and is excluded from
                                             -- overlap validation (so a retired range can be legitimately
                                             -- reused by a new subnet); existing ip_allocations rows against
                                             -- an inactive subnet remain valid and are NOT affected
    created_at TEXT NOT NULL DEFAULT (datetime('now'))
);

-- Reserved/excluded IPs skipped during auto-assignment (gateway, pre-existing infra, etc.)
CREATE TABLE subnet_reserved_ips (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    subnet_id  INTEGER NOT NULL REFERENCES subnets(id) ON DELETE CASCADE,
    ip_address TEXT NOT NULL,
    reason     TEXT,
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    UNIQUE (subnet_id, ip_address)
);

----------------------------------------------------------------------
-- NAMING SCHEMES, TOKEN VALUES, SEQUENCES
----------------------------------------------------------------------
-- naming_mode distinguishes schemes whose name is computed from tokens
-- ('generated' -- Server, Network Device, VM) from schemes whose name is
-- provided directly by the requester ('manual' -- e.g. F5 Virtual Server,
-- where the "name" is an F5 VIP name, not a NetBIOS-constrained hostname).
CREATE TABLE naming_schemes (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    name         TEXT NOT NULL UNIQUE,       -- "Server", "Network Device", "VM", "F5 Virtual Server"
    template     TEXT,                       -- "{site}{env}{app}{role}{seq}" for generated schemes; NULL for manual
    naming_mode  TEXT NOT NULL DEFAULT 'generated' CHECK (naming_mode IN ('generated','manual')),
    token_length INTEGER NOT NULL DEFAULT 3, -- exact length required per token (not max); unused for manual schemes
    seq_length   INTEGER NOT NULL DEFAULT 3,
    total_length INTEGER NOT NULL DEFAULT 15 -- matches Windows NetBIOS limit; unused for manual schemes
);

CREATE TABLE naming_scheme_token_values (
    id        INTEGER PRIMARY KEY AUTOINCREMENT,
    scheme_id INTEGER NOT NULL REFERENCES naming_schemes(id) ON DELETE CASCADE,
    token     TEXT NOT NULL CHECK (token IN ('site','env','app','role')),
    code      TEXT NOT NULL,
    label     TEXT NOT NULL,
    active    INTEGER NOT NULL DEFAULT 1,
    UNIQUE (scheme_id, token, code)
);

-- Enforce EXACT code length (not max) against the owning scheme's token_length.
-- SQLite has no cross-row CHECK, so this is done with triggers instead.
CREATE TRIGGER trg_token_value_length_insert
BEFORE INSERT ON naming_scheme_token_values
FOR EACH ROW
WHEN (SELECT token_length FROM naming_schemes WHERE id = NEW.scheme_id) != length(NEW.code)
BEGIN
    SELECT RAISE(ABORT, 'token code length must exactly match the scheme token_length');
END;

CREATE TRIGGER trg_token_value_length_update
BEFORE UPDATE ON naming_scheme_token_values
FOR EACH ROW
WHEN (SELECT token_length FROM naming_schemes WHERE id = NEW.scheme_id) != length(NEW.code)
BEGIN
    SELECT RAISE(ABORT, 'token code length must exactly match the scheme token_length');
END;

-- One counter per unique site+env+app+role prefix, per scheme. Unused for
-- manual-mode schemes (no rows ever inserted for them).
CREATE TABLE naming_sequences (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    scheme_id       INTEGER NOT NULL REFERENCES naming_schemes(id) ON DELETE CASCADE,
    computed_prefix TEXT NOT NULL,   -- e.g. "nycprderpweb" (12 chars, no separators)
    last_seq        INTEGER NOT NULL DEFAULT 0, -- 0-999 numeric, 1000-3599 maps to A00-Z99, hard-block beyond
    UNIQUE (scheme_id, computed_prefix)
);

INSERT INTO naming_schemes (name, template, naming_mode) VALUES
    ('Server', '{site}{env}{app}{role}{seq}', 'generated'),
    ('Network Device', '{site}{env}{app}{role}{seq}', 'generated'),
    ('VM', '{site}{env}{app}{role}{seq}', 'generated'),
    ('F5 Virtual Server', NULL, 'manual');

----------------------------------------------------------------------
-- SITE+ENV -> SUBNET MAPPING
----------------------------------------------------------------------
-- Scoped per naming scheme (not just per site+env) so, e.g., F5 Virtual
-- Server VIPs can draw from a dedicated LB/VIP subnet, separate from the
-- subnet servers use for the same site+env combination.
CREATE TABLE site_env_subnet_map (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    site_code        TEXT NOT NULL,
    env_code         TEXT NOT NULL,
    naming_scheme_id INTEGER NOT NULL REFERENCES naming_schemes(id) ON DELETE CASCADE,
    subnet_id        INTEGER NOT NULL REFERENCES subnets(id) ON DELETE RESTRICT,
    active           INTEGER NOT NULL DEFAULT 1,
    UNIQUE (site_code, env_code, naming_scheme_id)
);

----------------------------------------------------------------------
-- REQUESTS
----------------------------------------------------------------------
-- app_code/role_code are nullable: required for 'generated' schemes,
-- unused for 'manual' schemes (see trigger below). manual_name is the
-- requester-provided name for 'manual' schemes (e.g. an F5 VIP name) --
-- NULL for 'generated' schemes, where the name is computed instead.
CREATE TABLE requests (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    requester_id     INTEGER NOT NULL REFERENCES users(id),
    naming_scheme_id INTEGER NOT NULL REFERENCES naming_schemes(id),
    site_code        TEXT NOT NULL,
    env_code         TEXT NOT NULL,
    app_code         TEXT,
    role_code        TEXT,
    manual_name      TEXT,
    status           TEXT NOT NULL DEFAULT 'pending'
                       CHECK (status IN ('pending','approved','denied','cancelled')),
    generated_name   TEXT,   -- set only on approval: computed (generated) or copied from manual_name (manual)
    allocated_ip     TEXT,   -- set only on approval
    created_at       TEXT NOT NULL DEFAULT (datetime('now')),
    decided_by       INTEGER REFERENCES users(id),
    decided_at       TEXT
);

CREATE INDEX requests_requester_idx ON requests(requester_id);
CREATE INDEX requests_status_idx ON requests(status);

-- Enforce naming-mode-appropriate fields at the DB layer, same trigger
-- pattern as token-length enforcement above: app_code/role_code required
-- for 'generated' schemes; manual_name required for 'manual' schemes.
CREATE TRIGGER trg_requests_naming_mode_insert
BEFORE INSERT ON requests
FOR EACH ROW
WHEN
    ((SELECT naming_mode FROM naming_schemes WHERE id = NEW.naming_scheme_id) = 'generated'
        AND (NEW.app_code IS NULL OR NEW.role_code IS NULL))
    OR
    ((SELECT naming_mode FROM naming_schemes WHERE id = NEW.naming_scheme_id) = 'manual'
        AND NEW.manual_name IS NULL)
BEGIN
    SELECT RAISE(ABORT, 'app_code/role_code required for generated naming schemes; manual_name required for manual naming schemes');
END;

CREATE TRIGGER trg_requests_naming_mode_update
BEFORE UPDATE ON requests
FOR EACH ROW
WHEN
    ((SELECT naming_mode FROM naming_schemes WHERE id = NEW.naming_scheme_id) = 'generated'
        AND (NEW.app_code IS NULL OR NEW.role_code IS NULL))
    OR
    ((SELECT naming_mode FROM naming_schemes WHERE id = NEW.naming_scheme_id) = 'manual'
        AND NEW.manual_name IS NULL)
BEGIN
    SELECT RAISE(ABORT, 'app_code/role_code required for generated naming schemes; manual_name required for manual naming schemes');
END;

----------------------------------------------------------------------
-- SERVERS -- general asset registry, not just IPAM-provisioned ones.
-- Populated four ways, tracked via `source`: request approval
-- ('request', request_id set), manual admin entry ('manual'), CSV bulk
-- import ('csv_import'), or admin-direct allocation ('admin_direct') --
-- the latter three all have request_id NULL, so `source` is what
-- distinguishes them from each other. This is the central object that
-- maintenance windows and other future features attach to, independent
-- of how the server came to exist.
--
-- asset_type distinguishes real, patchable machines ('server') from F5
-- Virtual Server VIPs ('virtual_ip'), which are never patchable and must
-- never be eligible for maintenance_window_rules matching regardless of
-- what site/env/app/role values happen to be populated -- this is a
-- structural guarantee (a WHERE clause on asset_type), not an inference
-- from which fields are NULL. For source='request' rows, asset_type is
-- derived automatically from the originating naming scheme's
-- naming_mode ('generated' -> 'server', 'manual' -> 'virtual_ip'). For
-- the other three sources, an admin picks it explicitly at creation,
-- defaulting to 'server'.
--
-- site_code/env_code/app_code/role_code are denormalized here (not
-- looked up via request_id -> requests) so rule-based maintenance window
-- matching (maintenance_window_rules) works identically across all four
-- origins, not just request-provisioned servers. For source='request'
-- rows these are copied automatically from the approved request. For
-- the other three sources they are optional -- an admin can tag a
-- legacy/manually-registered server the same way if they want it
-- eligible for rule-based windows, or leave them NULL and rely on the
-- static maintenance_window_servers list only. NULL on any of these
-- fields never acts as a wildcard match against a rule -- it fails
-- closed: an unclassified server can only be covered by adding it to a
-- window's static list explicitly, never by a rule, no matter how broad
-- that rule's wildcards are.
----------------------------------------------------------------------
CREATE TABLE servers (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    hostname    TEXT NOT NULL UNIQUE,
    request_id  INTEGER REFERENCES requests(id),  -- set only when source = 'request'
    source      TEXT NOT NULL CHECK (source IN ('request','manual','csv_import','admin_direct')),
    asset_type  TEXT NOT NULL CHECK (asset_type IN ('server','virtual_ip')) DEFAULT 'server',
    site_code   TEXT,  -- optional for non-request sources; NULL never wildcard-matches a rule
    env_code    TEXT,
    app_code    TEXT,
    role_code   TEXT,
    description TEXT,                              -- context, especially for non-IPAM-provisioned servers
    created_by  INTEGER REFERENCES users(id),
    created_at  TEXT NOT NULL DEFAULT (datetime('now'))
);

----------------------------------------------------------------------
-- IP ALLOCATIONS
----------------------------------------------------------------------
CREATE TABLE ip_allocations (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    subnet_id    INTEGER NOT NULL REFERENCES subnets(id),
    ip_address   TEXT NOT NULL,
    request_id   INTEGER REFERENCES requests(id),  -- NULL for admin-direct/imported allocations
    server_id    INTEGER REFERENCES servers(id),   -- the server this IP belongs to
    allocated_at TEXT NOT NULL DEFAULT (datetime('now')),
    released_at  TEXT   -- set on decommission approval; IP becomes reusable, hostname never is
);

-- Partial unique index: an IP can only be actively held once per subnet, but the
-- SAME ip_address may have multiple historical rows (one per allocate/release cycle)
-- once released_at is set on the old row. This is what lets a released IP be
-- re-issued later without violating uniqueness against its own history.
CREATE UNIQUE INDEX ip_allocations_active_unique
    ON ip_allocations(subnet_id, ip_address)
    WHERE released_at IS NULL;

CREATE INDEX ip_allocations_subnet_idx ON ip_allocations(subnet_id);
CREATE INDEX ip_allocations_request_idx ON ip_allocations(request_id);

----------------------------------------------------------------------
-- DECOMMISSION REQUESTS
----------------------------------------------------------------------
CREATE TABLE decommission_requests (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    request_id   INTEGER NOT NULL REFERENCES requests(id),
    requested_by INTEGER NOT NULL REFERENCES users(id),
    reason       TEXT,
    status       TEXT NOT NULL DEFAULT 'pending'
                   CHECK (status IN ('pending','approved','denied')),
    decided_by   INTEGER REFERENCES users(id),
    decided_at   TEXT,
    created_at   TEXT NOT NULL DEFAULT (datetime('now'))
);

-- At most one OPEN decommission request per original request at a time
CREATE UNIQUE INDEX decommission_requests_open_unique
    ON decommission_requests(request_id)
    WHERE status = 'pending';

----------------------------------------------------------------------
-- AUDIT LOG
----------------------------------------------------------------------
CREATE TABLE audit_log (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    actor_user_id INTEGER REFERENCES users(id),  -- NULL for system-initiated events, if any
    action        TEXT NOT NULL,                 -- e.g. "request.approved", "scheme.token_added"
    target_type   TEXT,                          -- e.g. "request", "subnet", "user"
    target_id     TEXT,
    detail        TEXT,                          -- JSON text, free-form per action
    created_at    TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX audit_log_actor_idx ON audit_log(actor_user_id);
CREATE INDEX audit_log_created_idx ON audit_log(created_at);

----------------------------------------------------------------------
-- AUTH PROVIDER CONFIG (OIDC, LDAP) -- admin-configurable via UI
----------------------------------------------------------------------
-- Singleton rows (CHECK id = 1) rather than a users-style table, since
-- there is exactly one OIDC config and one LDAP config for the whole app.
-- Secrets (client_secret, bind_password) are stored in plaintext -- this
-- is a deliberate tradeoff for a self-hosted, open source tool: the
-- operator already has full filesystem access to the SQLite file, same
-- trust boundary as an env var. The real consequence is that Litestream
-- backups to S3 now contain live secrets, not just app data -- document
-- this for self-hosters so they secure the backup bucket accordingly.
--
-- LDAP has no stored TLS toggle by design: TLS/StartTLS is hardcoded in
-- the Go connection code, never a DB-driven option, so it can't be
-- disabled via a config UI mistake.
CREATE TABLE oidc_config (
    id            INTEGER PRIMARY KEY CHECK (id = 1),
    enabled       INTEGER NOT NULL DEFAULT 0,
    issuer_url    TEXT,
    client_id     TEXT,
    client_secret TEXT,
    redirect_url  TEXT
);

INSERT INTO oidc_config (id, enabled) VALUES (1, 0);

-- LDAP/AD connection config, admin-editable via /admin/auth-settings.
-- ca_cert is a PEM-encoded CA certificate, used to build a custom trust
-- pool for the LDAPS/TLS connection instead of the system default trust
-- store. This is necessary because real AD/LDAP servers are almost
-- always signed by an org's internal enterprise CA, not a publicly
-- trusted one -- without this, AuthenticateLDAP's TLS verification would
-- fail against most real deployments unless the container/host's OS
-- trust store was separately configured to trust that CA (a real
-- operational burden pushed onto every self-hoster with an internal CA).
-- NULL means "use the system default trust store" (fine for a publicly
-- trusted LDAPS cert, rare in practice for enterprise AD). Not a secret
-- -- a CA certificate is public information by design, safe alongside
-- the plaintext bind_password without changing the plaintext-secrets
-- tradeoff already accepted for this table.
CREATE TABLE ldap_config (
    id            INTEGER PRIMARY KEY CHECK (id = 1),
    enabled       INTEGER NOT NULL DEFAULT 0,
    server        TEXT,
    port          INTEGER,
    base_dn       TEXT,
    bind_dn       TEXT,
    bind_password TEXT,
    user_filter   TEXT,  -- e.g. "(uid=%s)" or "(sAMAccountName=%s)" for Active Directory
    ca_cert       TEXT   -- PEM-encoded CA cert; NULL = use system default trust store
);

INSERT INTO ldap_config (id, enabled) VALUES (1, 0);

----------------------------------------------------------------------
-- MAINTENANCE WINDOWS -- blackout periods for IPAM-provisioned servers,
-- consumed by external integrations (e.g. deployment tools) via the
-- /api/maintenance/* endpoints. Not vendor-specific -- general-purpose.
----------------------------------------------------------------------
-- rrule follows RFC 5545 (iCalendar). NULL rrule means a one-time
-- occurrence at dtstart for duration_minutes -- covers orgs whose
-- blackout dates are computed externally/manually each period (e.g.
-- payroll cycles that shift around holidays) rather than truly periodic.
-- Non-NULL rrule expands to multiple occurrences, each duration_minutes long.
CREATE TABLE maintenance_windows (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    label            TEXT NOT NULL,
    rrule            TEXT,                        -- NULL = one-time window
    dtstart          TEXT NOT NULL,                -- anchor date/time for rrule expansion, or the one-time start
    duration_minutes INTEGER NOT NULL,
    timezone         TEXT NOT NULL DEFAULT 'UTC',  -- recurrence math (e.g. "last day of month") is timezone-sensitive
    created_by       INTEGER REFERENCES users(id),
    created_at       TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE maintenance_window_servers (
    window_id INTEGER NOT NULL REFERENCES maintenance_windows(id) ON DELETE CASCADE,
    server_id INTEGER NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
    PRIMARY KEY (window_id, server_id)
);

-- Rule-based membership, additive to the static list above (a window's
-- effective server set is the UNION of both). Each row is one rule;
-- multiple rules on the same window are OR'd together. Within a single
-- rule, a NULL field is a wildcard ("any site", "any env", etc.) -- but
-- this wildcarding only ever broadens which SERVERS a rule can match; it
-- never causes a server with a NULL field to match (see the NULL
-- fail-closed comment on servers above -- that's a property of the
-- server row, not the rule).
--
-- Matching NEVER considers servers.asset_type = 'virtual_ip' -- this is
-- a hard, structural exclusion in the matching query (WHERE asset_type =
-- 'server'), not something expressible or overridable via a rule's own
-- fields. F5 Virtual Server VIPs are never patchable and must never be
-- swept into a rule-based window, regardless of what site/env/app/role
-- values a VIP's servers row happens to carry.
CREATE TABLE maintenance_window_rules (
    id        INTEGER PRIMARY KEY AUTOINCREMENT,
    window_id INTEGER NOT NULL REFERENCES maintenance_windows(id) ON DELETE CASCADE,
    site_code TEXT,   -- NULL = any site
    env_code  TEXT,   -- NULL = any env
    app_code  TEXT,   -- NULL = any app
    role_code TEXT    -- NULL = any role
);

----------------------------------------------------------------------
-- API KEYS -- machine auth for external integrations (maintenance
-- window read/write endpoints). Hashed, not plaintext: unlike
-- oidc_config/ldap_config secrets (which WE present to an external
-- system and must read back), an API key is a credential WE issue and
-- only ever need to verify, never read back -- same trust model as a
-- password.
----------------------------------------------------------------------
CREATE TABLE api_keys (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    label        TEXT NOT NULL,
    key_hash     TEXT NOT NULL UNIQUE,     -- argon2id, same as local admin password
    scope        TEXT NOT NULL CHECK (scope IN ('read', 'write')),
    created_by   INTEGER REFERENCES users(id),
    created_at   TEXT NOT NULL DEFAULT (datetime('now')),
    last_used_at TEXT,
    revoked_at   TEXT
);
