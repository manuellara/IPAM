# Routes and Endpoints

This is the canonical, code-adjacent reference for every route in the app.
Update this file when a route is added, removed, or changes shape —
treat it as documentation that ships with the code, not a planning
scratchpad.

**Type key:**
- **PAGE** — full HTML document, GET, hit via browser navigation
- **FRAGMENT** — htmx-triggered, returns a partial HTML swap, no full
  navigation. Used for fast, low-risk, in-place actions.
- **ACTION** — POST that mutates and redirects (classic POST/redirect/GET).
  Used for multi-field forms where a full re-render with validation errors
  is clearer than a fragment swap.

## RBAC

Roles are **additive, not hierarchical** — a user needs each role
explicitly assigned (e.g. an admin who also approves needs the `approver`
role too, it isn't implied by `admin`).

| Role | Can do |
|---|---|
| `requester` | Submit requests; view/edit/cancel their own pending requests; edit/resubmit their own denied requests; initiate decommission on their own approved requests |
| `approver` | View and act on the provisioning queue (approve/deny any pending request); view and act on the decommission queue |
| `admin` | Manage subnets, site+env mappings, naming schemes/token values, user role assignments, audit log; admin-direct allocations; CSV export |
| `viewer` | Read-only visibility into **all** requests (not just their own), subnets, and audit log — no write access anywhere |

In the tables below, **Any** means any authenticated user regardless of
role, and **None** means no auth required.

## Public / Unauthenticated

| Type | Method | Path | Required Role | Description |
|---|---|---|---|---|
| PAGE | GET | `/login` | None | SSO button (if OIDC configured), username/password form (if LDAP configured), link to local admin login |
| PAGE | GET | `/login/local` | None | Local admin login form |
| ACTION | POST | `/login/local` | None | Redirects to `/` on success, re-renders form with error on failure |
| ACTION | GET | `/auth/oidc/callback` | None | Redirect only, no body |
| ACTION | POST | `/login/ldap` | None | Redirects to `/` on success, re-renders form with error on failure |
| ACTION | POST | `/logout` | Any | Redirects to `/login` |

## All Authenticated Users

| Type | Method | Path | Required Role | Description |
|---|---|---|---|---|
| PAGE | GET | `/` | Any | Dashboard, composed from sections based on the user's assigned roles |
| PAGE | GET | `/requests` | `requester`, `viewer`, or `admin` | Requester sees own requests; viewer/admin see all (read-only for viewer) |
| PAGE | GET | `/requests/new` | `requester` | Submission form (scheme → site → env → app → role; no subnet/IP picker) |
| ACTION | POST | `/requests` | `requester` | Redirects to `/requests/:id` on success, re-renders form with errors on failure |
| PAGE | GET | `/requests/:id` | `requester` (own), `viewer`, or `admin` | Detail view with status timeline; shows Decommission action once approved |
| PAGE | GET | `/requests/:id/edit` | `requester` (own) | Edit form (pending: correcting a mistake; denied: resubmission) |
| ACTION | POST | `/requests/:id` | `requester` (own) | Saves edit / resubmits (denied → pending), redirects to `/requests/:id` |
| FRAGMENT | POST | `/requests/:id/cancel` | `requester` (own) | Swaps status badge/actions in place, only allowed while pending |
| FRAGMENT | POST | `/requests/:id/decommission` | `requester` (own) | Creates a pending `decommission_request` linked to this request |

## Approver Role

| Type | Method | Path | Required Role | Description |
|---|---|---|---|---|
| PAGE | GET | `/approvals` | `approver` | Pending provisioning queue |
| PAGE | GET | `/approvals/:id` | `approver` | Detail with approve/deny actions |
| FRAGMENT | POST | `/approvals/:id/approve` | `approver` | Atomic transaction: allocate IP, increment sequence, generate name, audit log, email requester |
| FRAGMENT | POST | `/approvals/:id/deny` | `approver` | Swaps row/detail to denied state; emails requester |
| PAGE | GET | `/decommissions` | `approver` | Pending decommission queue (separate from provisioning queue) |
| PAGE | GET | `/decommissions/:id` | `approver` | Detail with approve/deny actions |
| FRAGMENT | POST | `/decommissions/:id/approve` | `approver` | Releases the IP back to the subnet pool (hostname/sequence never reused); audit log; emails requester |
| FRAGMENT | POST | `/decommissions/:id/deny` | `approver` | Swaps row/detail to denied state; emails requester with reason |

## Admin Role

| Type | Method | Path | Required Role | Description |
|---|---|---|---|---|
| PAGE | GET | `/admin/subnets` | `admin` (`viewer`: read-only) | List, with search/filter |
| PAGE | GET | `/admin/subnets/new` | `admin` | |
| ACTION | POST | `/admin/subnets` | `admin` | Overlap validation enforced |
| PAGE | GET | `/admin/subnets/:id/edit` | `admin` | |
| ACTION | POST | `/admin/subnets/:id` | `admin` | Same overlap validation |
| PAGE | GET | `/admin/site-env-map` | `admin` | List of site+env → subnet mappings |
| PAGE | GET | `/admin/site-env-map/new` | `admin` | |
| ACTION | POST | `/admin/site-env-map` | `admin` | |
| PAGE | GET | `/admin/naming-schemes` | `admin` | List of the 3 schemes (Server, Network Device, VM) |
| PAGE | GET | `/admin/naming-schemes/:id` | `admin` | Scheme detail + its token value tables |
| PAGE | GET | `/admin/naming-schemes/:id/tokens/new` | `admin` | |
| FRAGMENT | POST | `/admin/naming-schemes/:id/tokens` | `admin` | Appends new token row in place; enforces exact 3-char code length |
| PAGE | GET | `/admin/users` | `admin` | List, assign roles |
| FRAGMENT | POST | `/admin/users/:id/roles` | `admin` | Swaps that user's role-badge cell in place |
| PAGE | GET | `/admin/audit-log` | `admin` (`viewer`: read-only) | Searchable/filterable audit trail |
| ACTION | POST | `/admin/allocations` | `admin` | Admin-direct allocation, bypasses request/approval flow |
| PAGE | GET | `/admin/allocations/export.csv` | `admin` | CSV export of current (non-released) allocations |

## Viewer Role (read-only)

| Type | Method | Path | Required Role | Description |
|---|---|---|---|---|
| PAGE | GET | `/requests`, `/admin/subnets`, `/admin/audit-log` | `viewer` | Same pages as above, read-only — sees ALL requests, not just own |

## Ops

| Type | Method | Path | Required Role | Description |
|---|---|---|---|---|
| ACTION | GET | `/healthz` | None | Reports DB/migration readiness for container orchestration |

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
- Auth: three independent, concurrently-supported sources — local admin
  (always available, break-glass), OIDC, LDAP/AD (`go-ldap/ldap`,
  TLS/StartTLS enforced). All map into the same `users`/`roles` tables.
- RBAC: roles are additive, not hierarchical (see table above).
