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
   - Check the mapped subnet's `active` flag first — an inactive
     (soft-retired) subnet fails the approval immediately with a distinct
     "subnet is inactive" error, separate from exhaustion, even if it
     technically still has free addresses.
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
4. **Increment `last_seq` first, then format the incremented value**
   (`internal/naming.FormatSequence`, IPAM-17) — not the other way
   around. `last_seq` defaults to `0` and is never itself issued as a
   code: the first allocation under a brand-new prefix increments it to
   `1` and formats `"001"`. `"000"` is permanently unused by design.
   - `1`–`999` → zero-padded decimal (`"001"`–`"999"`)
   - `1000`–`3599` → letter + 2-digit (`A00`–`Z99`; letter increments
     every 100)
   - `> 3599` → **hard block** the approval with a clear error
     (`naming.ErrSequenceExhausted`) requiring manual intervention
     (retire old servers under that prefix, or pick a different role
     code). Never silently wrap around.
5. Save the incremented `last_seq`.
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

## Subnet Lifecycle

`subnets.active` is a **soft-retire** flag, not a delete:
- `active = 0` blocks **new** allocations from that subnet, and excludes
  it from CIDR overlap validation entirely — this is what lets a
  legitimately retired address range be reused by a brand new subnet
  without being falsely blocked by its own retired history.
- Existing `ip_allocations` rows against an inactive subnet **remain
  valid and are unaffected** — deactivating doesn't require releasing
  anything first.
- An approval mapped to an inactive subnet fails with a distinct "subnet
  is inactive" error at allocation time, separate from **exhaustion**
  (an active subnet with genuinely no free addresses left) — same
  symptom (no allocation happens), different cause, different admin
  fix (reconfigure the mapping vs. add capacity).

## Subnet Admin UI (list + add/edit form)

- **CIDR overlap validation** happens entirely in Go
  (`internal/subnets/validate.go`: `ParseCIDR`, `ValidateNoOverlap`,
  `ValidateSubnet`), never in SQL. `ParseCIDR` masks the input
  (`netip.ParsePrefix(...).Masked()`) before comparing, so host-bit
  noise in the submitted CIDR can't cause a false negative.
  `ValidateNoOverlap` only checks against **active** subnets and
  excludes the subnet's own ID on edit. Since CIDR blocks can only be
  identical, fully-containing, or fully-disjoint (never partially
  overlapping), `netip.Prefix.Overlaps` alone is a sufficient check.
- **Deactivating a subnet**: the edit form loads
  `CountActiveAllocationsForSubnet` for that subnet and, if the admin
  unchecks "active" while that count is > 0, shows a themed warning
  (`role="alert" data-variant="warning"`) explaining that deactivating
  only blocks *new* allocations — existing ones are unaffected. This is
  advisory, not a blocking confirmation: deactivation is not destructive
  to existing data, so no extra confirm step is required.
- **List page utilization**: `ListSubnetsWithCounts` (see
  `queries.instructions.md` for the SQL aggregate gotchas behind its
  shape) returns `reserved_count`, `used_count`, and a comma-joined
  `site_envs` string per subnet.
  `subnets.ComputeUtilization(prefix, reservedCount, usedCount)`
  (`internal/subnets/utilization.go`) turns that into `capacity`,
  `used`, `reserved`, `free`:
  - `capacity = total_addresses_in_cidr - 2 (network + broadcast) - reserved_count`
  - `free = capacity - used_count`
  - both floor at 0 (a `/31` naturally computes 0 capacity; a `/32` is
    clamped to 0 — both fine, unlikely in practice).
  - This math is domain logic and lives in `internal/subnets`, not the
    views/admin package — presentation-only helpers (percent
    formatting, row styling, comma-list reformatting for display) stay
    in the views/admin package next to the templ that uses them.
- **Rendering**: shown as a custom stacked/segmented bar (allocated /
  reserved / free), not a native `<meter>` (which can only represent one
  value/color-state, not three at once), colored with Oat theme
  variables (`var(--primary)`, `var(--warning)`, `var(--muted)`), plus a
  text line underneath (`"N allocated · N reserved · P%"`). No separate
  color legend — the bar's `title` tooltips and the text line are
  self-explanatory.
- **Filtering** on the list page is client-side (Alpine.js `x-show`
  against a `data-search` attribute built from CIDR + label +
  site/envs) — acceptable because this table is admin-sized and
  bounded. See "Admin List Pagination Convention" below before copying
  this pattern onto an unbounded table.
- **Still ahead in this cycle** (not yet built): CSV export of
  allocations. Auto-assign next available IP, the site+env-to-subnet
  mapping admin UI, and reserved/excluded IP management are all done
  (see "IP Allocation Logic", "Site/Env Mapping Admin UI", and
  "Reserved/Excluded IPs Admin UI" below) — the auto-assign logic skips
  reserved IPs, and admins manage them directly at
  `/admin/subnets/{id}/reserved-ips`, no manual insert needed. Subnet
  exhaustion has a distinct sentinel error
  (`subnets.ErrSubnetExhausted`) but no caller surfaces it as a
  user-facing message yet — that's the approval flow's job, not yet
  started.

## IP Allocation Logic (Cycle 3)

- **Two-layer split, deliberately**: `internal/subnets.NextFreeIP`
  (pure — no DB dependency, takes plain `netip.Addr` slices, fully
  unit-testable) computes which address to hand out;
  `internal/allocation.Allocate` (DB-aware) loads a subnet's reserved
  and currently-allocated IPs via sqlc, calls `NextFreeIP`, and inserts
  the `ip_allocations` row. `internal/subnets` never imports
  `internal/db` — keeping that boundary is why the pure function stays
  trivially testable without a database.
- **`NextFreeIP` iterates in natural address order** (bottom-up), skips
  the network and broadcast addresses, and skips anything in the
  supplied reserved/allocated sets. `/31` and `/32` (and `/127`/`/128`
  for IPv6) always return `ErrSubnetExhausted` immediately — zero
  usable host bits means zero capacity, matching
  `ComputeUtilization`'s existing floor-at-0 behavior for the same
  prefix lengths, not a separately-bolted-on special case.
- **Two distinct sentinel errors**, both defined in `internal/subnets`
  since they're domain-level concepts:
  - `ErrSubnetInactive` — the subnet has `active = 0`. Checked first,
    before even attempting to scan for a free address — an inactive
    subnet blocks new allocations even if it technically still has
    free capacity.
  - `ErrSubnetExhausted` — every address in the usable host range is
    reserved or already allocated.
  These stay distinguishable all the way up (never collapsed into one
  generic "allocation failed" error) because the admin's fix is
  different for each: reconfigure the site+env mapping vs. add
  capacity.
- **`internal/allocation.Allocate` does not open its own transaction.**
  It's designed to be one step inside a larger `db.WithTx` call — the
  future approval flow's job is: check the mapped subnet is active
  (via `Allocate`'s own subnet lookup) → allocate the IP → lock and
  increment the naming sequence → write the audit log entry, all in
  one transaction, all-or-nothing. Calling `Allocate` outside of an
  already-open transaction would still work today (nothing enforces
  it), but defeats the point.
- **Concurrency safety comes from `_txlock=immediate` on the SQLite
  DSN, not from application-level locking.** SQLite only ever has one
  writer; the DSN change makes every `db.WithTx` transaction grab the
  write lock at `BEGIN` rather than lazily on the first write
  statement. Without it, two concurrent approval transactions could
  both scan and compute the *same* "next free IP" before either
  writes, and only the second `INSERT` would fail (against the
  existing `ip_allocations_active_unique` partial index) — correct,
  but a foreseeable failure requiring a retry loop. With the DSN
  change, the second transaction simply blocks until the first
  commits, so no retry logic is needed anywhere. This applies to every
  existing `db.WithTx` caller automatically (local admin bootstrap,
  OIDC/LDAP provisioning), not just IP allocation.
- **No caller is wired up yet** — `internal/allocation.Allocate` has no
  route, no controller, no UI. It's built and unit-tested in isolation
  (matching the split above), ready to be called from the approval
  transaction once the "Request and Approval Workflow" module starts.
  Don't build a temporary admin-direct trigger for it speculatively —
  that module will need its own design pass when it's picked up.

## Reserved/Excluded IPs Admin UI (IPAM-39)

- **`/admin/subnets/{id}/reserved-ips`** manages `subnet_reserved_ips`
  rows for one subnet — own page (list + add form + per-row delete,
  `ReservedIPController`), not an htmx fragment on the subnet edit
  form. Weighed against the fragment pattern used for naming-scheme
  token values and maintenance-window servers/rules, and a full page
  was chosen deliberately for this feature (UX reasons); linked from
  the subnet edit page.
- **No schema change was needed to support "a small range."**
  `subnet_reserved_ips` already stores one `ip_address` per row
  (`UNIQUE(subnet_id, ip_address)`). The add form takes a single IP or
  a start–end range; a range is expanded server-side
  (`subnets.ExpandReserveRange`, pure domain math in
  `internal/subnets`, same DB-agnostic package as `NextFreeIP`) into
  individual addresses before insert. Don't add `ip_start`/`ip_end`
  columns — the row-per-address shape matches how `NextFreeIP` already
  consumes this table (a flat address set, not a range test).
- **`ExpandReserveRange` validates**: start/end parse, same address
  family, both addresses fall inside the subnet's own CIDR
  (`prefix.Contains`), `start <= end` (via `netip.Addr.Compare`), and
  the expanded range is capped at `subnets.MaxReserveRangeSize` (256
  addresses) — the enforced definition of the work item's "a small
  range" language, not left to admin judgment.
- **A range insert is one `db.WithTx` transaction, not per-item partial
  success** — reserving a range is one coherent admin action (unlike
  CSV import or the maintenance-window batch API, which are
  intentionally per-item). A `UNIQUE` violation on any address in the
  range rejects the whole submission; zero rows are inserted, never a
  partially-reserved range. Same inline-error UX as the subnet/site-
  env-map form conflicts — re-render with an error, no redirect.
- **List ordering: `ORDER BY id`, not `created_at`.** All rows from one
  range submission are inserted in the same transaction, and SQLite's
  `datetime('now')` only has second resolution, so they tie on
  `created_at`. Ties fall back to the `(subnet_id, ip_address)` unique
  index's own order, which is a **lexicographic string sort on
  `ip_address`** (`"10.1.11.10"` sorts before `"10.1.11.5"`) — not
  numeric, and not insertion order. `id` (the autoincrement PK) is
  insertion order, which is address order (`ExpandReserveRange` returns
  addresses ascending, inserted in that order) — this was an actual bug
  caught during testing, not a hypothetical.
- **Reserving doesn't check current allocation status** — an admin can
  reserve an address that's currently actively allocated. Harmless: it
  just prevents that address from being handed out again once it's
  eventually released. Considered and deliberately not blocked.

## Allocation CSV Export (IPAM-41)

- **One route, `GET /admin/allocations/export.csv`, not two.** No
  `subnet_id` query param means system-wide; `?subnet_id=` scopes to
  one subnet. Two separate sqlc queries back it
  (`ListActiveAllocationsForExport` / `ListActiveAllocationsForSubnetExport`)
  since the codebase's pattern is a dedicated named query per shape
  rather than one dynamic/optional `WHERE`, but they share one HTTP
  route and one handler that dispatches on whether the query param is
  present.
- **Exports `ip_allocations`, not `subnets` or `subnet_reserved_ips`.**
  Worth stating plainly since it caused real confusion during
  development: this feature has nothing to do with reserved/excluded
  IPs (IPAM-39) or subnet configuration — it's a point-in-time dump of
  addresses that have actually been *allocated* (via request approval,
  admin-direct allocation, or CSV import). `subnets` is only
  `JOIN`ed in for the CIDR label column.
- **Active-only, no `include_released` toggle in v1** — matches the
  work item's acceptance criteria exactly. A historical/released-rows
  export is a distinct future feature if actually needed, not folded
  into this one speculatively.
- **`ORDER BY ... ia.id`, not `ia.ip_address`** — same
  lexicographic-string-sort trap as `ListSubnetReservedIPsForAdmin`
  (IPAM-39): `ip_address` is `TEXT`, so sorting on it directly produces
  wrong ordering. Applied here from the start rather than being
  discovered as a second bug.
- **Source column added beyond the work item's literal column list**
  (IP, hostname, subnet, requester, allocated date, status) — `source`
  (`request`/`manual`/`csv_import`/`admin_direct`) is included because
  `requester` is only meaningful for `source = 'request'` rows; the
  other three sources leave `Requester` blank, and `Source` is what
  explains why, rather than leaving an unexplained empty column.
- **`Status` is a hardcoded `"Active"` constant** for every row in v1 —
  not derived from anything, since only active (non-released)
  allocations are ever included. It exists as a column now so a future
  `include_released` addition doesn't require a CSV schema change,
  just populating it with "Released" for those rows.
- **Per-subnet export link lives on the `/admin/subnets` list page,
  per-row — not on the subnet edit form.** Initially placed on the
  edit form (next to the Reserved IPs link), then deliberately moved:
  an export isn't an edit action, and a per-row link on the list lets
  an admin export a subnet's allocations without detouring through
  Edit first. Don't put per-subnet export actions on edit/detail forms
  by default — list-page per-row actions are the better fit unless
  there's a specific reason to co-locate with editing (as Reserved IPs
  legitimately is, since managing reservations is itself a
  configuration action).

## Site/Env Mapping Admin UI (Cycle 3)

- **`/admin/site-env-map`** manages `site_env_subnet_map` rows — one
  row per `(naming_scheme_id, site_code, env_code)` → `subnet_id`.
  Schema already existed (scoped per `naming_scheme_id` from the
  start); this cycle only added the admin UI on top of it.
- **Site/env are not a global list** — they're
  `naming_scheme_token_values` rows (`token = 'site'`/`'env'`), scoped
  per naming scheme, same source the request form's app/role dropdowns
  draw from. So the mapping form has a cascading dependency: pick a
  scheme → site/env options narrow to that scheme's *active* token
  values.
- **The cascade is client-side (Alpine), not an htmx round-trip.**
  Unlike the request form's `GET /requests/new/fields?scheme_id=`
  fragment swap (which exists because the generated/manual field *set*
  genuinely differs), all schemes' active site/env token values are
  small enough to embed once as JSON when the form loads
  (`SchemeTokensJSON`, keyed by `schemeID` → `{"site": [...], "env":
  [...]}`) and filtered in the browser as the scheme select changes.
  Don't add a server round-trip here — the dataset doesn't warrant it.
- **The subnet dropdown offers active subnets only**
  (`ListActiveSubnets`) — same rule as allocation: an inactive subnet
  is never a valid target for a new or edited mapping.
- **Uniqueness conflict UX matches the subnet form**: a
  `(site_code, env_code, naming_scheme_id)` collision re-renders the
  form inline with an error, no redirect — never let the raw SQLite
  constraint error reach the user.
- **List page** shows a Free-addresses column per mapping, computed
  the same way as the subnets list
  (`subnets.ComputeUtilization(prefix, reservedCount, usedCount).Free`,
  fed by `ListSiteEnvSubnetMapWithDetails`'s per-row
  `reserved_count`/`used_count` — see `queries.instructions.md` for the
  query). Client-side search, since this is a bounded/admin-configured
  table (see "Admin List Pagination Convention" below).
- **Still not built**: the request-submission-time check that blocks
  submitting a request for a site+env+scheme combo with no mapping row
  ("no subnet configured for this site/environment/scheme — contact an
  admin") — that lives in the request flow (not yet built), not here.
  This item only covers managing the mappings themselves.

## Admin List Pagination Convention

Whether an admin list page needs real pagination depends on whether the
underlying table is bounded or unbounded:

- **Bounded / admin-configured** tables (subnets, naming schemes, site+env
  mappings, users) — the row count is inherently small and grows only as
  fast as an admin manually adds rows. Client-side Alpine.js `x-show`
  filtering over the full rendered list is fine; there's no realistic
  row count where this becomes a performance problem.
- **Unbounded / append-only** tables (`audit_log`, and any future
  history/log-style table) — row count grows continuously and
  unboundedly with usage. These require real server-side pagination and
  filtering (query-param-driven `LIMIT`/`OFFSET` or keyset pagination,
  plus server-side filter params) — never ship one of these with
  client-side-only filtering over an unbounded result set. This is why
  `/admin/audit-log` is tracked as its own work item rather than reusing
  the subnets list's approach.

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
- **Login rate limiting** (local admin and LDAP only, not OIDC): before
  attempting the password/bind check, `CheckLoginLockout` looks up
  `login_attempts` by `(identifier, auth_method)` — `identifier` is
  `"administrator"` for local admin, the submitted username for LDAP. If
  currently locked, the login is rejected immediately, without ever
  touching argon2 or opening an LDAP connection. On failure,
  `RecordLoginFailure` increments the count and computes the next lockout
  window via exponential backoff (no penalty for the first 2 failures,
  then doubling from 5s, capped at 5 minutes) — not a flat lockout, which
  would be weaponizable against the real account holder. On success,
  `ResetLoginAttempts` clears the row. Tracked per identity rather than
  per source IP, to avoid locking out a shared office NAT gateway — the
  tradeoff is that distributed username enumeration across many fake
  identifiers isn't blocked, only a specific targeted account is
  protected.

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
- `servers.asset_type` (`server` or `virtual_ip`) distinguishes real,
  patchable machines from F5 Virtual Server VIPs. For `source = request`
  rows, derived automatically from the originating naming scheme's
  `naming_mode` (`generated` → `server`, `manual` → `virtual_ip`). For
  the other three sources, an admin sets it explicitly (default `server`).
- `servers.site_code`/`env_code`/`app_code`/`role_code` are denormalized
  onto `servers` itself (not looked up via `requests`), so rule-based
  maintenance window matching (below) works identically regardless of a
  server's origin. For `source = request` rows, copied automatically from
  the approved request. For the other three sources, optional — an admin
  can tag a legacy server to make it rule-eligible. `NULL` on any of
  these fields never wildcard-matches a rule; an untagged server can only
  be covered by a window's static list.
- `maintenance_windows` uses RFC 5545 RRULE (`teambition/rrule-go`).
  `rrule` nullable — `NULL` means a one-time occurrence at `dtstart` for
  `duration_minutes` (for blackout dates computed externally/manually each
  period, e.g. payroll cycles shifting around holidays, rather than truly
  periodic).
- **A window's effective server set is the UNION of two mechanisms:**
  1. `maintenance_window_servers` — static, explicit, hand-picked list.
  2. `maintenance_window_rules` — rule-based membership. Each rule has
     `site_code`/`env_code`/`app_code`/`role_code`, all nullable
     (`NULL` = wildcard, matches any value for that field). Multiple
     rules on one window are OR'd. Example: `env_code = 'PRD'`,
     `app_code = 'PAY'`, site and role left NULL → matches every PRD
     payroll server, any site, any role — and stays correct as new
     matching servers are provisioned later, unlike a static list which
     needs manual upkeep every time a new matching server appears.
  - **Rule matching hard-excludes `asset_type = 'virtual_ip'`** — a
    `WHERE` clause in the matching query, not conditional logic a rule's
    field values could bypass. An F5 VIP is never patchable and must
    never be swept into a rule-based window regardless of what
    site/env/app/role it happens to carry. It can still be added to a
    window's static list explicitly, just never matched by a rule.
- `GET /api/maintenance/blocked` and `/allowed` (API-key auth, `read`
  scope) return `[{hostname, description, reason}]` — `description`
  included deliberately so a human reviewing the response (e.g. before
  running a deployment) can judge whether to override for a specific
  server.
- `POST /api/maintenance-windows` (`write` scope) batch-creates windows
  programmatically — e.g. an external payroll system pushing its own
  computed cycle dates, along with a rule like `{env: "PRD", app: "PAY"}`
  instead of a human re-selecting every matching server by hand. Accepts
  both `hostnames` (static list) and `rules` arrays. Per-item
  success/failure, not all-or-nothing. References servers by hostname,
  not internal ID.
- This API is general-purpose, not built around any specific vendor's
  tooling.
- `api_keys` stores a hash, never plaintext — opposite trust model from
  the OIDC/LDAP secrets above: an API key is issued by us and only ever
  verified, never read back.
