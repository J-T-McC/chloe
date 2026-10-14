# Chloe roadmap

Order reflects dependencies: later features build on the earlier ones. Specs for each area live in `docs/specs/`.

## 1. Foundation ✅
Go core, MySQL/PostgreSQL drivers, connection tree, objects pane, data grid, table viewer, query editor with autocomplete.

## 2. Connections ✅
- [x] Groups (folders), New Group, move connections between groups
- [x] Open/close databases (closed until opened; Close Database releases sessions)
- [x] Context menus: empty area (New Connection, Close All, Manage Connections, New Group, Refresh ⌘R), connection, database
- [x] Connection dialog per spec: Add To group, Advanced (auto connect, session limit, keepalive, initial queries, timeouts, compression, socket, PG parameters), custom database list with Auto Open, full SSL options, SSH password + key, Remarks
- [x] Presets for hosted services (Supabase, Neon, RDS, DigitalOcean, PlanetScale, TiDB) and paste-a-URL
- [x] Manage Connections window, duplicate, connection info, copy URL, import/export
- [x] Server monitor: sessions/processes (kill session or query), variables, status
- [x] Beautify SQL

## 3. Table designer ✅
- Columns: name, type, length/scale, nullable, default, auto increment/identity, comment, **collation/charset**, generated columns
- Keys: primary (including composite), unique, **foreign keys** (on update/delete, composite), checks
- **Indexes**: columns and order, unique, method (BTREE/HASH/GIN/GiST…), partial (PG), fulltext/spatial (MySQL)
- Triggers, table options (engine, charset, collation, row format, auto_increment, comment), partitions
- SQL preview before saving; generates ALTER for edits; rename-safe diffs
- Also: New/Drop/Rename/Truncate/Empty/Duplicate table, create/edit database (charset, collation), view and function editors

## 4. Import / export ✅ (except data transfer between connections)
- Export: CSV, TSV, JSON, SQL INSERTs, Excel (xlsx), with column and row selection
- Import: CSV/JSON/xlsx with column mapping, append/update/replace modes
- Dump SQL file (structure, structure + data), Execute SQL file
- Data transfer between connections

## 5. Data model / ER diagrams
- Reverse-engineer an ER diagram from a database
- Model editing, forward engineer to SQL, sync model ↔ database

## 6. Users & privileges
- Users/roles, server privileges, object privileges (MySQL, PostgreSQL)

## 7. More engines
MongoDB, Redis, SQLite, SQL Server, Oracle, Snowflake

## 8. Advanced
- Structure sync and data sync (diff + deploy script)
- Backup/restore, scheduled automation
- Query builder, explain/visual explain, find in database
- Grammar-based (ANTLR) autocomplete layer, preferences window
- Form view, cell editors (text/hex/image/JSON), FK value picker
