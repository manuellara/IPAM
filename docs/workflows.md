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
  on the login page if `enabled = 1`. Full flow:
  1. `GET /auth/oidc/login` — build the `oidc.Provider`/`oauth2.Config`
     fresh from the current DB row (rebuilt per attempt, not cached —
     the config can change at runtime via `/admin/auth-settings`, and a
     `.well-known` fetch per login is cheap at this traffic scale).
     Generate a random `state` and `nonce` (`crypto/rand`), store both in
     the session, redirect to the IdP's authorization URL.
  2. `GET /auth/oidc/callback` — verify `state` with a **constant-time**
     comparison (`crypto/subtle`) against what was stored; check for an
     `error` query param (user cancelled at the IdP); exchange the code
     for tokens; verify the ID token's signature and `nonce` claim;
     extract `sub` (and `name`/`preferred_username`/`email` for display).
  3. Look up `users` by `oidc_subject`. If not found: **create the user
     and assign the `viewer` role inside a single DB transaction**
     (`db.WithTx` helper — see below) — a mid-write failure here must
     never leave a user provisioned with no role, same bug class as the
     local admin bootstrap, fixed the same way (atomicity), just applied
     at runtime since OIDC users are created on demand rather than seeded.
  4. Renew the session token, set the authenticated principal, redirect
     to wherever `redirectAfterLogin` resolves.
  - Every outcome (invalid state, IdP error, provider unreachable, token
    verification failure, provisioning failure, role lookup failure,
    session renewal failure, success) is audit-logged — not just success.
- **LDAP/AD** — config lives in `ldap_config`. TLS/StartTLS is hardcoded
  in the Go connection code, never a stored/configurable option — there
  is no `use_tls` column, by design. Full flow (search-then-bind):
  1. Connect over implicit TLS (`ldaps://`) to `server:port`.
  2. Build the trust pool from `ldap_config.ca_cert` if set (a
     PEM-encoded internal/enterprise CA cert — common for real AD, which
     is almost never signed by a publicly trusted CA); fall back to the
     system default trust store if `ca_cert` is `NULL`.
  3. Bind as the service account (`bind_dn`/`bind_password`).
  4. Search `base_dn` using `user_filter` (e.g. `(uid=%s)` or
     `(sAMAccountName=%s)` for AD) with the submitted username
     **escaped** via `ldap.EscapeFilter` — never interpolate the raw
     username into the filter string, to prevent LDAP injection.
  5. Re-bind as the found entry's DN with the submitted password to
     verify it.
  6. Look up `users` by `ldap_dn` (the entry's DN, not the `uid`/
     `sAMAccountName` used in step 4 — that value only locates the entry,
     it isn't the stable local identifier). If not found: create the user
     and assign `viewer` inside a single transaction (`db.WithTx`), same
     atomicity requirement as OIDC provisioning.
  7. Renew session, set principal, redirect.
  - Every outcome is audit-logged, same as OIDC.
  - `ca_cert` is NOT treated as a secret (a CA cert is public information)
    — unlike `bind_password` in the same table.
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

## Atomic Multi-Step Writes (`db.WithTx`)

Any write that spans more than one statement where a partial failure
would leave the DB in a broken/inconsistent state must run inside a real
transaction — not just sequential calls hoping nothing fails in between.
This bit us once already (the local admin user existing with no role
assigned, before that seed moved into the migration's own transaction)
and would bite again for any runtime multi-step write without it.

```go
// internal/db/tx.go
func WithTx(ctx context.Context, sqlDB *sql.DB, fn func(*Queries) error) error {
	tx, err := sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	q := New(tx)
	if err := fn(q); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}
```

Callers need both `*db.Queries` (for normal reads) and the raw `*sql.DB`
(to open transactions) — controllers that need this hold both.

**Current users of this pattern:**
- OIDC user provisioning (create user + assign `viewer` role) — see
  Authentication Flow above.

**Future users of this pattern (not yet built):**
- The approval transaction (allocate IP, increment naming sequence,
  generate/validate name, create/update `servers` row, update the
  request, write `audit_log`) — this was always described as "one DB
  transaction" throughout this doc; `db.WithTx` is the mechanism that
  makes that literally true rather than just a description.
- LDAP user provisioning, once built (same shape as OIDC).
- The CSV server import and the batch maintenance-window API endpoint use
  a related idea — per-item processing with per-item success/failure —
  but are NOT single transactions (a bad row shouldn't roll back the good
  ones). Don't reach for `db.WithTx` there; it's the wrong tool for
  "partial success is fine," only the right tool for "all or nothing."

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
