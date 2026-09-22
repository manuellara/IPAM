# IPAM

IPAM is a self-hosted IP address management application for teams that need a
repeatable way to request, approve, allocate, and decommission infrastructure
addresses. It is intended to replace per-subnet spreadsheets with a workflow
that also generates standardized hostnames.

The project is currently under active development. The repository contains the
database schema, embedded migrations, sqlc data-access layer, SQLite-backed
sessions, middleware building blocks, and the initial HTTP server wiring. The
current server exposes the local login/logout flow and `/healthz`; the broader
request, approval, administration, and integration workflows are documented in
[`docs/routes.md`](docs/routes.md) and are being implemented incrementally.

## Features and design

- SQLite database with embedded, automatic migrations, WAL mode, foreign-key
  enforcement, and a busy timeout applied through the connection DSN.
- SQL queries compiled to Go with [sqlc](https://sqlc.dev/).
- SQLite-backed sessions using `scs`, with a 12-hour lifetime and a 30-minute
  idle timeout.
- Local administrator authentication using Argon2id password hashes and
  audit-logged login/logout events.
- Request and approval workflow for IP allocation.
- Site and environment mappings so requesters do not choose IPs directly.
- Reserved-address support and transactional allocation of the next free IP.
- Naming schemes with fixed-length site, environment, application, and role
  tokens plus per-prefix sequences.
- Separate decommission approval flow; released IPs may be reused, but hostnames
  and sequence numbers are not.
- Planned concurrent authentication sources: local administrator, OIDC, and
  LDAP/AD. OIDC and LDAP settings are stored in singleton database tables and
  are intended to be editable by an administrator at runtime.
- OIDC and LDAP secrets are intentionally stored in plaintext in the database,
  so encrypted or protected backups are important because backup media will
  contain those credentials as well.
- Additive roles: `admin`, `approver`, `requester`, and `viewer`.
- Planned maintenance-window integration API using hashed API keys, with
  blocked/allowed server queries and batch window creation.

## Requirements

- Go 1.27 or newer.
- A C compiler, because the application uses `github.com/mattn/go-sqlite3`.
- [Task](https://taskfile.dev/) for the migration and code-generation helpers.
- [sqlc](https://docs.sqlc.dev/en/latest/overview/install.html) if you need to
  regenerate database code after changing a query.

On macOS, install the system dependencies with Homebrew:

```sh
brew install go task sqlc
xcode-select --install
```

## Quick start

Clone the repository and change into the project directory:

```sh
git clone https://github.com/manuellara/ipam.git
cd ipam
```

Set the local administrator password before starting the server. It must be at
least 12 characters long. Values can be exported in the shell or placed in a
local `.env` file (which is not committed):

```sh
export ADMIN_PASSWORD='use-a-local-password-at-least-12-chars'
```

Run the test suite:

```sh
go test ./...
```

Start the development server:

```sh
task run-dev
```

The server listens on `http://localhost:8080`. On startup it loads `.env` if
present, creates `ipam.db` in the current working directory, applies the
embedded migrations, and ensures the `administrator` account uses the current
`ADMIN_PASSWORD` value. The database file is intentionally ignored by Git.

The local login page is available at `/login`; successful login redirects to
`/dashboard` unless the user was redirected from another protected page. The
health endpoint is `/healthz` and does not require a session.

> Security note: OIDC and LDAP credentials are intentionally stored in plain
> text in the database. That means any database backups or replicated copies of
> the SQLite file will also contain those secrets, so the backup storage itself
> must be protected accordingly.

## Database and code generation

The application runs embedded migrations during startup. To manage migrations
manually during development, use the Taskfile:

```sh
task migrate-create NAME=add_example_table
task migrate-up
task migrate-down
task templ-generate
task run-dev
```

SQL queries live in `internal/db/queries/`. After changing one of them, update
the generated Go package with:

```sh
task sqlc-generate
```

Do not hand-edit generated files in `internal/db/`; make changes in the SQL
queries or migrations and regenerate the package.

`task run-dev` regenerates templ output and sqlc output before starting the
server. `task migrate-up` and `task migrate-down` use the project-local
`.bin/migrate` binary, so the migration tool does not need to be installed
globally.

## Project layout

```text
cmd/                 Application entrypoint and HTTP controllers
docs/                Route and workflow documentation
internal/database/   SQLite connection, DSN settings, and embedded migrations
internal/db/         sqlc-generated models, queries, and interfaces
internal/auth/       Local-admin bootstrap and authentication helpers
internal/middleware/ HTTP middleware, CSRF, logging, and RBAC building blocks
internal/session/    SQLite-backed session configuration
migrations/          Database schema migrations
static/              Vendored htmx, Alpine.js, and Oat assets
Taskfile.yml         Development tasks
sqlc.yml             sqlc configuration
```

## Core workflow

1. A requester selects a naming scheme, site, environment, application, and
   role.
2. The site and environment mapping determines the subnet; the requester does
   not pick an address.
3. An approver approves or denies the request.
4. Approval allocates the next available non-reserved IP and generates a
   hostname in one database transaction.
5. A later decommission approval releases the IP for reuse without reusing the
   hostname or its sequence number.

See [`docs/routes.md`](docs/routes.md) for the route table, role permissions,
request lifecycle, and naming rules.

## Contributing

Before opening a change, run:

```sh
go test ./...
```

When changing schema or queries, also run the relevant Taskfile command and
include the migration or regenerated code required by the change. Keep route
changes synchronized with [`docs/routes.md`](docs/routes.md).

## License

No license has been selected for this repository yet.