# Chloe

A Navicat Premium 18 clone: Go core + Wails v3 (pinned v3.0.0-beta.29) + React 19/TS + Monaco.
What's done and what's next: `docs/ROADMAP.md`. Behaviour specs: `docs/specs/`.

## Layout
- `internal/` is plain Go with no Wails dependency: `core` (manager, design/edit/transfer/dump),
  `driver/{mysql,postgres,sqlite}` + `rdb` (Redis) + `mdb` (MongoDB: documents as `Val`, goja shell),
  `design` (DDL generators), `sqlsplit`, `transfer`, `privs`, `store`, `mcpserver` (MCP tools over `core`),
  `bi` (BI workspaces: data sources, charts, the chart query builder).
- `services/` holds thin Wails services over `core`. Their bindings are generated into `frontend/bindings`.
- `frontend/src`: `components/` (`mongo/`, `redis/`…), `editor/` (Monaco completion and format), `sql/` (lexer and
  autocomplete engine), `mongo/` (document formatting and shell completion),
  `dialect.ts`, `menus.ts`, `tree.ts`.

## Commands
The Makefile wraps the Wails tasks and puts `$(go env GOPATH)/bin` on PATH.
```sh
make bindings                          # after changing a service or any type it exposes
make dev                               # hot reload
make build                             # desktop app -> bin/Chloe
make install                           # signed Chloe.app -> /Applications (quits a running copy)
make server                            # browser build on :8080 -> bin/Chloe-server (used by e2e; needs CHLOE_SECRETS=memory)
make db                                # MySQL :33306, Postgres :55432, Redis :63790, MongoDB :37017 (root), sshd :2222 (password "chloe")
make test                              # unit tests plus SQLite (no Docker needed)
make test-it                           # start and seed the Docker databases, run integration tests
make test-frontend                     # type check and frontend unit tests
make e2e SCRIPT=smoke                  # Playwright against the browser build (smoke|sqlite|mongo|joins|mcp|...); reseeds first
e2e/tester.sh                          # MCP sandbox for a tester agent (seeded games db, Reports on); then
node e2e/mcp-call.mjs tools            #   list tools, or `node e2e/mcp-call.mjs <tool> '<json>'` (images saved to e2e/.tester/images)
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
- Only support engines that can be tested in local Docker (Snowflake is out).
- MCP server (`docs/specs/mcp-server.md`): loopback HTTP only, no TLS; Chloe builds all write SQL from checked column
  names and bound values; writes wait for approval unless the endpoint is in danger mode. No DDL over MCP.
- Verify UI changes in the browser build with an e2e script, not just a type check.

## Gotchas
- In async React handlers, read `e.target.value` before any `await`: a controlled input resets once the handler yields.
- `Dialect.TransactionalDDL()` is false only for MySQL. SQLite ALTERs it can't express become a table rebuild
  (`design.RebuildPrefix`), guarded by a `foreign_key_check` before and after in `core.ExecuteDDL`.
- modernc.org/sqlite always parses DATE/DATETIME text columns into `time.Time`.
- macOS secrets go through the Security framework (`internal/store/keyring_darwin.go`), never `/usr/bin/security`: items the
  CLI creates can be read by any process without a prompt. `CHLOE_KEYCHAIN_IT=1 go test ./internal/store` exercises it.
  `connections.json` is AES-GCM encrypted under a keychain key (plain with `CHLOE_SECRETS=memory`); if the key can't be read
  the store is `Locked()` and never writes, so the file survives. Once the key exists, a plain file where a sealed one
  belongs is refused, and sealed files are bound to their name (version 2). `queries.json`, `history.json` and `drafts.json`
  (`store.Workspace`) use the same key. Saved queries are tree objects of kind `query`, served by `ListObjects`/`DDL`.
- Tab panes mount when first shown and stay mounted. Query tabs aren't closed by Close Connection/Database; close
  tabs through `requestCloseTabs` (it asks to save edited queries), not `closeTabs`.
- Data Transfer (`core/datatransfer.go`) converts tables with `design.Convert(t, from, to)`: types, defaults (literal
  re-quoting), auto increment ↔ identity ↔ `INTEGER PRIMARY KEY`, unnamed SQLite constraints. Cells arrive as display
  strings, so `convertValue` turns hex back into bytes and booleans/timestamps into what the target takes.
- Live BI chart results are cached for 5 minutes (`core/bicache.go`): a test that changes data and reads the same
  chart again needs `core.BIRead{Since: time.Now()}` (what Refresh sends) or `Uncached`.
- Wails sets the macOS Dock icon before launch finishes, so `main.go` sets it again on ApplicationStarted.
