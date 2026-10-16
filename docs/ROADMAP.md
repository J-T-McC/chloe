# Chloe roadmap

Order reflects dependencies: later features build on the earlier ones. Specs for each area live in `docs/specs/`.

## Next up
1. **BI, phase 3** ✅ (spec: `docs/specs/bi.md`): chart types, dynamic dates, parameters, interaction and Control
   charts, custom fields, joined tables, shapes and tile lock. Next for BI: MongoDB data sources (phase 4)
2. **Explain / visual explain** for MySQL and PostgreSQL, with MongoDB's explain
3. Transfer and sync follow-ups: Data Transfer file target and per-table Advanced mode (rename, fields,
   filters); functions/procedures/events in transfer and structure sync; profiles for the sync tools;
   per-record exclusion in data sync; MongoDB → MongoDB; views between different
   engines (today only same-engine views are copied; the SQL needs translating)
4. **MongoDB, second pass**: aggregation pipeline builder, GridFS buckets, saved queries in MongoDB databases
5. **SQL Server and Oracle**, both in Docker
6. Polish: Redis pub/sub, cluster and sentinel; Form view and hex/image cell editors; preferences window
   (history logging on/off, autosave interval)

MCP server (6b): Redis and MongoDB tools are done. Agents get per-endpoint instructions and a `guide` tool (SQL, writes,
reports, models, MongoDB, Redis) on how to work without hurting the server.

### BI fixes (found building the Uptime Command Center dashboard, 2026-10-15)
Export (`ExportHost.tsx`, `DashboardEditor.tsx`, `services/render.go`):
- [x] Feedback while exporting: the Save dialog comes first (nothing is drawn if it's cancelled), then
  "Exporting page n of m…" with Cancel, then "Saved <path>" with Show in Finder
- [x] Each page capture times out after 60s instead of stalling the export queue (MCP included)
- [x] A failed capture names its page instead of reading `[object Event]`
- [ ] Tall report pages (~2,100px+) still fail to capture; capture them in slices or lower the pixel ratio
- [ ] Exporting all pages fails as a whole when one page fails
- [x] Export re-runs every chart's query: live chart results are now cached and shared (see Performance)

Performance (`core/bipool.go`, `core/bicache.go`): a 7-page dashboard ran the server out of connections
- [x] Every chart opened (and logged in on) a new server connection, all at once with no limit: BI reads now share
  4 warm sessions per connection, queued
- [x] A read past its 30 s limit kept running on the server: it's now cancelled there
- [x] Every view re-ran every query: live chart results are kept 5 minutes and shared by pages, exports and MCP;
  Refresh runs each query again once. (Reading each source once and aggregating it locally was tried and
  dropped: SQLite's text rules differ from MySQL's and PostgreSQL's collations)
- [x] Charts drawn together with the same filters share one read of their source (one statement: the source as a
  CTE, a UNION ALL branch per chart); zoned date filters keep the column bare, so its index is used
- [ ] Skip auto refresh while a dashboard's tab isn't showing
- [ ] Drop a connection's cached sources when Chloe itself writes to it (grid edits, query tabs)

Charts (`options.ts`, `ChartView.tsx`, `styles.css`):
- [ ] `bar_line` / `stacked_bar_line` split by a series drop their secondary (line) fields: the series pivot only
  reads the value column
- [ ] The line in bar+line charts shares the bars' y-axis; give it its own (`yAxisIndex: 1`)
- [ ] No y-axis min/max for line, area and bar charts, so a 97–100% uptime line is flat on a 0–100% axis
- [ ] KPI tiles 2 columns wide (height S) clip the target meter and change; the figure doesn't shrink
- [ ] KPI change always says "vs target"; let it say "vs previous" when the target is a previous period
- [ ] Control checkbox list stays two narrow columns (second one clipped) however wide the tile is
- [ ] Donut/pie legend "right" overlaps the ring in narrow tiles; slice labels get cut off
- [ ] Treemap runs out of palette colours after ~8 categories and greys the rest
- [ ] Long hbar category labels are clipped at the start ("ptime-api.tysonmccarney.com") instead of ellipsized
- [ ] Table number format applies to every column; allow a format (e.g. percent) per column

## 1. Foundation ✅
Go core, MySQL/PostgreSQL drivers, connection tree, objects pane, data grid, table viewer, query editor with autocomplete.

## 2. Connections ✅
- [x] Groups (folders), New Group, move connections between groups; drag connections and groups to reorder them or move them in and out of groups (order saved)
- [x] Open/close databases (closed until opened; Close Database releases sessions)
- [x] Context menus: empty area (New Connection, Close All, Manage Connections, New Group, Refresh ⌘R), connection, database
- [x] Connection dialog per spec: Add To group, Advanced (auto connect, session limit, keepalive, initial queries, timeouts, compression, socket, PG parameters), custom database list with Auto Open, full SSL options, SSH password + key, Remarks
- [x] Presets for hosted services (Supabase, Neon, RDS, DigitalOcean, PlanetScale, TiDB) and paste-a-URL
- [x] Manage Connections window, duplicate, connection info, copy URL, import/export
- [x] Server monitor: sessions/processes (kill session or query), variables, status
- [x] Beautify SQL
- [x] Saved queries: a Queries folder per database (schema in PostgreSQL), Save ⌘S / Save As ⇧⌘S, rename,
  duplicate, delete, a Save / Don't Save prompt when closing an edited query
- [x] Open query tabs (saved or not) come back after a quit or crash; a query tab survives its connection
  being closed and reconnects
- [x] History Log (Tools menu, or History in a query tab): every statement run, filter by connection, errors
  only, search, open in a query
- [x] Security: passwords in the OS keychain (on macOS only Chloe's signature can read them); connections,
  saved queries, history and drafts encrypted with a keychain key; passphrase-encrypted connection exports;
  console histories leave out commands carrying passwords

## 3. Table designer ✅
- Columns: name, type, length/scale, nullable, default, auto increment/identity, comment, **collation/charset**, generated columns
- Keys: primary (including composite), unique, **foreign keys** (on update/delete, composite), checks
- **Indexes**: columns and order, unique, method (BTREE/HASH/GIN/GiST…), partial (PG), fulltext/spatial (MySQL)
- Triggers, table options (engine, charset, collation, row format, auto_increment, comment), partitions
- SQL preview before saving; generates ALTER for edits; rename-safe diffs
- Also: New/Drop/Rename/Truncate/Empty/Duplicate table, create/edit database (charset, collation), view and function editors

## 4. Import / export ✅
- Export: CSV, TSV, JSON, SQL INSERTs, Excel (xlsx), with column and row selection
- Import: CSV/JSON/xlsx with column mapping, append/update/replace modes
- Dump SQL file (structure, structure + data), Execute SQL file
- [x] Data Transfer (Tools menu, or a connection/database/schema's menu): tables and views between databases,
  schemas and connections, MySQL/MariaDB ↔ PostgreSQL ↔ SQLite. Types, defaults, auto increment, indexes,
  uniques, checks and foreign keys are converted; Navicat's options, summary, `[DTF]` message log and profiles

## 5. Data model / ER diagrams ✅
- [x] Reverse-engineer an ER diagram from a database
- [x] Model editing, forward engineer to SQL, sync model ↔ database
- [x] Model library: Chloe keeps models in its config folder and lists them under Model (rename, duplicate, delete,
  Show in Finder), with the database each was built from or synchronized to; outside files open and import

## 6. Users & privileges ✅
- Users/roles, server privileges, object privileges (MySQL, PostgreSQL)

## 6b. MCP server (spec: `docs/specs/mcp-server.md`)
- [x] Streamable HTTP on 127.0.0.1, endpoints with their own tokens, Off / Read / Read-write per group, connection and database
- [x] Data tools for MySQL, PostgreSQL and SQLite (list/describe, select_rows, read-only run_query, insert/update/delete rows)
- [x] Approval dialog for writes, opt-in danger mode, activity log, Tools > MCP Server… and the titlebar button
- [x] Create connections (capability), into a group whose access they inherit
- [x] Hardening from live testing: PostgreSQL array values, index columns and row estimates before ANALYZE; an
  unanswered approval is reported as a timeout, not a refusal; SQLite paths checked only after approval
- [x] Monitor (sessions, status, variables, kill with approval) and saved queries / query tabs (capabilities)
- [x] ER model tools (capability): list, read, create from a database or a copy, edit agent-created models, generate CREATE or sync SQL, open in Chloe
- [x] Stdio bridge (`Chloe mcp`) over a user-only Unix socket, so clients need no network port; HTTP can be switched off
- [x] Reports tools, phase 1: BI workspaces, read-only data sources, charts and their data (`docs/specs/bi.md`)
- [x] Reports tools, phase 2: dashboards and notes
- [x] export_dashboard: PNGs returned to the agent and saved, or a PDF
- [x] Reports tools: snapshots dropped (agents compare periods in live reports)
- [x] Redis tools (scan, get, read-only commands; set, edit, delete, expire with approval; Monitor)
- [x] MongoDB tools (find, aggregate, describe; insert, update, delete limited to approved `_id`s; Monitor)

Access stays per group, connection and database: no table or column permissions. Without a Chloe-managed
database user there's no complete way to enforce them while keeping `run_query` (views, functions, run-time SQL and
statistics catalogs get around name checks), so share connections that log in as a restricted user instead.

## 7. More engines
- [x] Redis: key browser, typed value editors, TTL, console, server monitor (pub/sub, cluster and sentinel still to do)
- [x] SQLite: open or create database files, attach more files, tables/views/indexes/triggers,
  table designer (ALTER where SQLite allows it, otherwise a table rebuild that keeps rows,
  indexes, triggers and foreign keys), view designer, dump/execute SQL, import/export
- [x] MongoDB: standalone / replica set / sharded / SRV connections with password, LDAP or X.509 auth,
  TLS and SSH; database and collection tree with sizes; collection viewer (grid, tree and JSON views,
  filter / sort / projection, paging, type-preserving cell edits, document editor); query tab and
  MongoShell console running mongosh-style JavaScript (goja) with cursor paging; completion for
  collections, methods, operators, stages and sampled field names; collection designer (indexes,
  validation, capped and time series options) and view designer; server monitor (currentOp, killOp,
  parameters, serverStatus); mongodb:// URLs; export (JSON, JSON Lines, CSV) and import with
  append, append/update and copy modes; users and roles (built-in roles view-only)
  - still to do: transfer between connections, pipeline builder, explain, GridFS
- SQL Server, Oracle (each must be testable in Docker; Snowflake is out of scope)

## 8. Advanced
- [x] Structure Synchronization (MySQL/MariaDB, PostgreSQL): tables and views compared, results by object type
  with checkboxes, DDL comparison, per-object and full deployment script, deploy as a job with a message log
- [x] Data Synchronization (MySQL/MariaDB, PostgreSQL, SQLite): mapping (target table, key, condition per table),
  insert/update/delete/same counts with per-action checkboxes, side-by-side record view with changed cells,
  deployment script ordered by foreign keys, deploy in a transaction
- Backup/restore, scheduled automation
- Query builder, explain/visual explain, find in database
- Grammar-based (ANTLR) autocomplete layer, preferences window
- [x] Value panel for viewing and editing cell text/JSON
- Form view, hex/image cell editors, FK value picker

## 9. BI (spec: `docs/specs/bi.md`)
- [x] Charts and MCP core: BI library, data sources (table/view/query, live/archive), chart designer, Create Chart from
  the grid and from query results, Present; MCP Reports tools for workspaces, data sources, charts and their data
- [x] Time zone per data source (bucket and filter dates in a named zone)
- [x] Dashboards: pages (16:9 slides or scrolling reports) of charts and Markdown on a 12-column grid, auto refresh,
  present; MCP tools to build them, refresh them and write notes on them
- [x] Export: PNG per page and PDF of every page, in the designer and over MCP (agents get the images to check pages)
- [x] Reports: PNG/PDF export (snapshots dropped; print only if asked for)
- [x] HTML export: one self-contained file with the viewer, pages and data; opens offline, tooltips and page tabs
  work, clicks highlight (Export menu and `export_dashboard` `format: html`)
- [x] Chart types: Waterfall, Tornado, Treemap, Scatter, Heatmap, Trend, KPI, Gauge, Pivot Table
- [x] Dynamic date filters (this/last/next period, period to date, last n quarters)
- [x] Parameters ([$name] in queries as bound values, filters, titles and text; dashboard controls; MCP)
- [x] Dashboard interaction (click to filter or highlight) and Control charts
- [x] Custom fields (calculated, mapped, concatenated, type-changed, custom-sorted) and joined tables
- [x] Dashboard shapes and tile lock
- [ ] MongoDB data sources
