# Chloe roadmap

Order reflects dependencies: later features build on the earlier ones. Specs for each area live in `docs/specs/`.

## Next up
1. **Explain / visual explain** for MySQL and PostgreSQL, with MongoDB's explain
2. Transfer and sync follow-ups: Data Transfer file target and per-table Advanced mode (rename, fields,
   filters); functions/procedures/events in transfer and structure sync; profiles for the sync tools;
   per-record exclusion in data sync; MongoDB → MongoDB; views between different
   engines (today only same-engine views are copied; the SQL needs translating)
3. **MongoDB, second pass**: aggregation pipeline builder, GridFS buckets, saved queries in MongoDB databases
4. **SQL Server and Oracle**, both in Docker
5. Polish: Redis pub/sub, cluster and sentinel; Form view and hex/image cell editors; preferences window
   (history logging on/off, autosave interval)

The MCP server (6b) waits until the user has discussed it.

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

## 6. Users & privileges ✅
- Users/roles, server privileges, object privileges (MySQL, PostgreSQL)

## 6b. MCP server (on hold until discussed with the user)
- Chloe as an MCP server for AI assistants: pick connections to expose, with
  per-table / per-column read and write permissions
- Structured tools (list/describe tables, select/insert/update rows) where
  Chloe builds the SQL, so table and column rules are enforceable; read-only
  access also runs in read-only transactions
- Optional: create a dedicated database user with matching GRANTs (uses the
  privileges feature) so the database itself enforces the policy

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
