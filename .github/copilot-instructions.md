# IPAM OSS — Copilot Instructions

Self-hosted, open source IP address management tool. Replaces per-subnet Excel
sheets with a request/approval workflow, automatic IP allocation, and a
standardized server naming convention.

## Stack

- **Language:** Go
- **DB:** SQLite via `mattn/go-sqlite3` (CGO enabled) — this is the ONLY
  SQLite driver in the project. Never suggest `modernc.org/sqlite` or mixing
  drivers; sessions (`sqlite3store`) require the CGo driver, and the whole
  app must share one driver against one file.
- **DB connection settings (not optional):** set via **DSN query parameters**,
  not a manual `PRAGMA` exec — `db.ExecContext(ctx, "PRAGMA foreign_keys = ON")`
  only applies to whichever single pooled connection happens to run it; when
  `sql.DB`'s pool later opens a fresh connection, that connection silently
  reverts to the default. The correct approach: open with
  `?_foreign_keys=on&_journal_mode=WAL&_busy_timeout=5000` in the DSN —
  `mattn/go-sqlite3` applies these to every connection it opens. Don't add a
  redundant manual `PRAGMA` call alongside the DSN params; it's dead weight
  that implies the wrong mental model. Also cap `db.SetMaxOpenConns()` to a
  modest number (5-10) — SQLite only supports one writer at a time
  regardless of pool size, so a large pool doesn't help and can increase
  lock contention. There is no traditional network-style "connection
  pooling" need here (no auth handshake, no TCP round trip) — `sql.DB`'s
  pool exists only to bound concurrent SQLite connections sanely, not to
  amortize connection setup cost.
- **Queries:** sqlc-generated. Hand-written query files live in
  `internal/db/queries/`, split by domain (`users.sql`, `subnets.sql`,
  `requests.sql`, `naming.sql`, etc.) — sqlc compiles all of them into one
  generated package regardless of the split. Generated code (models,
  `Querier` interface, per-query functions) lands in `internal/db/` (package
  `db`) — **never hand-edit generated `.go` files there; edit the `.sql`
  query and run `sqlc generate`.** Schema source of truth is the
  `migrations/` directory itself (sqlc reads it directly), not a separate
  hand-maintained schema file.
- **Migrations:** golang-migrate, embedded via `go:embed`, run automatically
  on startup. See `.github/instructions/migrations.instructions.md`.
- **Web:** `net/http` (stdlib router), `templ` for server-rendered HTML,
  `htmx` for interactivity, Alpine.js only where htmx genuinely can't reach.
- **Sessions:** `alexedwards/scs` + `sqlite3store`.
- **Auth:** three concurrent, independently-configurable sources — see below.
- **Container:** CGO_ENABLED=1 build stage, `distroless/base-debian12`
  runtime (not `distroless/static` — CGo binaries need glibc).
- **Backups:** Litestream, replicating the SQLite file to S3 (dev) or a NAS
  path (prod), run as `litestream replicate -exec` wrapping the app binary.
  **Note:** `oidc_config.client_secret` and `ldap_config.bind_password` are
  stored in plaintext in the DB (deliberate tradeoff, see Authentication
  section below) — this means Litestream backups contain live credentials,
  not just app data. Document this for self-hosters: secure the backup
  bucket/NAS path accordingly (access controls, encryption at rest on the
  storage side).

## Authentication (three sources, always concurrent)

1. **Local admin** — fixed username `administrator`, password from
   `ADMIN_PASSWORD` env var, argon2id (`alexedwards/argon2id`). On every
   boot: if the stored hash doesn't match `ADMIN_PASSWORD`, re-hash and
   overwrite (env var always wins on restart — this is deliberate). Every
   login (success or failure) is audit-logged. This account always exists;
   it's a break-glass path, not the primary login.
2. **OIDC** — `coreos/go-oidc` + `golang.org/x/oauth2`. Configuration
   (`issuer_url`, `client_id`, `client_secret`, `redirect_url`) and an
   `enabled` flag live in the `oidc_config` table (singleton row,
   `id = 1`) — admin-configurable at runtime via the admin UI, no
   redeploy/env var needed. Only shown on the login page if `enabled = 1`.
3. **LDAP/AD** — direct use of `go-ldap/ldap` (NOT a wrapper package like
   `go-ad-auth`). Configuration (`server`, `port`, `base_dn`, `bind_dn`,
   `bind_password`, `user_filter`) and an `enabled` flag live in the
   `ldap_config` table (singleton row, `id = 1`), same admin-UI pattern as
   OIDC. **TLS/StartTLS is hardcoded in the Go connection code, never a
   DB-driven or configurable option** — there is deliberately no `use_tls`
   column, so an admin can never misconfigure their way into a plaintext
   bind through the settings UI.

Both `oidc_config` and `ldap_config` secrets (`client_secret`,
`bind_password`) are stored in plaintext — see the Backups note above for
the consequence of this.

OIDC and LDAP auto-provision a `users` row on first successful login with
**no roles assigned**. An existing admin must explicitly grant roles before
that user can access anything role-gated.

## RBAC

Roles: `admin`, `approver`, `requester`, `viewer`. A user can hold multiple
roles, and **roles are additive, not hierarchical** — `admin` does not
implicitly grant `approver` or `requester` access. Role checks happen in
per-route middleware, never scattered through template logic. Some routes
also need an ownership check beyond the role gate (e.g. `requester` can only
edit their own pending requests) — that's a second check in the handler,
not part of the role middleware.

Permission summary: `requester` submits/manages their own requests only;
`approver` acts on the provisioning and decommission queues (not scoped to
"own"); `admin` manages subnets, naming schemes, mappings, users, and audit
log; `viewer` gets read-only visibility into all requests/subnets/audit log.
See `docs/routes.md` for the exact required role per route.

## Route classification

Every handler is one of:
- **PAGE** — full HTML document, GET, browser navigation
- **FRAGMENT** — htmx-triggered partial swap, no full navigation (used for
  fast in-place actions: approve/deny, cancel, role toggle)
- **ACTION** — POST that mutates and redirects (classic POST/redirect/GET;
  used for multi-field forms where a full re-render with validation errors
  makes more sense than a fragment swap)

See `docs/routes.md` for the full route table.

## Middleware architecture

Per-route-group middleware (session load/save via `scs`, CSRF via
`CrossOriginProtection`, request logging) is assembled with
`middleware.MiddlewareStack(...)` and applied to specific route groups
(e.g. `publicMiddleware`) at route registration — **never wrapped globally
at `http.ListenAndServe`**. The only middleware wrapped globally is
`RecoverMiddleware` (panic recovery), because it must apply to every route
including `/healthz`.

`/healthz` is registered directly on the top-level `mux` with **no**
middleware stack — no session, no CSRF, no RBAC. This is a deliberate
structural isolation (a route with genuinely no stack), not a conditional
skip inside a shared middleware — don't "simplify" this by wrapping
`/healthz` in the same stack as everything else with an early-return
exception; that reintroduces the DB/session overhead on every orchestrator
poll (which hits this endpoint every few seconds) that this design
avoids.

`/api/*` routes use a **fourth, distinct auth mechanism**: an API-key
middleware (`X-API-Key` header, hashed lookup against `api_keys`, scope
check) — not session/CSRF/RBAC. This is machine-to-machine auth for
external integrations, not a browser-facing route group. Don't apply
session or CSRF middleware to `/api/*` routes, and don't apply the
API-key middleware to browser-facing routes.

## Core workflow (the thing this app actually does)

1. Requester picks a naming scheme, site, env, app, role. **No subnet or IP
   picker is ever shown** — site+env resolves to a subnet via
   `site_env_subnet_map`, and the IP is auto-assigned at approval time.
2. Approver approves or denies. Approval is one DB transaction: allocate
   next free IP in the mapped subnet (skip reserved + already-allocated),
   lock and increment the naming sequence for the exact
   site+env+app+role prefix, generate the hostname, write `ip_allocations`,
   update the request, write `audit_log`. All or nothing.
3. Decommissioning is a **separate** approval flow
   (`decommission_requests`), not a request status. On approval, the IP's
   `ip_allocations` row gets `released_at` set (IP becomes reusable) but
   **the hostname and its sequence number are never reused**.

## Naming convention

Fixed-length, no separators: `{site}{env}{app}{role}{seq}`, each token
exactly 3 characters, total 15 characters (matches the Windows NetBIOS
limit). Sequence is per exact prefix (site+env+app+role combo): `000`-`999`
numeric, then `A00`-`Z99` letter rollover. Hard-block (fail the approval,
require manual intervention) if a prefix would exceed `Z99` — never
silently wrap around.

## Servers, Maintenance Windows, and the External Integration API

- **`servers`** is a general asset registry, decoupled from the request/
  provisioning lifecycle — NOT the same thing as `requests`. Populated
  three ways: request approval (`request_id` set), manual admin entry, or
  CSV bulk import (`request_id` NULL for the latter two). Admin-direct
  allocation also creates a `servers` row. `ip_allocations.server_id`
  links every allocation to its server regardless of origin — always set
  this when writing an `ip_allocations` row, on every allocation path.
- **`maintenance_windows`** uses RFC 5545 RRULE (`teambition/rrule-go` for
  expansion, don't hand-roll recurrence math). `rrule` is nullable — NULL
  means a one-time occurrence at `dtstart` for `duration_minutes` (for
  orgs whose blackout dates are computed externally each period rather
  than truly periodic, e.g. payroll cycles shifting around holidays).
  `maintenance_window_servers` is a join table — one window can cover many
  servers.
- **The RRULE builder UI is a deliberate Alpine.js case** — selecting
  `FREQ=MONTHLY` vs `FREQ=WEEKLY` should reveal different fields
  immediately, client-side, no server round-trip. Don't build this as a
  pure htmx flow.
- **`/api/maintenance/blocked` and `/allowed` must include each server's
  `description` in the response**, not just hostname — this is
  deliberate: a human consuming the response (e.g. reviewing tonight's
  deployment list) uses the description to judge whether a technically-
  listed server is actually critical enough to skip.
- **This API is general-purpose, not built for any specific vendor** (it
  was motivated by a KACE incident, but must not be designed around KACE
  specifically) — any integration that can make an HTTP request with an
  API key should be able to consume it.
- **`api_keys` stores a hash, never plaintext** — this is the opposite
  trust model from `oidc_config`/`ldap_config`: an API key is a credential
  *we issue* and only ever verify, never read back, same as a user
  password (argon2id). Don't conflate this with the OIDC/LDAP secrets
  pattern.
- **API keys are scoped `read` or `write`** — `read` covers the two GET
  endpoints, `write` covers the batch-create endpoint. Least privilege:
  an integration that only checks blocked servers should never be handed
  a `write` key.
- **`/api/*` routes use a fourth auth mechanism** (`X-API-Key` header,
  hashed lookup, scope check) — completely separate from session/CSRF/RBAC.
  See "Middleware architecture" below.

## Things Copilot should NOT suggest

- Don't suggest `modernc.org/sqlite`, `bcrypt`, or reintroducing a subnet/IP
  picker on the request form — all deliberately decided against.
- Don't suggest reusing a decommissioned hostname or its sequence number.
- Don't suggest storing sessions anywhere but the `sessions` table via
  `sqlite3store`.
- Don't add IPv6-specific allocation logic (delegation/`/64` handling) — out
  of scope for v1 by design, though CIDR storage/math (`net/netip`) is
  already dual-stack-safe.
- Don't suggest `db.ExecContext(ctx, "PRAGMA ...")` for foreign keys/WAL/busy
  timeout — these must be DSN query parameters (see "DB connection
  settings" above), not a post-open exec, or the pool silently loses the
  setting on new connections.
- Don't add a `CrossOriginProtection` bypass for the OIDC callback route
  speculatively — it's only needed if `response_mode=form_post` is
  explicitly configured. A standard GET redirect callback is never checked
  by CSRF protection in the first place (it only checks POST/PATCH/DELETE).
- Don't wrap `/healthz` in the same middleware stack as authenticated
  routes, even conditionally — see "Middleware architecture" above.
- Don't add a `use_tls`/`enabled_tls` column (or similar) to `ldap_config` —
  TLS/StartTLS is intentionally hardcoded in the connection code, never a
  stored or admin-configurable option. Adding one, even defaulted to true,
  reintroduces the exact misconfiguration risk this design avoids.
- Don't suggest OIDC/LDAP config via env vars — that was the original
  design and is superseded; both now live in `oidc_config`/`ldap_config`
  DB tables, admin-editable at runtime. `ADMIN_PASSWORD` (local admin
  only) is still an env var — don't conflate the two.
- Don't store `api_keys` in plaintext — this is the opposite trust model
  from OIDC/LDAP secrets (see "Servers, Maintenance Windows..." above).
- Don't build the RRULE builder as a pure htmx flow with server round-trips
  for every field change — the FREQ-dependent field visibility is
  genuinely client-side, stateful UI (Alpine.js).
- Don't design `/api/maintenance/*` responses or behavior around KACE (or
  any specific vendor) — it's a general-purpose integration surface.
- Don't make `POST /api/maintenance-windows` (or the CSV import) all-or-
  nothing — both are explicitly per-item/per-row, with individual
  success/failure reporting.
- Don't hardcode `ip_allocations.server_id` as optional/skippable on any
  allocation path (request approval, admin-direct, CSV import) — every
  path must set it.
