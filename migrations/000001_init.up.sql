----------------------------------------------------------------------
-- USERS, ROLES, USER_ROLES, SESSIONS
----------------------------------------------------------------------
-- Users: three concurrent auth sources (local admin, OIDC, LDAP) on one table
CREATE TABLE users (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    display_name  TEXT NOT NULL,
    email         TEXT,
    auth_source   TEXT NOT NULL CHECK (auth_source IN ('local','oidc','ldap')),
    oidc_subject  TEXT UNIQUE,           -- OIDC "sub" claim, NULL for local/ldap users
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

-- scs sessions table (sqlite3store expected schema)
CREATE TABLE sessions (
    token  TEXT PRIMARY KEY,
    data   BLOB NOT NULL,
    expiry REAL NOT NULL
);

CREATE INDEX sessions_expiry_idx ON sessions(expiry);

----------------------------------------------------------------------
-- SUBNETS + RESERVED IPS
----------------------------------------------------------------------
CREATE TABLE subnets (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    cidr       TEXT NOT NULL UNIQUE,     -- e.g. "10.20.4.0/24", validated + overlap-checked in Go via net/netip
    label      TEXT,
    active     INTEGER NOT NULL DEFAULT 1,
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
-- SITE+ENV -> SUBNET MAPPING
----------------------------------------------------------------------
-- Requester never picks a subnet directly: site+env resolves to one automatically
CREATE TABLE site_env_subnet_map (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    site_code  TEXT NOT NULL,
    env_code   TEXT NOT NULL,
    subnet_id  INTEGER NOT NULL REFERENCES subnets(id) ON DELETE RESTRICT,
    active     INTEGER NOT NULL DEFAULT 1,
    UNIQUE (site_code, env_code)
);

----------------------------------------------------------------------
-- NAMING SCHEMES, TOKEN VALUES, SEQUENCES
----------------------------------------------------------------------
CREATE TABLE naming_schemes (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    name         TEXT NOT NULL UNIQUE,       -- "Server", "Network Device", "VM"
    template     TEXT NOT NULL,              -- "{site}{env}{app}{role}{seq}" -- shared shape across all schemes
    token_length INTEGER NOT NULL DEFAULT 3, -- exact length required per token (not max)
    seq_length   INTEGER NOT NULL DEFAULT 3,
    total_length INTEGER NOT NULL DEFAULT 15 -- matches Windows NetBIOS limit
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

-- One counter per unique site+env+app+role prefix, per scheme
CREATE TABLE naming_sequences (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    scheme_id       INTEGER NOT NULL REFERENCES naming_schemes(id) ON DELETE CASCADE,
    computed_prefix TEXT NOT NULL,   -- e.g. "nycprderpweb" (12 chars, no separators)
    last_seq        INTEGER NOT NULL DEFAULT 0, -- 0-999 numeric, 1000-3599 maps to A00-Z99, hard-block beyond
    UNIQUE (scheme_id, computed_prefix)
);

INSERT INTO naming_schemes (name, template) VALUES
    ('Server', '{site}{env}{app}{role}{seq}'),
    ('Network Device', '{site}{env}{app}{role}{seq}'),
    ('VM', '{site}{env}{app}{role}{seq}');

----------------------------------------------------------------------
-- REQUESTS
----------------------------------------------------------------------
CREATE TABLE requests (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    requester_id     INTEGER NOT NULL REFERENCES users(id),
    naming_scheme_id INTEGER NOT NULL REFERENCES naming_schemes(id),
    site_code        TEXT NOT NULL,
    env_code         TEXT NOT NULL,
    app_code         TEXT NOT NULL,
    role_code        TEXT NOT NULL,
    status           TEXT NOT NULL DEFAULT 'pending'
                       CHECK (status IN ('pending','approved','denied','cancelled')),
    generated_name   TEXT,   -- set only on approval
    allocated_ip     TEXT,   -- set only on approval
    created_at       TEXT NOT NULL DEFAULT (datetime('now')),
    decided_by       INTEGER REFERENCES users(id),
    decided_at       TEXT
);

CREATE INDEX requests_requester_idx ON requests(requester_id);
CREATE INDEX requests_status_idx ON requests(status);

----------------------------------------------------------------------
-- IP ALLOCATIONS
----------------------------------------------------------------------
CREATE TABLE ip_allocations (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    subnet_id    INTEGER NOT NULL REFERENCES subnets(id),
    ip_address   TEXT NOT NULL,
    request_id   INTEGER REFERENCES requests(id),  -- NULL for admin-direct allocations
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
