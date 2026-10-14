# Chloe

A Navicat Premium 18 clone: Go core + Wails v3 (pinned v3.0.0-beta.29) + React 19/TS + Monaco.
What's done and what's next: `docs/ROADMAP.md`. Behaviour specs: `docs/specs/`.

## Layout
- `internal/` is plain Go with no Wails dependency: `core` (manager, design/edit/transfer/dump),
  `driver/{mysql,postgres,sqlite}` + `rdb` (Redis), `design` (DDL generators), `sqlsplit`, `transfer`, `privs`, `store`.
- `services/` holds thin Wails services over `core`. Their bindings are generated into `frontend/bindings`.
- `frontend/src`: `components/`, `editor/` (Monaco completion and format), `sql/` (lexer and autocomplete engine),
  `dialect.ts`, `menus.ts`, `tree.ts`.

## Commands
```sh
export PATH=$PATH:$(go env GOPATH)/bin
wails3 task common:generate:bindings   # after changing a service or any type it exposes
wails3 task build                      # desktop app -> bin/Chloe
wails3 task build:server               # browser build on :8080 -> bin/Chloe-server (used by e2e)
docker compose up -d                   # MySQL :33306, Postgres :55432, Redis :63790, sshd :2222 (password "chloe")
go test ./...                          # unit tests plus SQLite (no Docker needed)
CHLOE_IT=1 go test ./internal/...      # integration tests against the Docker databases
(cd frontend && npx tsc --noEmit && npx vitest run)
e2e/run.sh [smoke|sqlite|designer|...] # Playwright against bin/Chloe-server; reseeds the databases first
```
Sample data lives in `testdata/seed_*.sql`. To rebuild `testdata/shop.db`, run
`rm -f testdata/shop.db && sqlite3 testdata/shop.db < testdata/seed_sqlite.sql`.

## Rules
- Conventional commits (`feat(scope):`, `fix(scope):`, `test:`, `docs:`) and push to origin main.
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
- Wails sets the macOS Dock icon before launch finishes, so `main.go` sets it again on ApplicationStarted.
