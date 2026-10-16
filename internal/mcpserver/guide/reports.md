# Reports (BI workspaces, charts and dashboards)

## The order to build in
1. Explore the data first (`list_tables`, `describe_table`, a few `run_query` checks) so you know the tables, their
   sizes and their indexes.
2. `create_workspace`, then `create_parameter` for anything the user should be able to change (a window in days, a
   scope).
3. Data sources: write each one's SQL, check its plan with EXPLAIN (see "Data sources that stay fast"), then
   `create_data_source`. It returns the fields Chloe found and their types.
4. Charts on those sources: `create_chart`, then `get_chart_data` to check the numbers are right.
5. `create_dashboard`, then `export_dashboard` with PNG to look at every page: text that's cut off, charts too
   small for their labels, empty space. Fix and look again.
6. `set_notes` for what you found, and tell the user where the dashboard is (`open_dashboard` brings it up).

## How charts read a data source
Each chart is a query with the source inside it, grouped and filtered by the database (so there's no need to
pre-aggregate for Chloe):

    SELECT <buckets>, SUM(<value>) FROM (<the source's SQL>) AS s WHERE <chart filters> GROUP BY 1

On MySQL and PostgreSQL, charts on a query (or joined tables) that are drawn together and have the same chart
filters share one read of it: the source and those filters run once, and each chart groups from that. A chart with
filters of its own reads the source again, once per set of filters, so a source that takes 3 seconds with charts in
four different filter sets gives the server 12 seconds of work. Filters most charts share belong on the source.
Results are kept for 5 minutes and shared by the pages, exports and these tools; Refresh and the dashboard's auto
refresh read again.

## Data sources that stay fast
- Check the plan of every source query with EXPLAIN before saving it (see the `sql` topic), with realistic parameter
  values. If it scans a big table, find a filter that uses an index, or tell the user which index would help (and
  give the CREATE INDEX). Don't save a slow source and hope.
- Put the time window inside the source, on the indexed column, with a parameter:
  `WHERE e.created_at > NOW() - INTERVAL [$days] DAY` (PostgreSQL: `now() - make_interval(days => [$days])`; SQLite:
  `created_at > datetime('now', '-' || [$days] || ' days')`). A column computed in the SELECT (`… AS in_window`,
  `CASE … AS period`) and filtered by a chart can't use an index, so the database reads every row to evaluate it.
- Group to the grain the charts need: a daily summary (one row per day, or per day and site) instead of every raw
  event, when no chart needs the events. Don't group further than that: charts can only filter and split by fields
  the source still has.
- Share sources: several charts on one cheap source are fine; several sources that each scan the same big table are
  not. Combining many unrelated subqueries into one source makes every chart on it pay for all of them.
- Archive mode reads the source once into a snapshot that charts read locally. Use it for history that doesn't need
  to be live (the user refreshes it in Chloe), not to hide a slow live query. Text in archived sources groups and
  compares by SQLite's rules, not the database's collation.
- `[$name]` parameters are bound as values. They can't stand for table or column names.

## Charts
- Field names on the shelves are the source's field names (`get_workspace` lists them). Dates take a bucket.
- `top` with `others` keeps a long category list readable. Use a table or pivot when the user needs exact values.
- Filters on a chart apply to that chart only; filters on the source apply to every chart on it.
- A day, week or month that's still going looks like a drop at the end of a trend. Stop the window at the start of
  the current period, or say in the title that the last one is partial. Check for rows dated in the future, too.
- Colours go by position (the first slice or series gets the first colour, also with `properties.colors`), so a
  category keeps its colour across charts only when they order it the same way: sort them alike. Keep labels short
  enough for the tile: donuts with long category names get cut off; an hbar reads better.
- Lines with the same values hide each other; a stacked area or a table shows them.
- `get_chart_data` returns what the chart shows, as unformatted numbers; use it to check totals against a
  `run_query` of your own.

## Dashboards and notes
- Pages are rows of tiles across 12 columns, with Markdown text tiles; Chloe lays them out. Report pages grow
  downward; 16:9 pages are slides and must fit.
- You can edit only what you created (`createdBy: mcp:<endpoint>`). The user's charts can go on your dashboards, and
  `set_notes` can add your notes to theirs.
- Notes are for findings: what changed, why it matters, what to do. Numbers in notes go stale; say when you read
  them.
- `export_dashboard` needs Chloe's window open. PDF and HTML are for the user to keep or share; the PNGs are for you
  to check the layout.
