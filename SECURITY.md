# Security Policy

IPAM OSS is a self-hosted tool. There is no hosted service and no
vendor holding your data — you run it, you're responsible for the box
it runs on, and this document tells you what the app does and doesn't
protect against so you can make informed decisions about where you
deploy it.

## Reporting a vulnerability

Please **do not** open a public GitHub issue for a security
vulnerability. Private vulnerability reporting is enabled on this
repository — use the **Security** tab → **"Report a vulnerability"**
to open a private advisory. That's the preferred channel; it keeps the
report and any discussion out of public view until a fix is ready.
Include:

- A description of the issue and its impact
- Steps to reproduce (a minimal repro is very helpful)
- The version/commit you tested against

You should get an acknowledgment within a few days. This is a
single-maintainer open-source project run outside of work hours —
there's no SLA, but reports are taken seriously and a fix or mitigation
is the priority once triaged.

## Supported versions

Pre-1.0: only the latest commit on `main` is supported. There is no
long-term-support branch yet. Once there's a tagged 1.0 release, this
section will list which versions receive security fixes.

## Deliberate design tradeoffs (not bugs)

These are documented here so a reporter doesn't spend time on a report
we'd have to close as "working as intended" — each one is a conscious
tradeoff made for a self-hosted OSS tool, not an oversight. If you
disagree with one of these tradeoffs for your environment, the
mitigation is usually listed alongside it.

- **`oidc_config.client_secret` and `ldap_config.bind_password` are
  stored in plaintext in the database.** They're admin-editable at
  runtime and need to be read back to populate the settings form —
  there is no secrets-manager integration in v1. **Mitigation:**
  restrict filesystem/DB access to the SQLite file to the app's own
  service account, and be aware that Litestream backups (if
  configured) carry these plaintext credentials too — secure the
  backup destination (S3 bucket/NAS path) accordingly.
- **`ldap_config.ca_cert` is *not* a secret** even though it lives in
  the same table as `bind_password` — it's a PEM-encoded CA
  certificate, which is public information by design. Don't treat it
  with the same caution as the fields around it.
- **API keys (`api_keys` table) are hashed with argon2id, never
  stored in plaintext** — the opposite trust model from the OIDC/LDAP
  secrets above, since a key we issue only ever needs to be *verified*,
  never read back. A generated key is shown to the admin exactly once,
  at creation time.
- **The local administrator account (`users.id = 1`) always exists**
  and is reconciled against the `ADMIN_PASSWORD` environment variable
  on every boot — this is an intentional break-glass path, not
  something to disable, even when OIDC/LDAP is fully configured.
  Anyone with shell/env access to the container can set this password;
  scope that access accordingly.
- **LDAP always uses TLS/StartTLS** — this is hardcoded in the
  connection code and there is deliberately no way to configure a
  plaintext bind, even accidentally, from the admin settings UI.
- **Login rate limiting uses exponential backoff, not a flat lockout**,
  and is tracked per `(identifier, auth_method)`, not per source IP.
  This is deliberate: a flat lockout policy is weaponizable (an
  attacker can lock out the real account holder on purpose), and
  per-IP tracking would let one shared office NAT gateway lock out
  everyone behind it. The accepted tradeoff is that this doesn't
  block distributed username enumeration across many different
  accounts — only a specific targeted account is protected. OIDC is
  exempt from this limiter since IPAM never sees a password for that
  flow.
- **CSRF protection (`net/http.CrossOriginProtection`) only checks
  unsafe methods** (POST/PATCH/DELETE). Every state-changing route
  must be registered as POST, never a state-changing GET — `logout`,
  in particular, is `POST /logout` only. If you find a state-changing
  action reachable via GET, that's a real bug, please report it.
- **IPv6 delegation/`/64`-specific allocation logic is out of scope**
  for v1. CIDR storage and math (`net/netip`) is dual-stack-safe, but
  the app hasn't been exercised against IPv6-scale subnets.

## What's in scope for a report

- Authentication/authorization bypass (RBAC, session handling, OIDC/LDAP
  flows, API key scoping)
- CSRF, XSS, SQL injection, LDAP injection
- Privilege escalation (e.g. a `requester` reaching `approver`/`admin`
  actions)
- Any path that exposes another user's data across the RBAC boundary
- Anything that defeats the rate limiter or the CIDR overlap
  validation in a way that corrupts allocation state
- Supply-chain concerns in the dependency list (`go.mod`)

## Out of scope

- Denial of service against a self-hosted instance you run yourself
  (there's no shared infrastructure to protect)
- Attacks requiring physical or root access to the host running the
  container
- The deliberate tradeoffs listed above, unless you're identifying a
  *new* consequence of one we hadn't considered
- Missing security headers / hardening suggestions with no
  demonstrated exploit — open those as a normal GitHub issue instead,
  they're welcome, just not a "vulnerability report"

## Dependencies

This project uses `mattn/go-sqlite3` (CGo), `coreos/go-oidc`,
`go-ldap/ldap`, `alexedwards/argon2id`, and other Go modules pinned in
`go.mod`. Dependabot (or an equivalent) should be enabled on this repo
to catch upstream CVEs — if you notice it isn't, that's a fair thing to
flag as a normal issue, not a private report.
