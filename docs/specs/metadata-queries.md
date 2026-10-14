# Metadata queries — what to load, per DB (Phase 1: MySQL/MariaDB, PostgreSQL)

Source: SQL fragments in Navicat's core library (`libcc.dylib`), found with `strings`. These are reference notes on **which catalog sources Navicat uses and when**, written in our own words. Chloe writes its own queries.

## Loading strategy (observed)
- **Lazy, per level.** The connection loads databases. Expanding a database loads its object names by type. Columns, indexes and FKs load only when needed: designer, info pane, completion after a dot, or the "show objects under table" tree option.
- **Two paths for MySQL**: `SHOW …` statements by default, with an `information_schema` alternative. The connection's Compatibility dialog can force either path for listing databases and views. Chloe: default to `information_schema`, which is faster on large schemas and easier to parse, and fall back to `SHOW` on permission errors or very old servers.
- **Session probes on connect**: server version, `lower_case_table_names` (skippable via compatibility override), `sql_mode` (for ANSI_QUOTES and quoting), default storage engine, NDB support. PG: `server_version_num`, `search_path`, and the current database.
- **Code-completion cache**: schema-wide name lists (tables+views with type, routines, columns with data type) fetched in bulk per schema. "Auto update code completion info" refreshes it. "Include system objects" adds system schemas.
- **Object DDL**: MySQL uses `SHOW CREATE <TYPE> name`, and Navicat **parses** that DDL client-side to get FKs, partitions, and routine params/body. PG rebuilds DDL from catalog columns plus `pg_get_*def` functions.

## MySQL / MariaDB
| Need | Navicat's source (observed) | Chloe plan |
|---|---|---|
| Server info | `SELECT VERSION()`; `SHOW VARIABLES LIKE 'lower_case_%'`, `'sql_mode'`, `'default_storage_engine'` / `'storage_engine'`, `'innodb_default_row_format'`; NDB check via `information_schema.ENGINES` | same set, one round trip where possible |
| Databases | `SHOW DATABASES` or `information_schema.SCHEMATA` (name, default charset, collation) | `SCHEMATA` |
| Charsets/collations | `information_schema.character_sets`, `.collations` | same (for designers) |
| Tables vs views vs sequences | `SHOW FULL TABLES WHERE Table_type …` (separate calls for tables, views, MariaDB SEQUENCE) or `information_schema.TABLES (schema, name, type)` | `information_schema.TABLES` with type |
| Table details (object list "Detail") | `SHOW TABLE STATUS [LIKE …]` | `information_schema.TABLES` (rows, data/index length, engine, auto_inc, collation, create/update time, comment) |
| Columns | `SHOW FULL COLUMNS FROM t`; bulk `information_schema.COLUMNS (schema, table, column, type)` for completion | `information_schema.COLUMNS` per schema for completion; per table for designers |
| Indexes | `SHOW INDEX FROM t` | `information_schema.STATISTICS` |
| Foreign keys | parsed from `SHOW CREATE TABLE` | `KEY_COLUMN_USAGE` + `REFERENTIAL_CONSTRAINTS` (needed for join completion) |
| Routines | `SHOW PROCEDURE STATUS WHERE Db=…`, `SHOW FUNCTION STATUS WHERE Db=…`, or `information_schema.ROUTINES` | `ROUTINES` (+ `PARAMETERS` for signatures) |
| Triggers | `information_schema.TRIGGERS WHERE TRIGGER_SCHEMA=…` | same |
| Events | `information_schema.EVENTS` | same |
| View updatability | `information_schema.VIEWS.IS_UPDATABLE` | same (decides grid editability) |
| Partitions, tablespaces | `information_schema.PARTITIONS`, `.FILES` | later phase |
| DDL | `SHOW CREATE TABLE/VIEW/FUNCTION/PROCEDURE/EVENT/TRIGGER/DATABASE` | same |
| Users/privileges | `information_schema.USER_PRIVILEGES`, `TABLE_PRIVILEGES`, `SHOW GRANTS FOR` | later phase |
| Processes / status | `SHOW FULL PROCESSLIST`, `SHOW GLOBAL STATUS`, `SHOW GLOBAL VARIABLES` | server monitor, later |
| Query profiling | `information_schema.PROFILING` | optional |

## PostgreSQL
| Need | Navicat's source (observed) | Chloe plan |
|---|---|---|
| Server info | `SHOW server_version_num`, `SHOW search_path`, `SELECT VERSION()`; `pg_settings` (e.g. block_size) | same |
| Databases | `pg_database` (name, acl, encoding via `pg_encoding_to_char`, `datistemplate`, `datallowconn`) | exclude templates and `datallowconn=false` |
| Schemas | `pg_namespace` + owner (`pg_get_userbyid`), ACL, description (`obj_description`) | same; hide `pg_*`/`information_schema` unless "system objects" is on |
| Relations | `pg_class` joined to `pg_namespace`, filtered by `relkind` in r (table), v (view), m (matview), S (sequence), f (foreign), p (partitioned); owner, ACL, description, clustered index, inheritance (`pg_inherits`) | same, one query per database grouped by schema |
| Columns | `information_schema.columns` joined to `pg_attribute`/`pg_attrdef`/`pg_type` for exact types, defaults, collation, identity/serial (via `pg_depend` + `pg_sequence`) | `pg_attribute` + `format_type(atttypid, atttypmod)` + `pg_get_expr(adbin)`; skip dropped columns and attnum ≤ 0 |
| Matview columns | `pg_attribute` where relkind 'm' (not in information_schema) | same |
| Constraints (PK/FK/unique/check/exclude) | `pg_constraint` joined to its table, index, and referenced table/schema | same; `pg_get_constraintdef` |
| Indexes | `pg_index` joined to index/table classes and `pg_am` (method) | same; `pg_get_indexdef` |
| Functions/procedures | `pg_proc` + `pg_namespace` + return type + `pg_language`; trigger-function flag (`prorettype = 'trigger'`); arg defaults via `pg_get_expr(proargdefaults)` | same; `pg_get_function_identity_arguments`, `pg_get_functiondef` for DDL |
| Aggregates | `pg_aggregate` + `pg_proc` (state/final funcs) | later |
| Triggers | `pg_trigger` + table + trigger function; events decoded from the `tgtype` bitmask (INSERT 4, DELETE 8, UPDATE 16, TRUNCATE 32) | same; exclude internal (`tgisinternal`) |
| Rules | `pg_rewrite` except `_RETURN`, `pg_get_ruledef` | later |
| Types/domains/enums/ranges | `pg_type` (+ `pg_range`, `pg_enum`, domain constraints) | names for completion in Phase 1; designers later |
| Sequences | `pg_class` relkind S + `pg_sequence` | same |
| Extensions, casts, operators, FDW/foreign servers, languages | `pg_extension`, `pg_cast`, `pg_operator`, `pg_foreign_server`, `pg_language` | later |
| Dependencies | `pg_depend` | later (drop-cascade previews) |
| Locks / activity | `pg_locks` + `pg_class`, `pg_stat_activity` | server monitor, later |

## Chloe metadata API (Go → frontend)
- `ListDatabases(conn)`
- `ListSchemas(conn, db)`
- `ListObjects(conn, db, schema, kinds[])`
- `ObjectDetails(conn, ref)`
- `ListColumns(conn, ref)`
- `ListIndexes(conn, ref)`
- `ListForeignKeys(conn, schema)`
- `GetDDL(conn, ref)`
- `CompletionSnapshot(conn, db, schema)`: bulk names, columns, routines and FKs for one schema, cached and versioned
