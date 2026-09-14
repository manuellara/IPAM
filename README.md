# IPAM

IPAM is a self-hosted IP address management application for teams that need a
repeatable way to request, approve, allocate, and decommission infrastructure
addresses. It is intended to replace per-subnet spreadsheets with a workflow
that also generates standardized hostnames.

The project is currently under active development. The repository contains the
database schema, migrations, sqlc data-access layer, session storage, middleware
building blocks, and the initial HTTP server wiring. The complete request,
approval, administration, and authentication workflows are documented in
[`docs/routes.md`](docs/routes.md) and are being implemented incrementally.

## Features and design

- SQLite database with embedded, automatic migrations.
- SQL queries compiled to Go with [sqlc](https://sqlc.dev/).
- SQLite-backed sessions using `scs`.
- Request and approval workflow for IP allocation.
- Site and environment mappings so requesters do not choose IPs directly.
- Reserved-address support and transactional allocation of the next free IP.
- Naming schemes with fixed-length site, environment, application, and role
  tokens plus per-prefix sequences.
- Separate decommission approval flow; released IPs may be reused, but hostnames
  and sequence numbers are not.
- Planned concurrent authentication sources: local administrator, OIDC, and
  LDAP/AD.
- Additive roles: `admin`, `approver`, `requester`, and `viewer`.

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

Optionally run the test suite:

```sh
go test ./...
```

Start the HTTP server:

```sh
go run ./cmd
```

The server listens on `http://localhost:8080`. On first startup it creates
`ipam.db` in the current working directory and applies the embedded migrations.
The database file is intentionally ignored by Git.

The current server wiring is still a work in progress, so some pages and login
handlers are placeholders even though their intended routes are documented.

## Database and code generation

The application runs embedded migrations during startup. To manage migrations
manually during development, use the Taskfile:

```sh
task migrate-up
task migrate-down
task migrate-create NAME=add_example_table
```

SQL queries live in `internal/db/queries/`. After changing one of them, update
the generated Go package with:

```sh
task sqlc-generate
```

Do not hand-edit generated files in `internal/db/`; make changes in the SQL
queries or migrations and regenerate the package.

## Project layout

```text
cmd/                 Application entrypoint and HTTP controllers
docs/                Route and workflow documentation
internal/database/   SQLite connection and embedded migrations
internal/db/         sqlc-generated models, queries, and interfaces
internal/middleware/ HTTP middleware, CSRF, logging, and RBAC building blocks
internal/session/    SQLite-backed session configuration
migrations/          Database schema migrations
static/              Vendored browser assets
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