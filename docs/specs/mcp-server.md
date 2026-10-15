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
- **Connection access**: Off / Read / Read-write per connection, with an optional override per database.
  Connections start Off, so a new connection is never exposed until it is added.
- **Danger mode** ("Run writes without asking"): writes run without the approval dialog. Every other rule still
  applies. Turning it on asks for confirmation; a titlebar indicator shows while any endpoint has it on.
- **Capabilities** (each opt-in): Create connections, Saved queries and query tabs, Monitor, ER models.

## Approval
Without danger mode, every write shows a dialog in Chloe: endpoint, connection/database, the exact SQL (with values),
and the number of rows it affects, with **Deny** and **Approve**. For update/delete Chloe counts the matching rows
first; on Approve it runs the statement in a transaction and rolls back if the affected count differs from what was
approved. No answer within 2 minutes counts as Deny. The agent gets "declined by the user".
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
- No DDL, no raw write SQL in v1.
- MySQL, PostgreSQL and SQLite in v1; MongoDB and Redis later.

### Monitor (capability)
`list_sessions`, `server_status`, `server_variables` (read-only); `kill_session` (asks unless danger mode).

### Saved queries (capability)
`list_saved_queries`, `get_saved_query`, `save_query` (into the database's Queries folder), `open_query_tab`
(opens a query tab with the agent's SQL for the user to review and run; it never runs it).

### Create connections (capability)
`create_connection` (engine, host, port, user, database, options or a URL; SQLite file path) always asks.
New connections go into the "Created by AI" group; the creating endpoint gets the access set on the endpoint
(default Read); other endpoints see it as Off. `test_connection` tries settings without saving them.

### ER models (capability)
`list_models`, `get_model`, `create_model_from_database`, `edit_model`, `generate_sql` (DDL as text, never run).
Applying a model to a database stays a manual Synchronize to Database.

## Window (Tools > MCP Server…, and the titlebar MCP button)
A tab like the History Log:
- Toolbar: **Server on/off**, port, URL, and **Copy Config** (Claude Code / Claude Desktop / JSON).
- Left: endpoints list (**New Endpoint**, **Delete**). Right: the selected endpoint's name, enabled, token
  (Copy, Regenerate), danger mode, capabilities and the connection access tree. The split is draggable and remembered.
- **Activity** tab: time, endpoint, tool, connection/database, SQL, rows, outcome (ok, error, approved, denied).
  The last 1000 calls are kept in memory only.

## Delivery
1. Server, endpoints, data tools, approval, danger mode, activity, window.
2. Monitor, saved queries / query tabs, create connections.
3. ER models.
