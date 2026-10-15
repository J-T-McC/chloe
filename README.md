<p align="center"><img src="docs/assets/logo.png" alt="Chloe" width="160"></p>

<h1 align="center">Chloe</h1>

A fast desktop database client inspired by Navicat Premium. It keeps the menus, dialogs and workflows I rely on, leaves out
the bloat and the features I never used, and adds the improvements I always wanted.

Built with Go, [Wails v3](https://v3.wails.io) (system webview), React and Monaco.

![Query editor with autocomplete](docs/assets/screenshots/query.png)

## Databases

MySQL, MariaDB, PostgreSQL, SQLite, MongoDB and Redis. SQL Server and Oracle are planned.

## Features

- **Connections**: groups with drag-and-drop ordering, SSH tunnels, SSL, presets for hosted services (Supabase, Neon, RDS,
  PlanetScale…), paste a connection URL, import/export
- **Query editor**: Monaco with context-aware autocomplete (tables, columns, aliases, FK-based joins, enum values, JSON
  keys), beautify, saved queries, history log, tabs that survive a restart
- **Data grid**: browse, filter, sort and edit tables; value panel for text and JSON
- **Designers**: tables (columns, keys, foreign keys, indexes, triggers, partitions), views, functions, databases, with an
  SQL preview before saving
- **Import / export**: CSV, TSV, JSON, SQL, Excel; dump and execute SQL files
- **Data Transfer** between MySQL/MariaDB, PostgreSQL and SQLite, converting types, defaults and constraints
- **Structure and Data Synchronization** with diff views and deployment scripts
- **ER diagrams**: reverse-engineer, edit, forward-engineer and sync models
- **Users and privileges** for MySQL and PostgreSQL
- **Server monitor**: sessions, kill query, variables, status
- **MongoDB**: grid/tree/JSON views, document editor, mongosh-style shell with completion, collection designer
- **Redis**: key browser, typed value editors, TTL, console
- **MCP server** for AI assistants (Claude Code, Claude Desktop…): per-endpoint tokens and Off/Read/Read-write access by
  group, connection or database; every write is shown in an approval dialog with its SQL and row count (or runs unasked
  in opt-in danger mode); agents can propose new connections; activity log
- **Security**: passwords in the OS keychain; connections, queries and history encrypted with a keychain key

What's next is in [docs/ROADMAP.md](docs/ROADMAP.md).

## MCP server

Tools > MCP Server… serves Chloe's connections to MCP clients at `http://127.0.0.1:47101/mcp` while Chloe is open. It's
off by default, listens on loopback only, and each endpoint has its own bearer token. Create an endpoint, open up the
groups, connections or databases it may use, and use **Copy Config** to get the client snippet, for example for Claude
Code:

```sh
claude mcp add --transport http chloe-local-testing http://127.0.0.1:47101/mcp --header "Authorization: Bearer chloe_…"
```

Agents get tools to list and describe objects, select rows, run one read-only query, and insert, update or delete rows.
Chloe builds the write SQL itself from checked column names and bound values, and there's no DDL. Writes wait for your
approval unless the endpoint is in danger mode; connections an agent proposes always do, and you can type the password
into the dialog so it never passes through the conversation. Details: [docs/specs/mcp-server.md](docs/specs/mcp-server.md).

## Screenshots

| | |
|---|---|
| ![Data grid](docs/assets/screenshots/grid.png) Data grid | ![Table designer](docs/assets/screenshots/designer.png) Table designer |
| ![ER diagram](docs/assets/screenshots/erd.png) ER diagram | ![MongoDB](docs/assets/screenshots/mongo.png) MongoDB documents |
| ![Redis](docs/assets/screenshots/redis.png) Redis keys | |

Regenerate with `make e2e SCRIPT=screenshots`, then copy `e2e/.out/shots/readme-*.png` to `docs/assets/screenshots/`.

## Build and install

Requires Go 1.26+, Node 22+ and the Wails CLI:

```sh
go install github.com/wailsapp/wails/v3/cmd/wails3@v3.0.0-beta.29
```

The Makefile finds `wails3` in `$(go env GOPATH)/bin` even when that isn't on your PATH:

```sh
make dev       # run with hot reload
make build     # build bin/Chloe
make install   # macOS: build a signed Chloe.app and replace /Applications/Chloe.app (rerun to update)
```

`install` quits a running copy first; saved connections and keychain passwords carry over. Builds are signed with
`CHLOE_SIGN_IDENTITY`, else the first "Apple Development" certificate in the keychain, else ad hoc. A stable signature
keeps the keychain trusting Chloe across rebuilds.

## Test

```sh
make test                     # Go unit tests (SQLite included, no Docker)
make test-it                  # start the Docker databases, seed them, run integration tests
make test-frontend            # type check and frontend unit tests
make e2e SCRIPT=sqlite        # build the browser build, then Playwright (smoke, sqlite, mongo, mcp, …)
```

The Docker databases (`docker compose up -d`) use password `chloe`: MySQL `:33306`, PostgreSQL `:55432`, MongoDB `:37017`
(user `root`) and Redis `:63790`, seeded with `shop` sample data from `testdata/`.

`CHLOE_CONFIG_DIR=<dir>` with `CHLOE_SECRETS=memory` runs against an isolated config without touching the keychain. The
browser build (`bin/Chloe-server`) has no login, so it requires `CHLOE_SECRETS=memory` and listens only on loopback.

## License

[MIT](LICENSE). Not affiliated with Navicat or PremiumSoft.
