# MCP Server — spec

Chloe can act as an MCP server so AI assistants (Claude Code, Claude Desktop, other MCP clients) can read and, when
allowed, change data through the connections Chloe already has. Navicat has no equivalent; this design came from a
discussion with the user (2026-10-14), not from reverse engineering.

## Transport and security
Both transports run inside the app (no separate process) while Chloe is open and the server is switched on (off by
default), and serve the same endpoints, tools, approvals and activity log.

- **Stdio bridge** (recommended). The client launches `Chloe mcp` (the app binary, e.g.
  `/Applications/Chloe.app/Contents/MacOS/Chloe mcp`), a stdio MCP server that relays to the running app over the
  Unix socket `mcp.sock` in Chloe's config folder. It exits before opening a window, the store or the keychain.
  - The folder is `0700` and the socket `0600`; Chloe also checks the peer's user (`LOCAL_PEERCRED` on macOS,
    `SO_PEERCRED` on Linux) and refuses other users.
  - The token comes from `CHLOE_MCP_TOKEN` in the client's server config (never the command line, where other
    processes can read it). The bridge's first line is `{"token": …}`; Chloe answers `{"ok": true}` or
    `{"error": …}`, then newline-delimited MCP messages follow.
  - No network port means no other program can pose as Chloe by taking the port first and collecting the tokens and
    queries clients send. A bridged session ends when its endpoint is disabled or deleted, or the server stops.
  - Errors go to the client's log on stderr: Chloe not running or the server off, an unknown token.
  - macOS and Linux. Windows (untested) would lack the peer-user check, and file modes don't apply there; it
    should use a named pipe with a user-only ACL instead.
- **Streamable HTTP** on `http://127.0.0.1:<port>/mcp` (default port 47101), loopback only, never `0.0.0.0`.
  "HTTP on port" switches it off, leaving only the bridge.
  - **Bearer token per endpoint** (`Authorization: Bearer chloe_…`). The token decides which endpoint applies.
    Requests without a known token get 401. Tokens are compared by hash in constant time.
  - **Browser protection**: requests whose `Host` isn't `127.0.0.1:<port>`/`localhost:<port>` (DNS rebinding) or
    that carry a cross-site `Origin` are refused.
- **No TLS.** Loopback traffic never leaves the machine, and reading it needs root. TLS would only add server
  authentication (the port-squatting case), which MCP clients can't do per server: Claude Code has no per-server
  certificate pinning or CA option, only a global `NODE_EXTRA_CA_CERTS`. The bridge covers that case instead.
- Endpoints and tokens live in `mcp.json`, encrypted with the keychain key like `connections.json`.

## Endpoints
An endpoint is a named access profile (e.g. "Claude Code – dev"):
- **Name**, **Enabled**, **Token** (Regenerate revokes the old one at once; Copy Config puts it in a client snippet).
- **Access** follows the connection tree: Off / Read / Read-write per group, per connection ("Same as group" by
  default) and per database ("Same as connection" by default); the most specific setting wins. Connections outside
  a group, and groups, start Off, so a new connection is only exposed through a group you've opened up.
  Renaming a group keeps its access; deleting it removes it (its connections move to the top level, which is Off).
- **Danger mode** ("Run writes without asking"): writes run without the approval dialog. Every other rule still
  applies. Turning it on asks for confirmation; a titlebar indicator shows while any endpoint has it on.
- **Capabilities** (each opt-in): Create connections, Saved queries and query tabs, Monitor, ER models, Reports
  (BI workspaces, charts and dashboards; tools in `docs/specs/bi.md`).

## Approval
Without danger mode, every write shows a dialog in Chloe: endpoint, connection/database, the exact SQL (with values),
and the number of rows it affects, with **Deny** and **Approve**. For update/delete Chloe counts the matching rows
first; on Approve it runs the statement in a transaction and rolls back if the affected count differs from what was
approved. Deny gives the agent "declined by the user in Chloe". With no answer within 2 minutes the dialog closes,
nothing runs, and the agent is told nobody answered (so it doesn't take silence for a refusal).
Creating a connection always asks, even in danger mode, and the password can be typed into the dialog instead of
coming from the agent.

## Tools
Results are `{columns, rows, row_count, truncated}` with cells as display strings (as the grid shows them; binary as
`0x…` hex; `null` for NULL). Default limit 100 rows, max 1000, ~256 KB per response, long cells cut at 4 KB. Every
statement has a 30 s timeout. Table and column names are checked against the database; values are bound as
parameters. A connection the endpoint can use is opened on demand (it needs a saved password).

### Data (per connection access)
| Tool | Access | Arguments |
|---|---|---|
| `list_connections` | any | — |
| `list_databases` | Read | `connection` |
| `list_schemas` | Read | `connection, database` (PostgreSQL) |
| `list_tables` | Read | `connection, database, schema?` |
| `describe_table` | Read | `connection, database, schema?, table` |
| `get_ddl` | Read | `connection, database, schema?, name, kind` |
| `select_rows` | Read | `connection, database, schema?, table, columns?, where?, order_by?, limit?, offset?` |
| `run_query` | Read | `connection, database, schema?, sql, limit?` |
| `insert_rows` | Read-write | `connection, database, schema?, table, rows` |
| `update_rows` | Read-write | `connection, database, schema?, table, where, set` |
| `delete_rows` | Read-write | `connection, database, schema?, table, where` |

- `where` is a list of conditions joined with AND: `{column, op, value}` with `op` one of
  `= != < <= > >= like not_like in not_in between is_null is_not_null`.
- `run_query` takes one statement starting with SELECT, WITH, EXPLAIN, SHOW, DESCRIBE, VALUES or TABLE (no PRAGMA)
  and runs it in a read-only transaction (`START TRANSACTION READ ONLY`, `BEGIN READ ONLY`, SQLite `query_only`).
  MySQL also refuses `INTO OUTFILE/DUMPFILE`. Functions with side effects outside the data (e.g. PostgreSQL
  `pg_terminate_backend`) are limited only by the connection's database user, so share connections with a
  least-privileged user.
- `update_rows` and `delete_rows` need a non-empty `where`. Each write call is one transaction.
- Values are bound as text and the database converts them to the column type. A JSON list for a PostgreSQL array
  column becomes an array literal (nested lists for multi-dimensional arrays); elsewhere lists and objects are sent
  as JSON text.
- Endpoints with no Read-write access anywhere aren't offered the write tools; calling one is an unknown-tool error.
  The list follows the settings on the next request, so a client sees the change when it reconnects.
- No DDL, no raw write SQL in v1.
- MySQL, PostgreSQL and SQLite; Redis has its own tools (below); MongoDB later.

### Redis (per connection access)
Offered to endpoints that can see a Redis connection. Access is per logical database, keyed by its number
(`db0`, `db1`… in the access tree). `list_connections` lists Redis connections with engine `redis`; the SQL tools
refuse them.

| Tool | Access | Arguments |
|---|---|---|
| `redis_databases` | any | `connection` |
| `redis_scan` | Read | `connection, db?, pattern?, type?, cursor?, count?` |
| `redis_get` | Read | `connection, db?, key, cursor?, count?` |
| `redis_command` | Read | `connection, db?, args` |
| `redis_set` | Read-write | `connection, db?, key, type, value, ttl?, only_if_new?` |
| `redis_edit` | Read-write | `connection, db?, key, incr?, set_fields?, delete_fields?, incr_fields?, push?, push_head?, add?, add_scored?, incr_scores?, remove?, add_entry?` |
| `redis_delete` | Read-write | `connection, db?, keys` |
| `redis_expire` | Read-write | `connection, db?, key, ttl` |

- `db` defaults to 0. Keys that aren't UTF-8 come back as base64 with `key_base64`, which the agent passes back;
  values that aren't UTF-8 are shown as `0x…` hex. Strings are cut at 64 KB and collection elements at 4 KB, and
  the result says `truncated` so an agent doesn't write a cut value back. Approvals show binary bytes as `\xNN`.
- `redis_scan` and `redis_get` page with a cursor (default 100, at most 1000 keys or elements; Redis treats the
  count as a hint, so small hashes and sets come whole). A cursor that isn't one the tool returned is refused.
- `redis_command` runs one command from a fixed list of read commands (GET, HGETALL, ZRANGEBYSCORE, XRANGE,
  PFCOUNT, GEOSEARCH, OBJECT ENCODING, JSON.GET…). Nothing that writes, blocks, switches database or runs a script;
  KEYS is refused in favour of `redis_scan`. The reply is formatted like redis-cli.
- Writes are built from fixed commands with the agent's keys and values as arguments, run in MULTI/EXEC, and shown
  in the approval dialog as redis-cli commands. `redis_set` replaces a whole value; a replaced key keeps its expiry
  unless `ttl` is given (a string replaced by a string with `KEEPTTL`, any other replacement with `PEXPIREAT` to the
  same deadline). `redis_edit` changes part of one value, creating it when missing: counters (`INCRBY`), hash
  fields, list values, set and sorted set members (`ZADD CH`, `ZINCRBY`), stream entries. `changed` reports what
  Redis actually did, so removing a member that isn't there gives 0. `redis_delete` takes at most 1000 exact keys (duplicates are dropped; UNLINK); there's no
  delete by pattern, and no FLUSHDB.
- After approval the key is WATCHed and its type checked against what was approved before MULTI/EXEC, so a key
  created, deleted or retyped by someone else while the dialog was open, or written to during the check, aborts the
  write ("changed while waiting for approval"). Changes of the same type in that window aren't detected: `redis_set`
  still replaces the whole value and `redis_edit`'s changes are relative to whatever is there.
- Summaries quote the key, so a key's text can't read as part of the sentence. Approvals list the first 20
  commands and cut each value at 4 KB; the summary always gives the full count.
- Reads have only Chloe's 30 s client timeout: Redis has no server-side limit, so `HGETALL` or `SMEMBERS` on a huge
  key can stall a single-threaded server for everyone. This is the same trust as `run_query`.
- Monitor (capability) covers Redis too: `list_sessions` (CLIENT LIST), `server_status` (INFO, each entry with its `section`), `server_variables` (CONFIG GET, with passwords hidden), `kill_session` (CLIENT KILL; no
  `query_only`). Sessions show client flags in words (blocked, pub/sub…) and the last command.

### Monitor (capability)
| Tool | Access | Arguments |
|---|---|---|
| `list_sessions` | Read | `connection` |
| `server_status` | Read | `connection, filter?` |
| `server_variables` | Read | `connection, filter?` |
| `kill_session` | Read-write | `connection, id, query_only?` |

- MySQL/MariaDB, PostgreSQL and Redis. Sessions and status span the whole server, so the endpoint needs the level on every
  database of the connection; one hidden or read-only database makes the tools refuse.
- `filter` keeps names containing the text (case-insensitive). Session queries are cut like result cells.
- `kill_session` asks unless danger mode is on, showing the session's user, host, database, state, time and query.
  `query_only` cancels the running query and keeps the session. Chloe's own sessions can't be killed. It's offered
  only to endpoints with Read-write somewhere, like the row tools.

### Saved queries (capability)
| Tool | Access | Arguments |
|---|---|---|
| `list_saved_queries` | Read | `connection, database, schema?` |
| `get_saved_query` | Read | `connection, database, schema?, name` |
| `save_query` | Read | `connection, database, schema?, name, sql` |
| `open_query_tab` | Read | `connection, database, schema?, sql?` or `name` |

- `save_query` adds a query to the database's Queries folder; it never replaces one (a taken name is an error), so
  an agent can't change SQL the user already trusts. It never runs the SQL.
- `open_query_tab` opens a new, unsaved tab with the agent's SQL (titled "Query from <endpoint>" unless `name`
  gives a title), or the saved query `name` when there's no `sql`. Chloe comes to the front. Nothing runs and the
  agent never sees results: this is how an agent hands over DDL or bulk changes, which the row tools don't do.
- These touch no data, so they need only Read access to the database, and don't ask.

### Create connections (capability)
`create_connection` (a URL, or engine, host, port, user, database, sslmode; or an existing SQLite file) always asks,
even in danger mode. The dialog shows the settings and has a password field, so the password needn't pass through
the agent; a SQLite file gets a warning naming the file. The file is only checked after approval, so the agent
can't use the tool to learn which paths exist. Chloe tests the connection and saves it only if it opens.
No SSH tunnels, client certificates or socket files. New connections go into the endpoint's group for new
connections ("Created by AI" by default, created when needed) and get that group's access, for every endpoint.
There's no separate `test_connection` tool: it would let an agent probe hosts and ports without asking.

### ER models (capability)
| Tool | Access | Arguments |
|---|---|---|
| `list_models` | — | — |
| `get_model` | — | `model, tables?` |
| `create_model` | Read (when built from a database) | `name` and one of `dialect` (empty), `connection, database, schema?, tables?` (reverse engineered) or `copy_of` |
| `edit_model` | — | `model, set_tables?, drop_tables?, rename?` |
| `generate_sql` | Read (with a connection) | `model, connection?, database?, schema?` |
| `open_model` | — | `model` |

- Agents see the models in Chloe's library (`models/` in the config folder; see `docs/specs/models.md`). `model` is a
  path from `list_models`; files outside the library are never read.
- `edit_model` changes only models created over MCP (`createdBy: "mcp:<endpoint>"`, shown as "AI (endpoint)" in the
  Models list); the user's own are read and copied, never written. Tables in
  `set_tables` have `get_model`'s shape (unknown fields are refused) and replace a table of the same name or are
  added; other tables, and their diagram positions, are kept. New tables are laid out when the diagram opens.
- An open model tab reloads when an agent edits its file, unless it has unsaved changes; then the status bar says
  the file changed and Save As keeps the user's version.
- `generate_sql` returns CREATE statements, or with a connection the statements that would bring that database in
  line with the model (as Synchronize to Database plans them, without dropping tables). Nothing is run: applying
  stays a manual Synchronize to Database or a query tab.
- MySQL, MariaDB and PostgreSQL; models don't support SQLite.

### MongoDB (planned)
Filters, sorts, projections, pipelines and documents arrive as JSON (Extended JSON for `$oid`, `$date`…) and are
never evaluated as JavaScript in Chloe. Guards, checked recursively before anything is sent:
- No server-side JavaScript: `$where`, `$function` and `$accumulator` are refused anywhere in a filter, pipeline or
  update.
- Updates take operator documents only (`$set`, `$inc`…); pipeline-style updates and whole-document replacement
  through the update tool are refused.
- Read tools refuse the writing stages `$out` and `$merge`, also inside `$facet`, `$lookup` and `$unionWith`
  pipelines; the admin stages `$currentOp`, `$listSessions` and `$listLocalSessions`; and a `$lookup`,
  `$graphLookup` or `$unionWith` whose source names another database.
- Every command carries `maxTimeMS` (30 s), so the server stops it rather than only Chloe giving up.
- Update and delete first collect the matching `_id`s (capped), the user approves that count, and the write runs
  with `{$and: [filter, {_id: {$in: approved}}]}`, so it can never touch a document that wasn't approved.


A tab like the History Log:
- Toolbar: **Run MCP server**, **HTTP on port** (with the port), and the status (HTTP URL and/or the bridge).
- **Copy Config** per client: Claude Code (bridge or HTTP), JSON with the bridge (Claude Desktop, `.mcp.json`,
  Cursor…), JSON over HTTP, Claude Desktop over HTTP via `mcp-remote`. The bridge is the default.
- Left: endpoints list (**New Endpoint**, **Delete**). Right: the selected endpoint's name, enabled, token
  (Copy, Regenerate), danger mode, capabilities and the connection access tree. The split is draggable and remembered.
- **Activity** tab: time, endpoint, tool, connection/database, SQL, rows, outcome (ok, error, approved, denied, no answer, ran without asking).
  The last 1000 calls are kept in memory only.

## Delivery
1. Server, endpoints, data tools, approval, danger mode, activity, window.
2. Monitor, saved queries / query tabs, create connections. ✅
3. ER models. ✅
4. Reports (`docs/specs/bi.md`): workspaces, data sources and charts ✅; dashboards ✅.
5. Redis ✅; MongoDB next.
