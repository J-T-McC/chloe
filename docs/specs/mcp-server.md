# MCP Server — spec

Chloe can act as an MCP server so AI assistants (Claude Code, Claude Desktop, other MCP clients) can read and, when
allowed, change data through the connections Chloe already has. Navicat has no equivalent; this design came from a
discussion with the user (2026-10-14), not from reverse engineering.

## Transport and security
- **Streamable HTTP** served by the running app on `http://127.0.0.1:<port>/mcp` (default port 47101). It only runs
  while Chloe is open and the server is switched on (off by default). It binds to loopback only, never `0.0.0.0`.
- **No TLS.** Loopback traffic never leaves the machine; MCP clients reject self-signed certificates and a trusted
  local CA would mean installing a root certificate. No stdio bridge in v1.
- **Bearer token per endpoint** (`Authorization: Bearer chloe_…`). The token decides which endpoint applies.
  Requests without a known token get 401. Tokens are compared by hash in constant time.
- **Browser protection**: requests whose `Host` isn't `127.0.0.1:<port>`/`localhost:<port>` (DNS rebinding) or
  that carry a cross-site `Origin` are refused.
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
- **Capabilities** (each opt-in): Create connections, Saved queries and query tabs, Monitor, ER models.

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
- MySQL, PostgreSQL and SQLite in v1; MongoDB and Redis later.

### Monitor (capability)
| Tool | Access | Arguments |
|---|---|---|
| `list_sessions` | Read | `connection` |
| `server_status` | Read | `connection, filter?` |
| `server_variables` | Read | `connection, filter?` |
| `kill_session` | Read-write | `connection, id, query_only?` |

- MySQL/MariaDB and PostgreSQL. Sessions and status span the whole server, so the endpoint needs the level on every
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

- Models are `.chloemodel` files. An agent sees the user's recent model files and the models created over MCP, which
  live in `models/` in Chloe's config folder and join the recent list. `model` is a path from `list_models`; no
  other file is read.
- `edit_model` changes only models created over MCP; the user's own are read and copied, never written. Tables in
  `set_tables` have `get_model`'s shape (unknown fields are refused) and replace a table of the same name or are
  added; other tables, and their diagram positions, are kept. New tables are laid out when the diagram opens.
- An open model tab reloads when an agent edits its file, unless it has unsaved changes; then the status bar says
  the file changed and Save As keeps the user's version.
- `generate_sql` returns CREATE statements, or with a connection the statements that would bring that database in
  line with the model (as Synchronize to Database plans them, without dropping tables). Nothing is run: applying
  stays a manual Synchronize to Database or a query tab.
- MySQL, MariaDB and PostgreSQL; models don't support SQLite.

## Window (Tools > MCP Server…, and the titlebar MCP button)
A tab like the History Log:
- Toolbar: **Server on/off**, port, URL, and **Copy Config** (Claude Code / Claude Desktop / JSON).
- Left: endpoints list (**New Endpoint**, **Delete**). Right: the selected endpoint's name, enabled, token
  (Copy, Regenerate), danger mode, capabilities and the connection access tree. The split is draggable and remembered.
- **Activity** tab: time, endpoint, tool, connection/database, SQL, rows, outcome (ok, error, approved, denied, no answer, ran without asking).
  The last 1000 calls are kept in memory only.

## Delivery
1. Server, endpoints, data tools, approval, danger mode, activity, window.
2. Monitor, saved queries / query tabs, create connections. ✅
3. ER models. ✅
