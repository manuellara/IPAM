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
| PAGE | GET | `/` | Any | Dashboard, composed from sections based on the user's assigned roles |
| PAGE | GET | `/requests` | `requester`, `viewer`, or `admin` | Requester sees own requests; viewer/admin see all (read-only for viewer) |
| PAGE | GET | `/requests/new` | `requester` | Submission form (scheme → site → env → app → role; no subnet/IP picker) |
| ACTION | POST | `/requests` | `requester` | Redirects to `/requests/:id` on success |
| PAGE | GET | `/requests/:id` | `requester` (own), `viewer`, or `admin` | Detail view with status timeline; shows Decommission action once approved |
| PAGE | GET | `/requests/:id/edit` | `requester` (own) | Edit form (pending: correcting a mistake; denied: resubmission) |
| ACTION | POST | `/requests/:id` | `requester` (own) | Saves edit / resubmits (denied → pending) |
| FRAGMENT | POST | `/requests/:id/cancel` | `requester` (own) | Only allowed while pending |
| FRAGMENT | POST | `/requests/:id/decommission` | `requester` (own) | Creates a pending `decommission_request` linked to this request |

## Approver Role

| Type | Method | Path | Required Role | Description |
|---|---|---|---|---|
| PAGE | GET | `/approvals` | `approver` | Pending provisioning queue |
| PAGE | GET | `/approvals/:id` | `approver` | Detail with approve/deny actions |
| FRAGMENT | POST | `/approvals/:id/approve` | `approver` | Atomic transaction: allocate IP, increment sequence, generate name, audit log, email requester |
| FRAGMENT | POST | `/approvals/:id/deny` | `approver` | Emails requester |
| PAGE | GET | `/decommissions` | `approver` | Pending decommission queue |
| PAGE | GET | `/decommissions/:id` | `approver` | Detail with approve/deny actions |
| FRAGMENT | POST | `/decommissions/:id/approve` | `approver` | Releases the IP back to the pool (hostname/sequence never reused) |
| FRAGMENT | POST | `/decommissions/:id/deny` | `approver` | Emails requester with reason |

## Admin Role

| Type | Method | Path | Required Role | Description |
|---|---|---|---|---|
| PAGE | GET | `/admin/subnets` | `admin` (`viewer`: read-only) | List, with search/filter |
| PAGE | GET | `/admin/subnets/new` | `admin` | |
| ACTION | POST | `/admin/subnets` | `admin` | Overlap validation enforced |
| PAGE | GET | `/admin/subnets/:id/edit` | `admin` | |
| ACTION | POST | `/admin/subnets/:id` | `admin` | |
| PAGE | GET | `/admin/site-env-map` | `admin` | Site+env → subnet mappings |
| PAGE | GET | `/admin/site-env-map/new` | `admin` | |
| ACTION | POST | `/admin/site-env-map` | `admin` | |
| PAGE | GET | `/admin/naming-schemes` | `admin` | The 3 schemes (Server, Network Device, VM) |
| PAGE | GET | `/admin/naming-schemes/:id` | `admin` | Scheme detail + token value tables |
| PAGE | GET | `/admin/naming-schemes/:id/tokens/new` | `admin` | |
| FRAGMENT | POST | `/admin/naming-schemes/:id/tokens` | `admin` | Exact 3-char code length enforced |
| PAGE | GET | `/admin/users` | `admin` | List, assign roles |
| FRAGMENT | POST | `/admin/users/:id/roles` | `admin` | |
| PAGE | GET | `/admin/audit-log` | `admin` (`viewer`: read-only) | |
| ACTION | POST | `/admin/allocations` | `admin` | Admin-direct allocation; also creates a `servers` row |
| PAGE | GET | `/admin/allocations/export.csv` | `admin` | Current (non-released) allocations |
| PAGE | GET | `/admin/auth-settings` | `admin` | View/edit `oidc_config` and `ldap_config` |
| ACTION | POST | `/admin/auth-settings` | `admin` | Takes effect on next `/login` load, no restart |
| PAGE | GET | `/admin/servers` | `admin` | All servers (any origin), search by hostname/description |
| PAGE | GET | `/admin/servers/import` | `admin` | CSV upload form (hostname, ip_address, subnet, description) |
| ACTION | POST | `/admin/servers/import` | `admin` | Per-row processing + per-row success/failure report; reuses subnet overlap/duplicate-IP validation |
| PAGE | GET | `/admin/maintenance-windows` | `admin` | List |
| PAGE | GET | `/admin/maintenance-windows/new` | `admin` | One-time or full RRULE builder (Alpine.js) |
| FRAGMENT | POST | `/admin/maintenance-windows/preview` | `admin` | Live occurrence preview |
| ACTION | POST | `/admin/maintenance-windows` | `admin` | Save |
| PAGE | GET | `/admin/maintenance-windows/:id/edit` | `admin` | |
| ACTION | POST | `/admin/maintenance-windows/:id` | `admin` | |
| FRAGMENT | POST | `/admin/maintenance-windows/:id/servers` | `admin` | Add/remove attached servers |
| PAGE | GET | `/admin/api-keys` | `admin` | List (label, scope, last used, revoked) — never shows the key |
| ACTION | POST | `/admin/api-keys` | `admin` | Generates a key, shown once |
| FRAGMENT | POST | `/admin/api-keys/:id/revoke` | `admin` | |

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
| ACTION | POST | `/api/maintenance-windows` | `write` | Batch-create windows programmatically (e.g. an external system pushing its own computed dates). Per-item success/failure, not all-or-nothing. References servers by hostname, not internal ID. |

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
- LDAP has **no `use_tls` column anywhere, ever** — TLS/StartTLS is
  hardcoded in the Go connection code.
- `ADMIN_PASSWORD` (local admin only) remains an env var — the one
  exception, not a pattern to extend.
- **API keys are hashed, not plaintext** — unlike the OIDC/LDAP secrets
  above, we only ever need to verify a presented key, never read it back.
  See Servers and Maintenance Windows below.

## Servers and Maintenance Windows

- `servers` is a general asset registry, decoupled from the request/
  provisioning lifecycle. Populated three ways: request approval
  (`request_id` set), manual admin entry, or CSV bulk import (both
  `request_id` NULL). Admin-direct allocation also creates a `servers` row.
- `ip_allocations.server_id` links every allocation to its server
  regardless of origin.
- `maintenance_windows` uses RFC 5545 RRULE for recurrence. `NULL` rrule =
  one-time occurrence (covers orgs whose blackout dates are computed
  externally/manually each period — e.g. payroll cycles shifting around
  federal holidays — rather than truly periodic).
- One window can cover many servers via `maintenance_window_servers`. The
  `/api/maintenance/blocked` response includes each server's `description`
  specifically so a human reviewing the list (e.g. before running a
  deployment) can judge whether to override for a server that's technically
  listed but not actually critical.
- This feature is general-purpose, not built for any specific vendor/tool.

## Key Workflow Notes

- Request form never shows a subnet or IP picker: site+env determines the
  subnet via admin-configured mapping; IP is auto-assigned (next free
  address in the subnet, transactional, skipping reserved IPs).
- Naming: site+env+app+role dropdowns (validated against active token
  values) generate a fixed-length hostname (15 chars, no separators) at
  approval time, using a per-prefix sequence counter (numeric `000`-`999`,
  then letter rollover `A00`-`Z99`, hard-blocked beyond `Z99`).
- Request lifecycle: `pending → approved | denied | cancelled`. Denied
  requests can be edited and resubmitted (status resets to `pending`)
  rather than creating a new request row.
- Decommissioning is a separate approval flow, not a request status. On
  approval, the IP is released back to the pool; the hostname and its
  sequence number are never reused.
- Email notifications (Resend): approver notified on new submission (both
  provisioning and decommission); requester notified on approve/deny
  decision. Both decoupled from the DB transaction.
- Auth: three user-facing sources (local admin, OIDC, LDAP/AD) plus a
  fourth machine-facing mechanism (API keys, `/api/*` only). OIDC/LDAP
  config is DB-driven (see Auth Provider Configuration above).
- RBAC: roles are additive, not hierarchical (see table above).
