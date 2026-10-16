# SQL databases (MySQL, MariaDB, PostgreSQL, SQLite)

## Finding your way
- `list_connections` shows each connection's engine and access; `databases` lists the ones whose access differs.
- `list_databases`, then (PostgreSQL) `list_schemas`, then `list_tables`, which estimates each table's rows. Check
  the size before reading a table: a 50-row lookup table and a 50-million-row event log need different queries.
- `describe_table` gives columns, the primary key, indexes and foreign keys. Use the foreign keys to write joins
  instead of guessing them from names, and the indexes to decide how to filter.
- `get_ddl` returns the CREATE statement of a table, view, routine or trigger. Read a view's SQL before building on
  it: a view over a slow query is a slow query.

## Engines
- MySQL and MariaDB: a database is a schema. Quote names with backticks. `run_query` isn't offered on a connection
  where some databases are hidden from this endpoint, because a MySQL query can name any database; use
  `select_rows` there.
- PostgreSQL: pass `schema` (default `public`). Quote names with double quotes; unquoted names are folded to lower
  case.
- SQLite: the database is `main` (attached files have their own names). Types are loose: dates are usually text,
  and a column may hold mixed types.

## Queries that are kind to the server
These are often production servers that other people depend on. Each statement has 30 seconds and runs in a
read-only transaction.
- Let the database filter, join, group and sort. `run_query`, `select_rows` and the MongoDB reads return at most
  1000 rows (`truncated` says when there were more), so totals worked out from returned rows are wrong once a result
  is cut off.
- Filter on indexed columns, with the column alone on one side: `created_at >= '2026-01-01'` can use an index;
  `DATE(created_at) = '2026-01-01'`, `YEAR(created_at) = 2026` and a filter on a CASE expression can't.
- Look at the plan before running a query over a big table, and always before saving one that will run again (a
  report data source, a saved query):
  - MySQL 8: `EXPLAIN FORMAT=TREE …` for the plan, `EXPLAIN ANALYZE …` to run it and see where the time went.
    MariaDB: `EXPLAIN …` (its `ANALYZE` statement isn't accepted by `run_query`).
  - PostgreSQL: `EXPLAIN …`, or `EXPLAIN (ANALYZE, BUFFERS) …` to run it.
  - SQLite: `EXPLAIN QUERY PLAN …`.
  A full scan (`Table scan`, `Seq Scan`, `SCAN table`) of a big table, or an index lookup that reads far more rows
  than it keeps, means a missing index.
- When an index is missing, tell the user which table and columns, why, and the statement, e.g.
  `CREATE INDEX monitor_events_category_status_created_at ON monitor_events (category, status, created_at)`. Don't
  create it yourself (see "Schema changes"), and don't work around it by reading more rows.
- Avoid per-row work over whole tables (regular expressions, functions on every row) when a filter can narrow the
  rows first.

## Schema changes
Chloe doesn't run DDL (CREATE, ALTER, DROP, TRUNCATE, GRANT…) over MCP. Write the statements and hand them to the user:
`open_query_tab` puts them in a query tab in Chloe for the user to review and run, and `save_query` keeps them in the
database's Queries folder (both need the endpoint's Saved queries capability). Without it, give the user the SQL in
your reply. Say what each statement does and what it locks or rewrites (a big ALTER can lock a table for minutes).
