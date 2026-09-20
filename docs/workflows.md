# Workflows

Plain-text versions of the workflows also diagrammed in Eraser (IPAM
folder). This file exists specifically so tools that can't visit external
links (like GitHub Copilot) have the same context a diagram viewer would.
Update this alongside the Eraser diagrams when a workflow changes —
treat it as documentation that ships with the code, same as `routes.md`.

## Request and Approval Workflow

1. Requester submits a request: naming scheme, site, env, then either
   app+role (generated-mode schemes) or a free-text name (manual-mode
   schemes — see "Naming Modes" below).
2. Web app looks up the subnet via `site_env_subnet_map`, keyed on
   site+env+**naming_scheme_id** (scheme-scoped, so e.g. F5 Virtual Server
   VIPs can use a different subnet than servers for the same site+env).
3. Request saved with `status = pending`. Approver is emailed (Resend).
4. Approver opens the queue, reviews, and either:

   **Approves** — one DB transaction:
   - Allocate the next available IP in the mapped subnet (skip reserved +
     already-allocated IPs).
   - **Branch by the naming scheme's `naming_mode`:**
     - `generated` (Server, Network Device, VM): lock and increment the
       `naming_sequences` row for the computed site+env+app+role prefix,
       generate the fixed-length hostname (see "Naming Sequence Generation
       Logic" below).
     - `manual` (F5 Virtual Server): no sequence involved. Validate the
       requester's free-text `manual_name` is non-empty and not already in
       use (check against `servers.hostname`), then use it directly as the
       final name.
   - Create/update the `servers` row, write to `ip_allocations`
     (`server_id` set either way), write `audit_log`.
   - Email the requester with the allocated IP and final name.

   **Denies** — sets `status = denied`, writes `audit_log`, emails the
   requester with the reason. The requester can edit and resubmit
   (`status` resets to `pending` on the same row — no new request created).

## Naming Sequence Generation Logic

*(Only runs for `naming_mode = 'generated'` schemes — manual-mode schemes
skip this entirely, see above.)*

1. Look up the scheme's active token values for site, env, app, role
   (each exactly 3 characters).
2. Concatenate them with no separators into a 12-character prefix.
3. Lock (or create) the `naming_sequences` row for `(scheme_id,
   computed_prefix)`, within the approval transaction.
4. Format `last_seq` as the 3-character sequence code:
   - `0`–`999` → zero-padded decimal (`000`–`999`)
   - `1000`–`3599` → letter + 2-digit (`A00`–`Z99`; letter increments
     every 100)
   - `> 3599` → **hard block** the approval with a clear error requiring
     manual intervention (retire old servers under that prefix, or pick a
     different role code). Never silently wrap around.
5. Increment `last_seq`, save.
6. Concatenate prefix (12 chars) + sequence (3 chars) = 15-character final
   hostname (matches the Windows NetBIOS limit). Write it to
   `requests.generated_name`.

## Naming Modes (generated vs. manual)

- `naming_schemes.naming_mode` is `'generated'` or `'manual'`.
- `generated`: name is computed from tokens (see above). `requests.app_code`
  and `role_code` are required (enforced by a DB trigger).
- `manual`: name is provided directly by the requester as free text
  (`requests.manual_name`), required and validated for uniqueness at
  approval time instead of being computed. `app_code`/`role_code` are not
  required (also enforced by the same trigger, the opposite direction).
- The request form is scheme-conditional: selecting a scheme triggers an
  htmx fragment swap (`GET /requests/new/fields?scheme_id=`) showing the
  right field set — app+role dropdowns for generated schemes, a single
  free-text name input for manual schemes. Never render both and hide one
  with CSS; swap server-side so unused fields are never submitted.
- The only manual-mode scheme currently seeded is **F5 Virtual Server**,
  for reserving IPs for F5 LTM virtual servers (VIP name + IP, no
  NetBIOS-style hostname generation). The design supports more manual-mode
  schemes later without further schema changes.

## Authentication Flow

Three concurrent, independently-configurable sources, all converging into
the same `users`/`user_roles` tables:

- **Local admin** — fixed username `administrator`. The user row (`id=1`)
  and its `admin` role are seeded directly in the migration (atomic,
  transactional) — not created at runtime. `password_hash` starts `NULL`
  and is reconciled on every boot by `EnsureLocalAdmin` against
  `ADMIN_PASSWORD` (env var always wins on restart). Every login attempt,
  success or failure, is audit-logged.
- **OIDC** — config (`issuer_url`, `client_id`, `client_secret`,
  `redirect_url`) and an `enabled` flag live in `oidc_config` (singleton
  row), admin-editable at runtime via `/admin/auth-settings`. Only shown
  on the login page if `enabled = 1`.
- **LDAP/AD** — config lives in `ldap_config`, same pattern. TLS/StartTLS
  is hardcoded in the Go connection code, never a stored/configurable
  option — there is no `use_tls` column, by design.
- OIDC and LDAP both auto-provision a `users` row on first successful
  login and auto-assign the **`viewer`** role (read-only access to all
  requests, subnets, and audit log) — chosen over no-roles-at-all to avoid
  a confusing empty dashboard on first login. This assumes the IdP/AD's
  own membership is already a meaningful access gate; an admin can grant
  additional roles (or revoke `viewer`) via `/admin/users`.
- `oidc_config.client_secret` and `ldap_config.bind_password` are stored
  in **plaintext** (deliberate tradeoff for a self-hosted OSS tool) —
  Litestream backups therefore carry live credentials, not just app data.
- Sessions: `scs` + `sqlite3store`. After login, redirect to whatever path
  was stashed before the auth redirect (`PostLoginRedirectSessionKey`),
  falling back to `/dashboard`.

## Decommission Workflow

1. Requester views an already-**approved** request's detail page, clicks
   Decommission, provides a reason.
2. Web app creates a `decommission_requests` row (`status = pending`),
   linked to the original request. At most one open decommission request
   per original request at a time (enforced by a partial unique index).
3. Approver is emailed, reviews in a **separate queue** from the
   provisioning queue (`/decommissions`, not `/approvals`).
4. **Approves** — one transaction: set `released_at` on the corresponding
   `ip_allocations` row (IP becomes reusable), write `audit_log`. **The
   hostname/name and its sequence number (if generated-mode) are never
   reused** — this applies to manual-mode names too (a released F5 VIP
   name is not implicitly available for reuse without going through the
   same uniqueness check as any other new `manual_name`). Email the
   requester.
5. **Denies** — `status = denied`, `audit_log`, email with reason. The
   original request's own status (`approved`) is unchanged — decommission
   is tracked as a separate linked record, never a new request status.

## Servers, Maintenance Windows, and the External Integration API

- `servers` is a general asset registry, decoupled from the request/
  provisioning lifecycle. Populated four ways, tracked via
  `servers.source` (`request`, `manual`, `csv_import`, `admin_direct`):
  request approval sets `source = request` plus `request_id`; the other
  three set `request_id` NULL and rely on `source` to distinguish
  themselves from each other (`request_id NULL` alone is ambiguous
  between manual entry, CSV import, and admin-direct allocation). Every
  allocation path must set `ip_allocations.server_id`.
- `maintenance_windows` uses RFC 5545 RRULE (`teambition/rrule-go`).
  `rrule` nullable — `NULL` means a one-time occurrence at `dtstart` for
  `duration_minutes` (for blackout dates computed externally/manually each
  period, e.g. payroll cycles shifting around holidays, rather than truly
  periodic). One window can cover many servers via
  `maintenance_window_servers`.
- `GET /api/maintenance/blocked` and `/allowed` (API-key auth, `read`
  scope) return `[{hostname, description, reason}]` — `description`
  included deliberately so a human reviewing the response (e.g. before
  running a deployment) can judge whether to override for a specific
  server.
- `POST /api/maintenance-windows` (`write` scope) batch-creates windows
  programmatically — e.g. an external payroll system pushing its own
  computed cycle dates. Per-item success/failure, not all-or-nothing.
  References servers by hostname, not internal ID.
- This API is general-purpose, not built around any specific vendor's
  tooling.
- `api_keys` stores a hash, never plaintext — opposite trust model from
  the OIDC/LDAP secrets above: an API key is issued by us and only ever
  verified, never read back.
