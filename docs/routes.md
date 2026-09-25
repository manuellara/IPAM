# Routes and Endpoints

This is the canonical, code-adjacent reference for every route in the app.
Update this file when a route is added, removed, or changes shape —
treat it as documentation that ships with the code, not a planning
scratchpad.

**Type key:**
- **PAGE** — full HTML document, GET, hit via browser navigation
- **FRAGMENT** — htmx-triggered, returns a partial HTML swap, no full
  navigation. Used for fast, low-risk, in-place actions.
- **ACTION** — POST that mutates and redirects (classic POST/redirect/GET),
  or a machine-consumed API endpoint (see External Integration API below).

## RBAC

Roles are **additive, not hierarchical** — a user needs each role
explicitly assigned (e.g. an admin who also approves needs the `approver`
role too, it isn't implied by `admin`).

| Role | Can do |
|---|---|
| `requester` | Submit requests; view/edit/cancel their own pending requests; edit/resubmit their own denied requests; initiate decommission on their own approved requests |
| `approver` | View and act on the provisioning queue (approve/deny any pending request); view and act on the decommission queue |
| `admin` | Manage subnets, site+env mappings, naming schemes/token values, user role assignments, audit log, OIDC/LDAP settings, servers, maintenance windows, API keys; admin-direct allocations; CSV import/export |
| `viewer` | Read-only visibility into **all** requests (not just their own), subnets, and audit log — no write access anywhere |

In the tables below, **Any** means any authenticated user regardless of
role, and **None** means no auth required.

## Public / Unauthenticated

| Type | Method | Path | Required Role | Description |
|---|---|---|---|---|
| PAGE | GET | `/login` | None | Unified login page: local admin form (de-emphasized), SSO button if `oidc_config.enabled`, LDAP form if `ldap_config.enabled` — all DB-driven, not env vars |
| ACTION | POST | `/login` | None | Dispatches by hidden `method` field (local/ldap); OIDC uses a separate redirect flow |
| ACTION | GET | `/auth/oidc/login` | None | Kicks off OIDC redirect to IdP (only reachable if `oidc_config.enabled`) |
| ACTION | GET | `/auth/oidc/callback` | None | Redirect only, no body |
| ACTION | POST | `/logout` | Any | Redirects to `/login` |

## All Authenticated Users

| Type | Method | Path | Required Role | Description |
|---|---|---|---|---|
| PAGE | GET | `/dashboard` | Any | Nav shell (phase 1) composed from links based on the user's assigned roles; will gain data widgets (phase 2) once requests/subnets exist. **Not `/`** — that's `ServeMux`'s catch-all pattern and collides with other registrations. |
| PAGE | GET | `/requests` | `requester`, `viewer`, or `admin` | Requester sees own requests; viewer/admin see all (read-only for viewer) |
| PAGE | GET | `/requests/new` | `requester` | Submission form: scheme → site → env, then EITHER app+role dropdowns (generated schemes) OR a free-text "Virtual Server Name" field (manual schemes, e.g. F5 Virtual Server). No subnet/IP picker. |
| FRAGMENT | GET | `/requests/new/fields?scheme_id=` | `requester` | Swaps the field set based on the selected scheme's `naming_mode` |
| ACTION | POST | `/requests` | `requester` | Redirects to `/requests/{id}` on success |
| PAGE | GET | `/requests/{id}` | `requester` (own), `viewer`, or `admin` | Detail view with status timeline; shows Decommission action once approved |
| PAGE | GET | `/requests/{id}/edit` | `requester` (own) | Edit form (pending: correcting a mistake; denied: resubmission) |
| ACTION | POST | `/requests/{id}` | `requester` (own) | Saves edit / resubmits (denied → pending) |
| FRAGMENT | POST | `/requests/{id}/cancel` | `requester` (own) | Only allowed while pending |
| FRAGMENT | POST | `/requests/{id}/decommission` | `requester` (own) | Creates a pending `decommission_request` linked to this request |

## Approver Role

| Type | Method | Path | Required Role | Description |
|---|---|---|---|---|
| PAGE | GET | `/approvals` | `approver` | Pending provisioning queue |
| PAGE | GET | `/approvals/{id}` | `approver` | Detail with approve/deny actions |
| FRAGMENT | POST | `/approvals/{id}/approve` | `approver` | Atomic transaction, branches by `naming_mode`: generated (sequence lock/increment/generate) vs manual (validate + copy `manual_name`). Allocates IP, writes `servers` row, audit log, emails requester either way. |
| FRAGMENT | POST | `/approvals/{id}/deny` | `approver` | Emails requester |
| PAGE | GET | `/decommissions` | `approver` | Pending decommission queue |
| PAGE | GET | `/decommissions/{id}` | `approver` | Detail with approve/deny actions |
| FRAGMENT | POST | `/decommissions/{id}/approve` | `approver` | Releases the IP back to the pool (hostname/sequence never reused) |
| FRAGMENT | POST | `/decommissions/{id}/deny` | `approver` | Emails requester with reason |

## Admin Role

| Type | Method | Path | Required Role | Description |
|---|---|---|---|---|
| PAGE | GET | `/admin/subnets` | `admin` (`viewer`: read-only) | List with per-subnet utilization (stacked allocated/reserved/free bar) and site/env tags; search is client-side (Alpine.js `x-show`) — acceptable because this table is admin-sized/bounded, unlike audit log below |
| PAGE | GET | `/admin/subnets/new` | `admin` | Shares `SubnetFormPage`/`SubnetFormMain` templ with the edit form (`SubnetFormData.IsEdit` flag), not a separate template |
| ACTION | POST | `/admin/subnets` | `admin` | Overlap validation enforced (active subnets only) |
| PAGE | GET | `/admin/subnets/{id}/edit` | `admin` | Shows an active-allocation-count warning if unchecking "active" |
| ACTION | POST | `/admin/subnets/{id}` | `admin` | Overlap validation excludes self |
| PAGE | GET | `/admin/site-env-map` | `admin` | Site+env+**scheme** → subnet mappings (scheme-scoped, so e.g. F5 VIPs can use a dedicated subnet) |
| PAGE | GET | `/admin/site-env-map/new` | `admin` | |
| ACTION | POST | `/admin/site-env-map` | `admin` | |
| PAGE | GET | `/admin/naming-schemes` | `admin` | The 4 schemes (Server, Network Device, VM, F5 Virtual Server) |
| PAGE | GET | `/admin/naming-schemes/{id}` | `admin` | Scheme detail + token value tables + `naming_mode` |
| PAGE | GET | `/admin/naming-schemes/{id}/tokens/new` | `admin` | |
| FRAGMENT | POST | `/admin/naming-schemes/{id}/tokens` | `admin` | Exact 3-char code length enforced |
| PAGE | GET | `/admin/users` | `admin` | List, assign roles |
| FRAGMENT | POST | `/admin/users/{id}/roles` | `admin` | |
| PAGE | GET | `/admin/audit-log` | `admin` (`viewer`: read-only) | **Not yet built** (planned work item, unscheduled). Unlike `/admin/subnets`, this table is unbounded/append-only — it must use real server-side pagination and filtering, not client-side `x-show`. See "Admin List Pagination Convention" in `docs/workflows.md`. |
| ACTION | POST | `/admin/allocations` | `admin` | Admin-direct allocation; also creates a `servers` row |
| PAGE | GET | `/admin/allocations/export.csv` | `admin` | Current (non-released) allocations |
| PAGE | GET | `/admin/auth-settings` | `admin` | View/edit `oidc_config` and `ldap_config` |
| ACTION | POST | `/admin/auth-settings` | `admin` | Takes effect on next `/login` load, no restart |
| PAGE | GET | `/admin/servers` | `admin` | All servers (any origin), search by hostname/description/site/env/app/role/asset_type |
| PAGE | GET | `/admin/servers/import` | `admin` | CSV upload form (hostname, ip_address, subnet, description, optional asset_type/site/env/app/role) |
| ACTION | POST | `/admin/servers/import` | `admin` | Per-row processing + per-row success/failure report; reuses subnet overlap/duplicate-IP validation |
| PAGE | GET | `/admin/maintenance-windows` | `admin` | List |
| PAGE | GET | `/admin/maintenance-windows/new` | `admin` | One-time or full RRULE builder (Alpine.js), plus rule builder (site/env/app/role) |
| FRAGMENT | POST | `/admin/maintenance-windows/preview` | `admin` | Live occurrence preview |
| FRAGMENT | POST | `/admin/maintenance-windows/rules/preview` | `admin` | Live matching-server-count preview as rule fields change |
| ACTION | POST | `/admin/maintenance-windows` | `admin` | Save |
| PAGE | GET | `/admin/maintenance-windows/{id}/edit` | `admin` | |
| ACTION | POST | `/admin/maintenance-windows/{id}` | `admin` | |
| FRAGMENT | POST | `/admin/maintenance-windows/{id}/servers` | `admin` | Add/remove attached servers (static list) |
| FRAGMENT | POST | `/admin/maintenance-windows/{id}/rules` | `admin` | Add/remove rules (site/env/app/role wildcards) |
| PAGE | GET | `/admin/api-keys` | `admin` | List (label, scope, last used, revoked) — never shows the key |
| ACTION | POST | `/admin/api-keys` | `admin` | Generates a key, shown once |
| FRAGMENT | POST | `/admin/api-keys/{id}/revoke` | `admin` | |

## Viewer Role (read-only)

| Type | Method | Path | Required Role | Description |
|---|---|---|---|---|
| PAGE | GET | `/requests`, `/admin/subnets`, `/admin/audit-log` | `viewer` | Read-only — sees ALL requests, not just own |

## Ops

| Type | Method | Path | Required Role | Description |
|---|---|---|---|---|
| ACTION | GET | `/healthz` | None | Live DB ping. No middleware at all — see Middleware Architecture below. |

## External Integration API (API key auth, not session-based)

| Type | Method | Path | Required Scope | Description |
|---|---|---|---|---|
| ACTION | GET | `/api/maintenance/blocked?date=YYYY-MM-DD` | `read` | Returns `[{hostname, description, reason}]` for servers with a window covering that date (default: today). `description` included so a human consuming the response can judge whether to override for a given server. |
| ACTION | GET | `/api/maintenance/allowed?date=YYYY-MM-DD` | `read` | Inverse of blocked |
| ACTION | POST | `/api/maintenance-windows` | `write` | Batch-create windows programmatically (e.g. an external system pushing its own computed dates). Accepts `hostnames` (static list) and/or `rules` (site/env/app/role wildcards) per window. Per-item success/failure, not all-or-nothing. References servers by hostname, not internal ID. |

This API is intentionally general-purpose — not built around any specific
vendor's deployment tool. Any integration that can make an HTTP request
with an API key can consume it.

## Middleware Architecture

- Per-route-group middleware (session load/save, CSRF, logging) is
  assembled via `middleware.MiddlewareStack` and applied to specific route
  groups (e.g. `publicMiddleware`) — **not** wrapped globally at
  `http.ListenAndServe`.
- The **only** globally-wrapped middleware is `RecoverMiddleware` (panic
  recovery) — it must apply to every route, including `/healthz`.
- `/healthz` is registered directly on the top-level `mux` with no stack.
- `/api/*` routes use a **fourth, distinct auth mechanism**: API key
  (`X-API-Key` header, hashed lookup, scope check) — not
  session/CSRF/RBAC. This is machine-to-machine auth, applied only to
  `/api/*` routes.

## Auth Provider Configuration

- OIDC and LDAP config live in `oidc_config` / `ldap_config` singleton
  tables (`CHECK id = 1`), seeded disabled. Admin-editable at runtime via
  `/admin/auth-settings` — no env vars, no redeploy needed to toggle or
  change.
- Secrets (`client_secret`, `bind_password`) are stored in **plaintext** —
  deliberate tradeoff for a self-hosted OSS tool. Litestream backups carry
  live credentials as a result — document this for self-hosters.
- `ldap_config.ca_cert` (PEM-encoded CA certificate) is **not** a secret,
  despite living in the same table as `bind_password` — a CA cert is
  public information. Used to build a custom trust pool for LDAPS
  verification when the AD/LDAP server's cert is signed by an internal
  enterprise CA (the common case for real AD); falls back to the system
  default trust store if unset.
- LDAP has **no `use_tls` column anywhere, ever** — TLS/StartTLS is
  hardcoded in the Go connection code.
- `ADMIN_PASSWORD` (local admin only) remains an env var — the one
  exception, not a pattern to extend.
- **API keys are hashed, not plaintext** — unlike the OIDC/LDAP secrets
  above, we only ever need to verify a presented key, never read it back.

## Local Admin Bootstrap

- The local admin user (`id = 1`, `administrator`) and its `admin` role
  assignment are seeded directly in the migration, not created at
  runtime — this makes the whole thing atomic (one migration transaction)
  and means `EnsureLocalAdmin` only ever reconciles the password hash,
  never creates the user or assigns the role.
- `password_hash` starts `NULL` in the seed and is set on first boot from
  `ADMIN_PASSWORD`.

## Servers and Maintenance Windows

- `servers` is a general asset registry, decoupled from the request/
  provisioning lifecycle. Populated four ways, tracked via
  `servers.source` (`request`, `manual`, `csv_import`, `admin_direct`):
  request approval sets `source = request` and `request_id`; the other
  three set `request_id` NULL — `source` is what distinguishes them from
  each other, not `request_id` alone.
- `servers.asset_type` (`server` or `virtual_ip`) distinguishes real
  machines from F5 Virtual Server VIPs — derived automatically for
  request-sourced servers, admin-chosen for the other three sources.
- `servers.site_code`/`env_code`/`app_code`/`role_code` (denormalized,
  not looked up via `requests`) classify a server for rule-based
  maintenance window matching. Copied automatically for request-sourced
  servers, optional/admin-set for the other three. `NULL` never
  wildcard-matches a rule.
- `ip_allocations.server_id` links every allocation to its server
  regardless of origin.
- `maintenance_windows` uses RFC 5545 RRULE for recurrence. `NULL` rrule =
  one-time occurrence (covers orgs whose blackout dates are computed
  externally/manually each period — e.g. payroll cycles shifting around
  federal holidays — rather than truly periodic).
- A window's effective server set is the **union** of `maintenance_window_servers`
  (static, explicit list) and `maintenance_window_rules` (site/env/app/role
  wildcards, OR'd across rules) — e.g. one rule with just `env=PRD, app=PAY`
  set matches every PRD payroll server regardless of site or role, and
  stays correct as new matching servers are provisioned later.
- **Rule matching hard-excludes `asset_type = 'virtual_ip'`** — never
  expressible or overridable via a rule's own field values; a VIP can only
  be covered by the static list.
- The `/api/maintenance/blocked` response includes each server's
  `description` specifically so a human reviewing the list (e.g. before
  running a deployment) can judge whether to override for a server that's
  technically listed but not actually critical.
- This feature is general-purpose, not built for any specific vendor/tool.

## Admin List UI Conventions

- **Pagination**: client-side (Alpine.js `x-show`) filtering is only for
  bounded, admin-sized tables (`/admin/subnets`). Unbounded/append-only
  tables (`/admin/audit-log`) require real server-side pagination and
  filtering — don't default to the subnets pattern there.
- **Semantic color**: use Oat's theme CSS variables (`var(--primary)`,
  `var(--warning)`, `var(--danger)`, `var(--success)`, `var(--muted)`,
  `var(--border)`) for any status/utilization coloring, never hardcoded
  hex/rgba — they adapt to dark mode automatically. For inline alert
  callouts, prefer `role="alert" data-variant="warning"` (Oat's native
  alert styling) over manual background/border CSS.
- **Utilization display**: a native `<meter>` can only show one
  value/color-state, so it can't represent used+reserved+free at once.
  The subnets list instead uses a custom stacked/segmented bar (flex
  divs, per-segment `width: %`, Oat theme variable backgrounds) with a
  text line underneath and no separate color legend — the bar's `title`
  tooltips plus the text line are considered self-explanatory. Reuse
  this pattern rather than `<meter>` for any future multi-state
  utilization display.

## Key Workflow Notes

See **`docs/workflows.md`** for the full step-by-step versions of these
workflows (mirrors the Eraser diagrams in plain text).

- Request form never shows a subnet or IP picker: site+env+**scheme**
  determines the subnet via admin-configured, scheme-scoped mapping; IP
  is auto-assigned (next free address in the subnet, transactional,
  skipping reserved IPs).
- `subnets.active` is a soft-retire flag: `0` blocks new allocations and
  excludes the subnet from CIDR overlap validation, but existing
  allocations against it stay valid. An inactive subnet fails an
  approval with a distinct error from subnet exhaustion.
- Naming schemes have a `naming_mode`: `generated` (Server, Network
  Device, VM) computes a fixed-length hostname (15 chars, no separators)
  from site+env+app+role dropdowns at approval time, using a per-prefix
  sequence counter (numeric `000`-`999`, then letter rollover `A00`-`Z99`,
  hard-blocked beyond `Z99`). `manual` (F5 Virtual Server) skips all of
  that — the requester provides a free-text name directly, validated for
  uniqueness at approval time instead of computed.
- Request lifecycle: `pending → approved | denied | cancelled`. Denied
  requests can be edited and resubmitted (status resets to `pending`)
  rather than creating a new request row.
- Decommissioning is a separate approval flow, not a request status. On
  approval, the IP is released back to the pool; the hostname/name (and
  its sequence number, for generated-mode) are never reused.
- Email notifications (Resend): approver notified on new submission (both
  provisioning and decommission); requester notified on approve/deny
  decision. Both decoupled from the DB transaction.
- Auth: three user-facing sources (local admin, OIDC, LDAP/AD) plus a
  fourth machine-facing mechanism (API keys, `/api/*` only). OIDC/LDAP
  config is DB-driven (see Auth Provider Configuration above).
- RBAC: roles are additive, not hierarchical (see table above).
