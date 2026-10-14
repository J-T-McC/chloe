<p align="center"><img src="docs/assets/logo.png" alt="Chloe" width="160"></p>

<h1 align="center">Chloe</h1>

A fast desktop database client for MySQL, MariaDB, PostgreSQL and Redis (SQLite, MongoDB, SQL Server and Oracle planned).

Built with Go and [Wails v3](https://v3.wails.io) (system webview), React and Monaco.

## Layout

| Path | What |
|---|---|
| `internal/core` | Connections, editor sessions, streamed result buffers, table browse and edits. No UI dependencies. |
| `internal/driver/{mysql,postgres}` | Engine drivers: metadata, DDL, sessions, cancel. |
| `internal/tunnel` | Direct and SSH dialers. |
| `internal/store` | Saved connections (JSON) with secrets in the OS keychain. |
| `internal/sqlsplit` | Dialect-aware statement splitter (DELIMITER, `$$`, comments). |
| `services` | Thin Wails bindings over `internal/core`. |
| `frontend/src/sql` | Autocomplete engine (pure TS, unit tested). |
| `frontend/src/components` | UI: connection tree, objects pane, data grid, table viewer, query editor. |
| `docs/specs` | Behaviour specs for the features being built. |

## Develop

```sh
wails3 dev                # run with hot reload
wails3 task build         # build bin/Chloe
wails3 task test          # Go unit tests
wails3 task test:it       # start Docker test databases, seed, run integration tests
cd frontend && npx vitest # frontend unit tests
```

Test databases (from `docker-compose.yml`): MySQL on `127.0.0.1:33306` and PostgreSQL on `127.0.0.1:55432`, password `chloe`, with a `shop` sample schema from `testdata/`.

`CHLOE_CONFIG_DIR=<dir>` and `CHLOE_SECRETS=memory` run the app against an isolated config without touching the keychain.
