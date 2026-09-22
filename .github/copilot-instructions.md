# IPAM OSS — Copilot Instructions

Self-hosted, open source IP address management tool. Replaces per-subnet
Excel sheets with a request/approval workflow, automatic IP allocation,
a standardized server naming convention, and blackout-window scheduling
consumable by external integrations.

## Stack

- **Language:** Go
- **DB:** SQLite via `mattn/go-sqlite3` (CGO enabled) — this is the ONLY
  SQLite driver in the project. Never suggest `modernc.org/sqlite` or mixing
  drivers; sessions (`sqlite3store`) require the CGo driver, and the whole
  app must share one driver against one file.
- **DB connection settings (not optional):** set via **DSN query
  parameters**, not a manual `PRAGMA` exec — `db.ExecContext(ctx, "PRAGMA
  foreign_keys = ON")` only applies to whichever single pooled connection
  happens to run it; when `sql.DB`'s pool later opens a fresh connection,
  that connection silently reverts to the default. The correct approach:
  open with `?_foreign_keys=on&_journal_mode=WAL&_busy_timeout=5000` in the
  DSN — `mattn/go-sqlite3` applies these to every connection it opens.
  Don't add a redundant manual `PRAGMA` call alongside the DSN params.
  Also cap `db.SetMaxOpenConns()` to a modest number (5-10) — SQLite only
  supports one writer at a time regardless of pool size. There is no
  traditional network-style "connection pooling" need here (no auth
  handshake, no TCP round trip) — `sql.DB`'s pool exists only to bound
  concurrent SQLite connections sanely, not to amortize connection setup
  cost.
- **Queries:** sqlc-generated. Hand-written query files live in
  `internal/db/queries/`, split by domain (`users.sql`, `subnets.sql`,
  `requests.sql`, `naming.sql`, `servers.sql`, `maintenance.sql`,
  `api_keys.sql`, etc.) — sqlc compiles all of them into one generated
  package regardless of the split. Generated code (models, `Querier`
  interface, per-query functions) lands in `internal/db/` (package `db`) —
  **never hand-edit generated `.go` files there; edit the `.sql` query and
  run `sqlc generate`.** Schema source of truth is the `migrations/`
  directory itself (sqlc reads it directly), not a separate hand-maintained
  schema file.
- **Migrations:** golang-migrate, embedded via `go:embed`, run automatically
  on startup, before the HTTP server starts listening — this means there is
  no window where the server is up but migrations aren't done (no readiness
  flag needed for `/healthz`; a live DB ping is sufficient).
- **Web:** `net/http` (stdlib router, method-specific patterns like
  `"GET /dashboard"`), `templ` for server-rendered HTML, `htmx` for
  interactivity, Alpine.js only where htmx genuinely can't reach (e.g. the
  RRULE builder's dynamic field visibility).
- **Sessions:** `alexedwards/scs` + `sqlite3store`.
- **Auth:** three user-facing sources (local admin, OIDC, LDAP), always
  concurrent — see below. Plus a fourth, machine-facing mechanism (API
  keys) for `/api/*` routes only.
- **Container:** CGO_ENABLED=1 build stage, `distroless/base-debian12`
  runtime (not `distroless/static` — CGo binaries need glibc).
- **Backups:** Litestream, replicating the SQLite file to S3 (dev) or a NAS
  path (prod), run as `litestream replicate -exec` wrapping the app binary.
  **Note:** `oidc_config.client_secret` and `ldap_config.bind_password` are
  stored in plaintext in the DB (deliberate tradeoff, see Authentication
  below) — this means Litestream backups contain live credentials, not
  just app data. Document this for self-hosters: secure the backup
  bucket/NAS path accordingly.

## Authentication (three user-facing sources, always concurrent)

1. **Local admin** — fixed username `administrator`, password from
   `ADMIN_PASSWORD` env var, argon2id (`alexedwards/argon2id`). The user
   row (`id = 1`) and its `admin` role assignment are **seeded directly in
   the initial migration**, not created at runtime — `password_hash`
   starts `NULL` and `EnsureLocalAdmin` only ever reconciles the hash on
   boot (create-on-first-boot is not a code path; the row always exists
   post-migration). On every boot: if the stored hash doesn't match
   `ADMIN_PASSWORD` (or is `NULL`), re-hash and overwrite — env var always
   wins on restart, this is deliberate. Every login (success or failure)
   is audit-logged. This account is a break-glass path, not the primary
   login.
2. **OIDC** — `coreos/go-oidc` + `golang.org/x/oauth2`. Configuration
   (`issuer_url`, `client_id`, `client_secret`, `redirect_url`) and an
   `enabled` flag live in the `oidc_config` table (singleton row,
   `id = 1`) — admin-configurable at runtime via `/admin/auth-settings`,
   no redeploy needed. Only shown on the login page if `enabled = 1`.
3. **LDAP/AD** — direct use of `go-ldap/ldap` (NOT a wrapper package like
   `go-ad-auth`). Configuration (`server`, `port`, `base_dn`, `bind_dn`,
   `bind_password`, `user_filter`, `ca_cert`) and an `enabled` flag live in
   the `ldap_config` table, same admin-UI pattern as OIDC. **TLS/StartTLS
   is hardcoded in the Go connection code, never a DB-driven or
   configurable option** — there is deliberately no `use_tls` column, so
   an admin can never misconfigure their way into a plaintext bind through
   the settings UI.
   - `ca_cert` is a PEM-encoded CA certificate, admin-pasteable, used to
     build a custom `x509.CertPool` for the LDAPS TLS connection instead
     of relying on the system default trust store. This exists because
     real AD/LDAP servers are almost always signed by an org's internal
     enterprise CA, not a publicly trusted one — without it,
     `AuthenticateLDAP`'s TLS verification would fail against most real
     deployments unless the container/host's OS trust store was
     separately configured (real operational friction for self-hosters
     with an internal CA). `NULL` means fall back to the system default
     trust store.
   - **`ca_cert` is NOT a secret** — a CA certificate is public
     information by design. It's stored alongside `bind_password` in the
     same table, but don't treat it with the same "why is this
     plaintext" caution; it doesn't need to be. Never confuse the two
     fields' sensitivity.
   - `users.ldap_dn` (parallel to `users.oidc_subject` for OIDC) is the
     stable identifier a returning LDAP user is matched against —
     populated from the entry's DN returned by the search-then-bind flow,
     not from the `uid`/`sAMAccountName` used in `user_filter` (that's
     only used to *find* the entry during the directory search, not to
     key the local `users` row).

Both `oidc_config` and `ldap_config` secrets (`client_secret`,
`bind_password` — NOT `ca_cert`) are plaintext — see the Backups note
above.

OIDC and LDAP auto-provision a `users` row on first successful login and
auto-assign the **`viewer`** role (read-only access to all requests,
subnets, and audit log) — chosen over no-roles-at-all to avoid a confusing
empty dashboard on first login. This assumes the IdP/AD's own membership
is already a meaningful access gate; an admin can grant additional roles
(or revoke `viewer`) via `/admin/users`.

The login page (`GET /login`) is a single unified page: local admin form
(de-emphasized as a collapsed disclosure — it's break-glass, not the
primary option), SSO button shown only if `oidc_config.enabled`, LDAP form
shown only if `ldap_config.enabled`. `POST /login` dispatches by a hidden
`method` field. There is no separate `/login/local` route.

After a successful login, redirect to whatever path was stashed in the
session before the auth redirect (`middleware.PostLoginRedirectSessionKey`,
via `SafeRedirectPath`), falling back to `/dashboard` if nothing was
stashed.

## RBAC

Roles: `admin`, `approver`, `requester`, `viewer`. A user can hold multiple
roles, and **roles are additive, not hierarchical** — `admin` does not
implicitly grant `approver` or `requester` access. Role checks happen in
per-route middleware, never scattered through template logic. Some routes
also need an ownership check beyond the role gate (e.g. `requester` can
only edit their own pending requests) — that's a second check in the
handler, not part of the role middleware.

Permission summary: `requester` submits/manages their own requests only;
`approver` acts on the provisioning and decommission queues (not scoped to
"own"); `admin` manages subnets, naming schemes, mappings, users, audit
log, OIDC/LDAP settings, servers, maintenance windows, and API keys;
`viewer` gets read-only visibility into all requests/subnets/audit log.
See `docs/routes.md` for the exact required role per route.

## Route classification

Every handler is one of:
- **PAGE** — full HTML document, GET, browser navigation
- **FRAGMENT** — htmx-triggered partial swap, no full navigation (used for
  fast in-place actions: approve/deny, cancel, role toggle)
- **ACTION** — POST that mutates and redirects (classic POST/redirect/GET;
  used for multi-field forms), or a machine-consumed API endpoint

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
poll (which hits this endpoint every few seconds) that this design avoids.

`/api/*` routes use a **fourth, distinct auth mechanism**: an API-key
middleware (`X-API-Key` header, hashed lookup against `api_keys`, scope
check) — not session/CSRF/RBAC. This is machine-to-machine auth for
external integrations, not a browser-facing route group. Don't apply
session or CSRF middleware to `/api/*` routes, and don't apply the
API-key middleware to browser-facing routes.

## Atomic multi-step writes

Use `db.WithTx(ctx, sqlDB, func(q *db.Queries) error { ... })` for any
write spanning more than one statement where a partial failure would
leave the DB inconsistent (e.g. a user created with no role assigned).
See `docs/workflows.md`'s "Atomic Multi-Step Writes" section for the full
pattern and current/future callers (OIDC provisioning, the approval
transaction). **Don't** use it for per-item batch operations where
partial success is the intended behavior (CSV import, the maintenance-
window batch API) — those are explicitly NOT single transactions.

## Core workflow (the thing this app actually does)

1. Requester picks a naming scheme, site, env, app, role. **No subnet or IP
   picker is ever shown** — site+env resolves to a subnet via
   `site_env_subnet_map`, and the IP is auto-assigned at approval time,
   skipping reserved IPs.
2. Approver approves or denies. Approval is one DB transaction: allocate
   next free IP in the mapped subnet, lock and increment the naming
   sequence for the exact site+env+app+role prefix, generate the hostname,
   write `ip_allocations` (with `server_id` set), create/update the
   corresponding `servers` row, update the request, write `audit_log`. All
   or nothing.
3. Decommissioning is a **separate** approval flow
   (`decommission_requests`), not a request status. On approval, the IP's
   `ip_allocations` row gets `released_at` set (IP becomes reusable) but
   **the hostname and its sequence number are never reused**.
4. Admin-direct allocation and CSV import also create `servers` +
   `ip_allocations` rows, bypassing the request/approval flow — every
   allocation path must populate `ip_allocations.server_id`, not just the
   request-driven one.

## Naming convention

See `docs/workflows.md` for the full step-by-step version of this and
every other workflow in the app (plain-text mirror of the Eraser
diagrams).

Naming schemes have a `naming_mode`: `generated` or `manual`.

- **`generated`** (Server, Network Device, VM): fixed-length, no
  separators — `{site}{env}{app}{role}{seq}`, each token exactly 3
  characters, total 15 characters (matches the Windows NetBIOS limit).
  Sequence is per exact prefix (site+env+app+role combo): `000`-`999`
  numeric, then `A00`-`Z99` letter rollover. Hard-block (fail the
  approval, require manual intervention) if a prefix would exceed `Z99`
  — never silently wrap around. `requests.app_code`/`role_code` are
  required for these schemes (enforced by a DB trigger).
- **`manual`** (F5 Virtual Server — reserving IPs for F5 LTM virtual
  servers): no computed name, no sequence, no 15-character constraint at
  all. The requester provides `requests.manual_name` directly; it's
  validated for uniqueness (against `servers.hostname`) at approval time
  and copied straight to `generated_name`. `app_code`/`role_code` are NOT
  required for these schemes (same trigger, opposite direction). More
  manual-mode schemes can be added later without further schema changes.
- **`site_env_subnet_map` is scoped per `naming_scheme_id`**, not just
  site+env — this is what lets a manual-mode scheme (like F5 VIPs) draw
  from a dedicated subnet, separate from whatever servers use for the
  same site+env.
- **The request form is scheme-conditional**: selecting a naming scheme
  triggers an htmx fragment swap (`GET /requests/new/fields?scheme_id=`)
  that shows app+role dropdowns for `generated` schemes or a single
  free-text name input for `manual` schemes. Don't render both field sets
  and hide one with CSS — swap them server-side so unused fields are
  never submitted.
- **The approval transaction branches by `naming_mode`** — see
  `docs/workflows.md`'s "Request and Approval Workflow" section for the
  exact branch logic.

## Login rate limiting

`login_attempts` (keyed on `identifier` + `auth_method`, `UNIQUE` pair)
tracks failed login attempts and applies **exponential backoff**, not a
flat lockout — a flat "N failures then locked for M minutes" policy is
weaponizable (an attacker can deliberately lock out the real admin by
repeatedly failing their login on purpose).

- Tracked **per identity, not per source IP** — avoids locking out a
  whole office behind one shared NAT gateway. Tradeoff: this doesn't
  block distributed username-enumeration across many different
  identifiers, only protects a specific targeted account. Accepted
  tradeoff, not an oversight.
- `identifier` is `"administrator"` (fixed) for local admin, or the
  submitted username for LDAP. **OIDC is exempt** — IPAM never sees a
  password for that flow; the IdP owns its own lockout policy.
- Backoff schedule (`internal/auth/rate_limit.go`,
  `loginBackoffDuration`): no penalty for the first 2 failures (typos
  happen), then doubling from 5s, capped at 5 minutes.
- **Always check `CheckLoginLockout` BEFORE attempting the actual
  password/bind check** — a locked-out attempt must never do the
  expensive work (argon2 comparison, LDAP bind), both for cost and to
  avoid leaking timing information.
- **`login_attempts.locked_until` is stored as RFC3339, NOT SQLite's
  `datetime('now')` style** used by every other timestamp column in this
  schema. This is intentional, not an inconsistency to "fix": it's a
  value computed and formatted entirely in Go
  (`time.Now().Add(backoffDuration)`), not something SQLite generates —
  RFC3339 round-trips cleanly with `time.Parse`/`time.Format` without
  hand-matching SQLite's string format. Don't change it to match the
  other columns.

## Servers, Maintenance Windows, and the External Integration API

- **`servers`** is a general asset registry, decoupled from the request/
  provisioning lifecycle — NOT the same thing as `requests`. Populated
  four ways, tracked via `servers.source` (`'request'`, `'manual'`,
  `'csv_import'`, `'admin_direct'`): request approval sets both
  `source = 'request'` and `request_id`; the other three set `request_id`
  NULL — **`source` is what distinguishes them from each other**,
  `request_id` NULL alone can't tell manual entry from CSV import from
  admin-direct allocation. Always set `source` explicitly on every
  allocation path — never leave it to be inferred. `ip_allocations.server_id`
  links every allocation to its server regardless of origin.
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
  pure htmx flow. Pair it with a live occurrence-preview fragment
  (`POST /admin/maintenance-windows/preview`) — RRULE strings are easy to
  get subtly wrong.
- **`/api/maintenance/blocked` and `/allowed` must include each server's
  `description` in the response**, not just hostname — deliberate: a human
  consuming the response (e.g. reviewing tonight's deployment list) uses
  the description to judge whether a technically-listed server is actually
  critical enough to skip.
- **This API is general-purpose, not built for any specific vendor** (it
  was motivated by a KACE incident, but must not be designed around KACE
  specifically) — any integration that can make an HTTP request with an
  API key should be able to consume it.
- **`api_keys` stores a hash, never plaintext** — opposite trust model
  from `oidc_config`/`ldap_config`: an API key is a credential *we issue*
  and only ever verify, never read back, same as a user password
  (argon2id). Don't conflate this with the OIDC/LDAP secrets pattern.
- **API keys are scoped `read` or `write`** — `read` covers the two GET
  endpoints, `write` covers the batch-create endpoint. Least privilege: an
  integration that only checks blocked servers should never be handed a
  `write` key.
- **`POST /api/maintenance-windows` and CSV import are per-item, not
  all-or-nothing** — one bad item in a batch shouldn't fail the rest;
  report per-item success/failure.

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
  timeout — these must be DSN query parameters, not a post-open exec, or
  the pool silently loses the setting on new connections.
- Don't add a `CrossOriginProtection` bypass for the OIDC callback route
  speculatively — it's only needed if `response_mode=form_post` is
  explicitly configured. A standard GET redirect callback is never checked
  by CSRF protection in the first place (it only checks POST/PATCH/DELETE).
- Don't wrap `/healthz` in the same middleware stack as authenticated
  routes, even conditionally.
- Don't add a `use_tls`/`enabled_tls` column (or similar) to `ldap_config` —
  TLS/StartTLS is intentionally hardcoded in the connection code, never a
  stored or admin-configurable option.
- Don't suggest OIDC/LDAP config via env vars — superseded; both now live
  in `oidc_config`/`ldap_config` DB tables, admin-editable at runtime.
  `ADMIN_PASSWORD` (local admin only) is still an env var — don't conflate
  the two.
- Don't store `api_keys` in plaintext — opposite trust model from
  OIDC/LDAP secrets.
- Don't build the RRULE builder as a pure htmx flow with server round-trips
  for every field change — the FREQ-dependent field visibility is
  genuinely client-side, stateful UI (Alpine.js).
- Don't design `/api/maintenance/*` responses or behavior around KACE (or
  any specific vendor) — it's a general-purpose integration surface.
- Don't make `POST /api/maintenance-windows` or CSV import all-or-nothing —
  both are explicitly per-item/per-row.
- Don't skip setting `ip_allocations.server_id` on any allocation path
  (request approval, admin-direct, CSV import) — every path must set it.
- Don't create a "create local admin on first boot" code path —
  `EnsureLocalAdmin` only ever reconciles the password hash; the user row
  and its role are seeded in the migration, not created at runtime.
- Don't register a route at a bare `/` on the `ServeMux` — it's the
  catch-all pattern. The dashboard is `GET /dashboard`.
- Don't apply the 15-character NetBIOS length constraint (or any part of
  the generated-mode sequence logic) to `manual`-mode schemes — F5 VIP
  names have no such limit and are never computed, only validated for
  uniqueness.
- Don't make `app_code`/`role_code` unconditionally required on
  `requests` — they're required only for `generated`-mode schemes
  (enforced by trigger, not a plain `NOT NULL`).
- Don't assume `site_env_subnet_map` has one row per site+env — it's
  scoped per `naming_scheme_id` too; a site+env can map to different
  subnets for different schemes.
- Don't skip the `naming_mode` branch in the approval transaction and
  route every request through the sequence-generation path.
- Don't infer a server's origin from `request_id IS NULL` alone — use
  `servers.source` directly. `request_id NULL` is ambiguous between
  manual entry, CSV import, and admin-direct allocation.
- Don't treat `ldap_config.ca_cert` as a secret (mask it in a form,
  exclude it from a "safe to display" list, etc.) — it's a public CA
  certificate, unlike `bind_password` in the same table.
- Don't populate `users.ldap_dn` from the submitted username or the
  `user_filter` match value — use the DN the directory search actually
  returned for that entry.
- Don't "fix" `login_attempts.locked_until` to match the `datetime('now')`
  style of other timestamp columns — it's intentionally RFC3339, see
  "Login rate limiting" above.
- Don't replace the exponential backoff in `login_attempts` with a flat
  N-failures-then-locked-for-M-minutes policy — it's weaponizable against
  the real account holder.
- Don't key `login_attempts` by source IP instead of identifier — that's
  a deliberate choice, not something to "improve."
- Don't apply rate limiting to OIDC — it never receives a password
  through IPAM.
