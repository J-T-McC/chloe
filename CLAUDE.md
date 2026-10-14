# Chloe

A Navicat Premium 18 clone: Go core + Wails v3 (pinned v3.0.0-beta.29) + React 19/TS + Monaco.
What's done and what's next: `docs/ROADMAP.md`. Behaviour specs: `docs/specs/`.

## Layout
- `internal/` is plain Go with no Wails dependency: `core` (manager, design/edit/transfer/dump),
  `driver/{mysql,postgres,sqlite}` + `rdb` (Redis) + `mdb` (MongoDB: documents as `Val`, goja shell),
  `design` (DDL generators), `sqlsplit`, `transfer`, `privs`, `store`.
- `services/` holds thin Wails services over `core`. Their bindings are generated into `frontend/bindings`.
- `frontend/src`: `components/` (`mongo/`, `redis/`…), `editor/` (Monaco completion and format), `sql/` (lexer and
  autocomplete engine), `mongo/` (document formatting and shell completion),
  `dialect.ts`, `menus.ts`, `tree.ts`.

## Commands
```sh
export PATH=$PATH:$(go env GOPATH)/bin
wails3 task common:generate:bindings   # after changing a service or any type it exposes
wails3 task build                      # desktop app -> bin/Chloe
wails3 task install                    # signed Chloe.app -> /Applications (quits a running copy)
wails3 task build:server               # browser build on :8080 -> bin/Chloe-server (used by e2e; needs CHLOE_SECRETS=memory)
docker compose up -d                   # MySQL :33306, Postgres :55432, Redis :63790, MongoDB :37017 (root), sshd :2222 (password "chloe")
go test ./...                          # unit tests plus SQLite (no Docker needed)
CHLOE_IT=1 go test ./internal/...      # integration tests against the Docker databases
(cd frontend && npx tsc --noEmit && npx vitest run)
e2e/run.sh [smoke|sqlite|mongo|joins|...] # Playwright against bin/Chloe-server; reseeds the databases first
```
Sample data lives in `testdata/seed_*` (`seed_mongo.js` runs in mongosh). To rebuild `testdata/shop.db`, run
`rm -f testdata/shop.db && sqlite3 testdata/shop.db < testdata/seed_sqlite.sql`.

## Rules
- Conventional commits (`feat(scope):`, `fix(scope):`, `test:`, `docs:`). Work on a branch and land it through a GitHub PR
  (`gh pr create`, then `gh pr merge --squash --delete-branch` once tests pass); no direct pushes to main.
- Match Navicat's menus, dialogs and layouts. Every split pane is draggable and its size is remembered.
- Destructive menu items are not coloured red.
- Reverse engineering (REA, Ghidra) of the installed Navicat is for behaviour specs only.
  Never copy decompiled code, names or assets.
- Only support engines that can be tested in local Docker (Snowflake is out). Don't start the MCP server feature until the user discusses it.
- Verify UI changes in the browser build with an e2e script, not just a type check.

## Gotchas
- In async React handlers, read `e.target.value` before any `await`: a controlled input resets once the handler yields.
- `Dialect.TransactionalDDL()` is false only for MySQL. SQLite ALTERs it can't express become a table rebuild
  (`design.RebuildPrefix`), guarded by a `foreign_key_check` before and after in `core.ExecuteDDL`.
- modernc.org/sqlite always parses DATE/DATETIME text columns into `time.Time`.
- macOS secrets go through the Security framework (`internal/store/keyring_darwin.go`), never `/usr/bin/security`: items the
  CLI creates can be read by any process without a prompt. `CHLOE_KEYCHAIN_IT=1 go test ./internal/store` exercises it.
  `connections.json` is AES-GCM encrypted under a keychain key (plain with `CHLOE_SECRETS=memory`); if the key can't be read
  the store is `Locked()` and never writes, so the file survives. `queries.json`, `history.json` and `drafts.json`
  (`store.Workspace`) use the same key. Saved queries are tree objects of kind `query`, served by `ListObjects`/`DDL`.
- Tab panes mount when first shown and stay mounted. Query tabs aren't closed by Close Connection/Database; close
  tabs through `requestCloseTabs` (it asks to save edited queries), not `closeTabs`.
- Data Transfer (`core/datatransfer.go`) converts tables with `design.Convert(t, from, to)`: types, defaults (literal
  re-quoting), auto increment ↔ identity ↔ `INTEGER PRIMARY KEY`, unnamed SQLite constraints. Cells arrive as display
  strings, so `convertValue` turns hex back into bytes and booleans/timestamps into what the target takes.
- Wails sets the macOS Dock icon before launch finishes, so `main.go` sets it again on ApplicationStarted.
