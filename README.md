<p align="center"><img src="docs/assets/logo.png" alt="Chloe" width="160"></p>

<h1 align="center">Chloe</h1>

A fast desktop database client for MySQL, MariaDB, PostgreSQL, SQLite, MongoDB and Redis (SQL Server and Oracle planned).

Built with Go and [Wails v3](https://v3.wails.io) (system webview), React and Monaco.

## Layout

| Path | What |
|---|---|
| `internal/core` | Connections, editor sessions, streamed result buffers, table browse and edits. No UI dependencies. |
| `internal/driver/{mysql,postgres,sqlite}` | Engine drivers: metadata, DDL, sessions, cancel. |
| `internal/mdb`, `internal/rdb` | MongoDB (documents, indexes, a goja-based shell) and Redis clients. |
| `internal/tunnel` | Direct and SSH dialers. |
| `internal/store` | Saved connections (JSON) with secrets in the OS keychain (on macOS through the Security framework, so only Chloe's signature can read them). |
| `internal/sqlsplit` | Dialect-aware statement splitter (DELIMITER, `$$`, comments). |
| `services` | Thin Wails bindings over `internal/core`. |
| `frontend/src/sql`, `frontend/src/mongo` | SQL and MongoDB autocomplete engines (pure TS, unit tested). |
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

Test databases (from `docker-compose.yml`): MySQL on `127.0.0.1:33306`, PostgreSQL on `127.0.0.1:55432`, MongoDB on `127.0.0.1:37017` (user `root`) and Redis on `127.0.0.1:63790`, password `chloe`, with `shop` sample data from `testdata/`.

`CHLOE_CONFIG_DIR=<dir>` and `CHLOE_SECRETS=memory` run the app against an isolated config without touching the keychain.
The browser build (`bin/Chloe-server`) has no login, so it refuses to start without `CHLOE_SECRETS=memory` and only listens on
loopback (`CHLOE_ALLOW_REMOTE=1` overrides that, as the Docker image does). Exported connections carry passwords only when
a passphrase is given, encrypted with it (scrypt + AES-256-GCM).

macOS builds are signed with `CHLOE_SIGN_IDENTITY`, else the first "Apple Development" certificate in the keychain, else ad hoc.
A stable signature lets the Keychain keep trusting Chloe across rebuilds. Sharing builds needs a Developer ID certificate
and notarization (`wails3 setup`, then `wails3 task darwin:sign:notarize`).
