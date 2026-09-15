---
applyTo: "**/*.templ,internal/web/**"
---

# Web layer conventions (templ + htmx)

See `docs/routes.md` for the full, current list of routes and their
classifications — check it before adding a new route so the doc and the
code don't drift.

- Every handler is classified PAGE, FRAGMENT, or ACTION (see repo-wide
  instructions). Pick the classification before writing the handler —
  it determines the response shape:
  - PAGE: full document render, normal GET.
  - FRAGMENT: htmx request (`HX-Request` header present), return just the
    swapped partial. Used for approve/deny, cancel, decommission-approve,
    role-toggle — fast, low-risk, in-place actions.
  - ACTION: POST that validates a multi-field form. On success, redirect
    (303) to the resulting page. On validation failure, re-render the full
    form with inline errors — don't return a bare error fragment for
    multi-field forms.
- CSRF: all state-changing routes go through `net/http.CrossOriginProtection`.
  Don't add a separate token-based CSRF middleware — it's redundant with
  this and not the chosen approach. It only checks unsafe methods
  (POST/PATCH/DELETE) — GET routes, including the OIDC callback, are never
  checked, so no bypass pattern is needed there by default (see repo-wide
  instructions for the `form_post` exception).
- Middleware is assembled per-route-group via `middleware.MiddlewareStack`,
  not wrapped globally — see the repo-wide "Middleware architecture"
  section. `/healthz` specifically has no stack at all; don't add session,
  CSRF, or logging middleware to it even as a "just in case."
- Request submission forms never include a subnet or IP picker. If you're
  writing a form that touches `requests`, the only user-facing fields are:
  naming scheme, site, env, app, role. Subnet and IP are resolved
  server-side.
- Dashboard sections (`GET /`) are composed per the logged-in user's roles,
  not per a single "current role" — a user with multiple roles sees
  multiple sections stacked. Don't gate the dashboard behind a single role
  switch.
- Admin token-value edits (naming scheme codes) should deactivate, not hard
  delete — historical requests reference these codes and must keep
  rendering correctly.
