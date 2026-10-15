# BI (charts, dashboards, reports) — spec

Navicat's BI turns tables and queries into charts and multi-page dashboards that can be presented, printed and exported.
This spec takes its structure and workflow from what Navicat Premium 18.0.3 ships: the bundled help
(`en.lproj/Navicat Help/bi*.html`), the sample workspace (`Resources/charts/Sample.nbidemo`, a tar of gzipped JSON
objects plus SQLite snapshots and images) and strings from the `Charts*` nibs and binary. Agent reporting over MCP,
and Markdown notes are Chloe's own additions, from a discussion with the user (2026-10-14). Chloe aims to
look better than Navicat: a modern palette and type scale instead of fixed Arial, tiles that snap to a grid by default,
and a polished HTML export.

## Objects
A **workspace** holds four kinds of object. Each has a name, created and modified times, and Created By.
- **Data source**: rows from one connection (see Data sources). The fields of its result can be used in charts.
- **Chart**: one data source plus a chart type, fields placed on the type's shelves, filters, sort and properties.
- **Dashboard**: one or more pages of charts, text, images and layout tiles.
- **Parameter**: a named value, global to the workspace, that data sources, charts and dashboards refer to as `[$name]`.

A chart uses exactly one data source. When a chart changes, every dashboard that contains it shows the change
(dashboards refer to charts; they don't copy them). Every object's Properties pane has **Using** (what it depends on) and
**Used By** (what depends on it). Deleting an object that something uses asks first and names the objects that use it.

## Library and window
- Workspaces live in `bi/` in Chloe's config folder, one `.chloebi` file each (JSON), named like models
  (made safe for a file name, numbered when taken). Navicat keeps `.nbi` files in its profile folder.
- **BI** in the main toolbar shows the library in the Objects tab, like Model: Name, Data Sources, Charts, Dashboards,
  Created By, Modified, with a detail panel. Toolbar: Open, New Workspace, Delete, Open External…, Refresh, Filter.
  Row menu: Open, Rename…, Duplicate, Delete…, Save As External File…, Show in Finder.
- Opening a workspace opens a **workspace tab**:
  - Toolbar: New Data Source, New Chart, New Dashboard, New Parameter, Theme.
  - Content pane with quick filters (All, Data Source, Chart, Dashboard, Parameter) and Detail view (sortable
    columns: Name, Type, Chart Type, Data Source, Created, Modified, Data Refresh Time) or Icon view (thumbnails).
  - Properties pane (General, Using, Used By). View > Show Properties toggles it.
  - Opening an object opens it in its own Chloe tab, titled `<object> — <workspace>`.
- **Open External…** copies a `.chloebi` file into the library (its archive sources read again on refresh);
  **Save As External File…** writes the definitions (no rows) to a file you pick.
- Agent-created objects show Created By "AI (endpoint)". A workspace an agent created is agent-owned as a whole.

## Data sources
- **New Data Source** asks for a name and a connection from the tree (MySQL/MariaDB, PostgreSQL, SQLite;
  MongoDB in a later phase). Chloe doesn't import files or ODBC sources: use Import to load a file into SQLite first.
- The designer has the connection tree on the left (databases, tables, views, saved queries), a design pane, a preview
  grid and Properties (Remarks, Used By). Every split is draggable and remembered.
- A data source has one **dataset**, which is one of:
  - **Table or view**: drag it onto the design pane.
  - **Query**: **New Data Source Query** opens an SQL editor (Chloe's editor, with completion); **Import** copies a
    saved query's SQL. Unlike Navicat, the query is stored in the data source, so later edits to the saved query don't
    change it.
  - **Joined tables** (phase 3): drag a second table onto the first and pick the join type and fields, as in Navicat.
    Joins stay within one database. Cross-connection joins are out: they would need a local copy of both tables.
- **Mode**:
  - **Live**: rows are read when a chart is shown or refreshed.
  - **Archive**: rows are read once into a snapshot, and charts read the snapshot until **Refresh Data**. Navicat
    stores these as SQLite files in the workspace; Chloe does the same.
- **Apply and Refresh Data** runs the source and fills the preview. The preview shows field names and types
  (String, Number, Date/Time), and column visibility can be toggled.
- **Filter / Sort / Projection** panes on the source, as on Navicat's toolbar:
  - Filter operators: `= != < <= > >=`, contains, does not contain, begins with, does not begin with, ends with,
    does not end with, is null, is not null, is empty, is not empty, is between, is not between, is in list,
    is not in list.
  - Conditions join with and/or, can be grouped with brackets, and can be toggled off or negated.
  - A value can be a literal, a parameter, or a dynamic date (this/last/next day, week, month, quarter, year, and
    year-to-date style ranges).
  - Projection includes or excludes listed fields.
- **Custom fields** (phase 3): Type-Changed, Concatenated, Mapped (value → value rules, with case sensitivity and
  an action for unmapped values), Custom-Sorted (order taken from another field or a hand-made order) and Calculated.
- A broken source (missing connection, table or column) shows "Due to issues detected, editing of this data source is
  disabled." with the reason, and charts using it show the error in place of the chart.

### How data is read
- Chloe builds every chart query itself. It never runs the shelves as raw rows plus client-side aggregation:
  `SELECT <dimensions>, <aggregates> FROM (<source>) AS s WHERE <filters> GROUP BY … ORDER BY … LIMIT …`.
  - Field names are checked against the source's result columns and quoted for the dialect.
  - Literals and parameter values are **bound**, never pasted into the SQL. A parameter used inside a source query is
    a bound placeholder too.
- Date buckets (Year, Quarter, Month, Week, Day, Hour, Minute, Second and the Extract forms) become dialect
  expressions: `date_trunc`/`extract` on PostgreSQL, `DATE_FORMAT`/`EXTRACT` on MySQL, `strftime` on SQLite.
- Archive snapshots are SQLite, so they are aggregated with the same SQLite generator.
- **Every data source runs read-only**, whoever made it. The source must be a single statement starting with
  SELECT, WITH, VALUES or TABLE. Chloe runs it in a read-only transaction, the same check as MCP `run_query`.
  Opening a dashboard can never change data.
- Limits: 30 s per query (configurable per source). A chart query returns at most 10 000 rows; past that the chart
  says it is showing the first 10 000 and suggests a coarser bucket or Top N. Archive snapshots hold up to 1 000 000
  rows.

## Charts
- **New Chart** asks for the data source and a name. The designer has the data source's fields (left), a chart
  type picker and **shelves** (middle), the preview, and Properties (Properties, Using, Used By).
  Present ⌘↵ shows the chart full screen; Esc exits.
- Fields are dragged onto shelves ("Drag Fields Here"). The shelves are **Dimension** (category or X axis),
  **Series** (split into colours or stacks), **Value** (one or more measures), **Secondary Value** (the line in bar +
  line, or the bubble size in scatter) and **Target** (KPI, gauge, scatter Y). Which shelves a type shows, from the
  sample workspace:

| Type | Shelves |
|---|---|
| Bar (vertical, vertical stacked, horizontal, horizontal stacked) | Dimension, Series, Value |
| Line, Area, Stacked Area | Dimension, Series, Value (one or more) |
| Bar and Line, Stacked Bar and Line | Dimension, Series, Value (bars), Secondary Value (lines), on one value axis |
| Waterfall | Dimension, Value |
| Tornado | Dimension, Series (exactly two values), Value |
| Pie, Donut, Treemap | Dimension, Value |
| Scatter | Dimension, Series, Value (X), Target (Y), Secondary Value (size) |
| Heatmap | Dimension, Series, Value (intensity) |
| Value | Value |
| Trend | Dimension (date), Value: the latest value and its change from the one before |
| KPI | Value, Target: value against target with a progress bar |
| Gauge | Value, Target (min/max on the arc) |
| Table | Value (columns), with totals |
| Pivot Table | Dimension (rows), Series (columns), Value |
| Control | Dimension: a list, range or combo box that filters other charts on a dashboard |

- Each field on a shelf has a menu:
  - **Set Alias**.
  - **Aggregate**:
    - Number: Sum, Average, Count, Count (Distinct), Min, Max, Median (PostgreSQL live sources only: MySQL and
      SQLite have no median aggregate).
    - Date/Time: Count, Count (Distinct), Year, Quarter, Month, Week, Day, Hour, Minute, Second, and Quarter / Month /
      Week / Day / Hour / Minute / Second (Extract).
    - Text: First, Last, Count, Count (Distinct).
  - **Add Range**: fixed bin size with a Calculate button, or custom stop values.
  - **Value Operation**: None, Cumulative, Subtract (difference from the previous value).
  - **Display Filter**.
  - **Sort**.
  - **Remove**.
- **Data Filter** (filters the source rows for this chart) and **Display Filter** (filters what's drawn, including
  ⌃-click on a series or point) panes; a **Sort** pane.
- Properties, common to all types:
  - **General**: background, border, title (text, wrap, font, position, alignment), remarks.
  - **Legend**: show, position, alignment, label colour.
  - **Data**: data labels (value/percentage, position, font, positive/negative colours), Show only top #,
    Fill missing values, Group values less than (a percentage or fixed threshold) into Others.
  - **Colour**: palette, or a fixed colour per series value.
  - **Number format**: prefix, unit (K/M/B or custom divider + suffix), thousand and decimal separators, decimal
    places, negatives.
  - **Date format**: presets, or custom with Navicat's specifiers (`D DD M MM MMM MMMM MMMMM YY YYYY h hh H HH m mm S SS wd Wd WD w WW q QQ p`).
  - **Axes**: title, labels, rotation, interval, min/max, gridlines; **Reference lines** (constant, average, median…).
  - Per type: bar width, 100 % stacked, line smoothing, markers, pie label placement and slice separators, table
    stripes and gridlines, data bars, totals, gauge arcs.
- Navicat's sample workspace sets most properties per chart. Chloe keeps the same properties but takes defaults from
  the workspace theme, so a new chart already looks finished.

## Dashboards
- **New Dashboard** asks for a name. Layout: a **Pages** pane with thumbnails (+, Duplicate Page, Delete Page), the
  toolbar (Add Chart, Add Text, Add Image, Add Shape, Add Tile Layer, Object List, Refresh Data, Present, Focus Mode),
  the design canvas, and Properties (Dashboard Properties, Using). All splits are draggable and remembered.
- **Page size**: 16:9 (1920 × 1080, the default), 4:3, A4/Letter portrait and landscape, or custom. Fit page on
  or off. Per-page background colour or image (fill, fit, tile; opacity) and a page note.
- **Add Chart** shows the workspace's charts as thumbnails (with a filter) to drag onto the page. Per placed chart:
  position, size, Target Charts (which charts it may control) and Chart Interaction (as an interaction control;
  related charts None / Highlight / Filter).
- **Text label**:
  - Settings: caption, alignment, padding, font, bold/italic, background, URL, and page navigation (clicking opens
    another page).
  - Chloe addition: a caption can be **Markdown** (headings, lists, bold, links, tables). This is how an agent's notes
    appear (see MCP). Parameters in a caption are replaced with their values: `[$this_year]`.
- **Image**: PNG/JPG, opacity, original size, URL and page navigation. Images are stored in the workspace.
- **Shape** (phase 3): line, arrow, rectangle, ellipse, triangle, right triangle, right arrow, database, cloud, server,
  desktop, mobile, person. Settings: colour, border, line width, dash, caps and joins, and arrow ends.
- **Tile layer**: a container whose children are laid out Horizontal, Vertical or Floating, with distribution None,
  Fill Equally or Fill Proportionally.
- **Arrange**:
  - Bring to Front / Send to Back.
  - Align (left, centre, right, top, middle, bottom).
  - Distribute (horizontal, vertical).
  - Group: link scrolling, link highlight, show hints when highlighting.
  - Lock / Unlock.
- **View menu**: Show Grid, Snap to Grid (on by default in Chloe), and Alignment Guide (object centres, edges,
  relative sizing, relative spacing). Arrow keys nudge the selection; ⇧-drag keeps proportions.
- **Refresh Data** refreshes every chart on the page. Its menu sets **Auto Refresh**: off, 1, 5, 15 or 30 min, or
  1 h, while the tab is open.
- **Interaction**:
  - Charts on a page that share a data source cross-filter or cross-highlight when you click a point, as set in
    Dashboard Properties.
  - Control charts filter their target charts.
  - **Present** (full screen, arrow keys change page, Esc exits) keeps interaction, the per-chart sort button, and
    parameter controls (⌃-click > Modify Parameter Value; a range parameter gets a slider).
- **File > Print…** and **File > Export To**: PDF, PNG, SVG, JPG, one file per page or one PDF for all.
  Chloe adds **HTML**: a single self-contained file with the charts drawn and interactive, data as of the export,
  no connection needed to view it.

## Parameters
- **New Parameter**:
  - Name; where it may be used (data source query, filter, custom field; chart data filter, display filter, title;
    dashboard label caption, label URL, image URL, shape URL).
  - Data type.
  - Allowed values: Any, List (value and optional description) or Range (min, max, step).
  - Current value.
- Inserted with the parameter button next to each field that supports them, or typed as `[$name]`.

## Theme
- A workspace has its own chart theme (Light, Dark, and later more), independent of Chloe's appearance, so
  exported reports look the same everywhere. **Theme** in the workspace toolbar switches it for every chart and
  dashboard.
- Chloe's themes set the palette (colour-blind safe, 8 categorical colours plus sequential and diverging scales),
  fonts, title sizes, gridline weight and tile corner radius. A chart's own properties override the theme.

## Comparisons (no snapshots)
Snapshots were planned and dropped (2026-10-15): an agent compares periods in one live report (this month beside
last month, a Trend chart's change), so nothing static is kept. For data that's overwritten rather than logged
(stock levels, current statuses), an agent can append the values to a table on a schedule, and reports chart that
table like any other.

## MCP (capability "Reports")
This is the main reason for the feature. An agent can build a report from what it finds in a database, save it in
Chloe for the user to open, and come back on a schedule to refresh it and write down what changed. The capability is
opt-in per endpoint like the others (`docs/specs/mcp-server.md`).

| Tool | Access | Arguments |
|---|---|---|
| `list_workspaces` | — | — |
| `get_workspace` | — | `workspace` → its data sources, charts, dashboards, parameters (definitions, not data) |
| `create_workspace` | — | `name` |
| `create_data_source` | Read | `workspace, name, connection, database, schema?` and `table` or `sql`; `mode?` (live / archive) |
| `create_chart` | — | `workspace, name, data_source, type, shelves, filters?, sort?, top?, properties?` |
| `edit_chart` | — | `workspace, chart, …` the same fields, replaced if given |
| `get_chart_data` | Read on its sources | `workspace, chart, parameters?` → the chart's aggregated rows |
| `create_dashboard` | — | `workspace, name, pages` |
| `edit_dashboard` | — | `workspace, dashboard, pages?, rename?` |
| `refresh_dashboard` | Read on its sources | `workspace, dashboard, parameters?` → each chart's rows and the page text |
| `set_notes` | — | `workspace, dashboard, page?, markdown` |
| `export_dashboard` | Read on its sources | `workspace, dashboard, format` (pdf / png / html) |
| `open_dashboard` | — | `workspace, dashboard` (Chloe comes to the front) |
| `open_chart` | — | `workspace, chart` (Chloe comes to the front) |
| `edit_data_source` | Read | `workspace, data_source, rename?, table? or sql?, mode?, timezone?, filters?` (agent-made only) |
| `delete_chart` | — | `workspace, chart` (agent-made charts only) |
| `delete_data_source` | — | `workspace, data_source` (agent-made, once no chart uses it) |

- **Ownership**, as with models:
  - An agent creates objects in any workspace, but edits only objects it created (`createdBy: "mcp:<endpoint>"`).
  - The user's own objects are read, copied and used (an agent's dashboard can show the user's chart), never
    written.
  - Agents delete only charts and data sources they created (a data source once no chart uses it); everything else
    the user deletes from Chloe.
- **Data access**:
  - `create_data_source` needs Read on that database for the endpoint.
  - Its SQL must pass the same check as `run_query` and is stored as given. Chloe runs it read-only every time.
  - Tools that return data or export need Read on every database the chart or dashboard's sources use, including
    archive snapshots (the data came from there). One database the endpoint can't read makes the tool refuse,
    naming the source.
  - Nothing here writes to a database, so nothing asks for approval. The only file writes are Chloe's own workspace
    files and exports.
- **Shelves over MCP** use field names from the source: `{"dimension": [{"field": "order_date", "bucket": "month"}],
  "series": [...], "value": [{"field": "total", "aggregate": "sum", "alias": "Revenue"}], "target": [...],
  "secondary": [...]}`. Unknown fields, aggregates or shelves the type doesn't have are refused with the allowed
  list. `properties` takes the common properties by name (title, legend, labels, number and date format, colours,
  top N, reference lines). Unknown keys are refused.
- **Layout over MCP**: agents don't place things in pixels.
  - A page is a list of rows, each a list of tiles `{chart | notes | text, span}` across 12 columns, plus an
    optional `height` per row (S / M / L).
  - Chloe turns that into positions on the page's grid. Pages the user has laid out by hand keep their positions;
    an agent can only add rows below.
- **Notes**: `set_notes` writes the Markdown label on a page that the endpoint owns, titled "Notes from <endpoint>"
  with the time, and creates it at the top of the page if needed. Calling it again replaces that label. This is where an agent writes "revenue fell 12 % week on week,
  driven by…".
- **Results** use `run_query`'s shape (`columns, rows, row_count, truncated`, display strings).
- **Exports** go to the Reports folder (Preferences; default `~/Documents/Chloe Reports/<workspace>/`), named after
  the dashboard and time. The tool returns the path. Agents never choose the path.
- **Activity log** rows show the workspace and object, and for data tools the SQL Chloe generated.

### Scheduled check-ins
- MCP works only while Chloe is running and its server is on. Both transports are local, so cloud schedulers (e.g.
  claude.ai routines) can't reach it. That's by design (`docs/specs/mcp-server.md`).
- Schedule the agent on the same machine: Claude Code `/loop`, a launchd or cron job running `claude -p "…"` with
  Chloe's bridge in its MCP config, or Claude Desktop's scheduled tasks.
- A typical check-in prompt: "Refresh the *Weekly sales* dashboard in Chloe, compare this week with last week, update
  the notes with anything unusual, and export a PDF."
- The BI library shows when each dashboard was last refreshed, by whom, and whether its notes changed since the user
  last opened it (a dot on the row, like unread mail).

## Implementation notes (phase 1)
- `internal/bi` holds the workspace format, the chart type catalog and the query builder; `core/bi.go` runs live
  sources (read-only sessions, 30 s) and archive snapshots (an in-memory SQLite database, serialized, gzipped and
  sealed with the keychain key in `bi/data/<workspace id>/<source id>.archive`, cached in memory while in use).
- Columns are typed from the database's type names; untyped columns, and text columns whose sampled values are all
  dates (SQLite stores dates as text), are typed from up to 200 sample rows.
- Date buckets come back as sortable text (`2026-03`, `2026-Q1`; a week as the date of its Sunday, `2026-09-13`) and
  the chart labels them (`Mar 2026`, `Q1 2026`, `Week of Sep 13, 2026`). `week_x` numbers weeks like strftime's `%U`.
- **Time zone** per data source (an IANA name): date/time fields are bucketed and filtered as wall-clock time there.
  Values without a zone count as UTC; DATE columns aren't shifted. PostgreSQL uses `AT TIME ZONE`, MySQL `CONVERT_TZ`
  (Chloe refuses with a hint when MySQL's time zone tables aren't loaded), SQLite and archives Chloe's own `chloe_tz()`.
  Archive snapshots store zoned values in UTC. `in_last` counts days in the source's zone.
- Sort, Top N with Others and value operations run on the aggregated rows, so they work the same live and archived.
  Without a sort, date and number dimensions keep their order and categories go largest first.
- Names are unique per kind: a chart may share its data source's name.
- Legends, tooltips and table headers show fields readably ("Sum of Total", "Placed At (Month)", "Customer ID"); an
  alias is shown as typed. Shelves keep the field names.
- A date axis bucketed by year, quarter, month, week, day or hour gets its empty periods back (sums and counts 0,
  other aggregates empty), unless the chart uses Top N or sorts by value.
- A plain date compared with a date/time field stands for the whole day: `between a and b` and `<= b` run up to
  midnight after b, `= d` covers the day, `> d` starts the next day (DATE columns compare as they are).
- Filters on the dimension's field widen that fill to their window: `in_last` (today included), `between`, `=`, and
  either end from `>=`, `>`, `<=`, `<`.
- Extracted weekdays and months come back named (`Sun`…`Sat`, `Jan`…`Dec`), in calendar order.
- Table columns can group a date by a bucket.
- Every dimension value gets a row for every series (0 for sums and counts), so stacked series never have holes.
- A sort by value can name the value it sorts by (`sort.field`, or the value's field or label as `sort.by`); tables sort by their first aggregated column
  otherwise, and by their first column when sorting "by dimension".
- `in_last` filters (`[n, unit]`, unit day, week, month or year) keep charts current: the last n units including the
  current one, from the machine's local date.
- Over MCP, `edit_chart` accepts the chart shape `get_workspace` returns (`aggregate`, `op`, `values`, `props`), and
  `get_chart_data` returns its SQL only with `include_sql`. Values are unformatted; number formats are display only.
- Over MCP, data sources on connections the endpoint can't read show only their name, and their charts only name and
  type; creating or editing charts on them is refused.

## Implementation notes (phase 2, dashboards)
- Pages are a 12-column grid (react-grid-layout): tiles snap to it, move by their grip and resize from the corner. A
  page is a **16:9 slide** (24 rows that fit the pane) or a **report** that grows downward. A grid row is 18px with
  12px gaps, so tiles grow in 30px steps (workspace format version 2; version 1 files, with rows twice as tall, are
  converted as they're read by doubling every tile's row and height, which keeps their pixels). Navicat's free canvas,
  shapes, images and tile layers aren't there; the grid replaces tile layers.
- Tiles are charts of the workspace or Markdown text (sanitized: no HTML; links open in the browser). An agent's notes
  are a text tile it owns, headed "Notes from <endpoint> · <time>"; `set_notes` replaces it, at the top of a report
  page (pushing the rest down) or in the first free rows of a slide.
- Pages are laid out at one size, 1280px wide (a slide 1280×720), and scaled to fit wherever they're shown, so the
  designer is a true miniature of Present and of exports.
- A row an agent describes that holds only text is exactly as tall as its text (its height label is ignored); a row
  with a chart takes its label (S 6, M 10, L 16, XL 22 grid rows; a slide holds 24) and grows if its text needs more.
  `edit_dashboard` keeps notes at the top or bottom of their page, as they were. Whenever an agent edits its
  dashboard or writes notes on it, text rows are fitted again (pages laid out in clean rows only; a slide never grows
  past 24 rows), so layouts from older builds correct themselves.
- The MCP server's instructions name the Chloe build (git commit), so testers can tell which build they reach. Heights come from metrics measured in Chloe (13.5px text on 20.25px lines, 18px tile
  padding, rows 18px plus 12px gaps); an e2e check renders agent notes and asserts they fit with under a row to spare.
- `set_notes` takes `place` (top or bottom of a report page); without it, existing notes stay where they are. It and
  `open_dashboard` answer briefly (the notes' row and height; the dashboard's name and page count). `edit_dashboard` keeps notes written with
  `set_notes` on the page with the same number. `refresh_dashboard` returns a page's own `text` apart from agents'
  `notes` (with who wrote them and when).
- Within each dimension value, series always come back in the same order (by name), filled rows included.
- A `>=`/`>` filter on the date dimension with no end fills to today (in the source's zone), so charts side by side
  end on the same day.
- `refresh_dashboard` reads a chart that's on several pages once (one result, one `as_of`) and reports each text
  tile's and note's height against the rows its text needs (`text_fit`, `needed`).
- Value axes leave about 8% above the tallest value.
- Deleting a chart takes its tiles off every dashboard. Auto refresh (off, 1, 5, 15, 30, 60 min) reloads the open
  dashboard. Present shows one page at a time (← →, Esc).
- MCP: `create_dashboard` and `edit_dashboard` (rows of tiles across 12 columns; pages default to report),
  `set_notes`, `refresh_dashboard` (every chart's data plus the page's text), `open_dashboard`, `delete_dashboard`.
- Next (phase 2b): export.

## Implementation notes (phase 2b, export)
- Chloe's window draws exports: the page is laid out off screen at its 1280px size without animation, captured once
  every chart has its data (html-to-image), at twice the size for files. **Export** in the dashboard toolbar saves this
  page as PNG or every page as a PDF (pages embedded losslessly, 960pt wide).
- `export_dashboard` (MCP) draws pages at their layout size, returns PNGs as images so the agent can see the page, and
  saves them (or a PDF) in the Reports folder (`~/Documents/Chloe Reports/<workspace>/`, `CHLOE_REPORTS_DIR` to move
  it). It needs Read on every source the pages' charts use, and Chloe's window open (the server asks the window to draw
  and waits up to a minute).
- Notes headers are a caption beside the first line. Headings in tiles are tight (# 22px at 1.2, ## 18px at 1.3), so a
  lone heading fits two grid rows. Notes go on top of slides too (the slide's tiles move down) when there's room, and
  sit centred in their tile so the grid's leftover space splits above and below (text tiles stay top-aligned).
- Notes on a slide are never cut off: set_notes (and an edit_dashboard that would squeeze them) is refused with the
  rows the notes need and the rows free. Notes follow their page by name when pages move. Slide rows that don't fit
  are reported first, with each row's height.
- Charts show enum-like category and series values readably (ended_early → Ended Early; all-lowercase or camelCase
  values only, so emails, URLs and Mixed Case stay as stored); `labelCase: raw` shows them as stored. Only the
  display changes: data, filters and MCP results keep the stored values. Day and week labels drop the year when all
  dates are in one year; value-sort ties go alphabetically.
- `e2e/tester.sh` starts a sandbox for a tester agent (seeded games data, Reports on, a browser kept open to draw
  exports); `node e2e/mcp-call.mjs` calls tools and saves returned images. A tester pass runs before builds go to
  the user's beta tester.

## Out of scope
- File/ODBC data sources, cross-connection joins, Navicat Cloud/On-Prem sync.
- Reading or writing Navicat's `.nbi` files (the sample was read only to learn the object model).
- Redis: Navicat doesn't chart it either.

## Delivery
1. **Charts and MCP core** ✅:
   - BI library and workspace tab.
   - Data sources: table, view or query on MySQL, PostgreSQL and SQLite; live and archive; filter, sort and
     projection.
   - Chart designer with Bar, Line/Area, Bar and Line, Pie/Donut, Value, Table, and theme defaults.
   - **Create Chart** in the data grid toolbar (as Navicat has), which makes a data source from the grid's table or
     query in a workspace you pick.
   - MCP tools: `list_workspaces`, `get_workspace`, `create_workspace`, `create_data_source`, `create_chart`,
     `edit_chart`, `get_chart_data`, `open_chart`. ✅
2. **Dashboards and reports** ✅: pages, charts, Markdown text, refresh and auto refresh, Present, Export (PDF, PNG),
   and the MCP dashboard, notes and export tools. Snapshots were dropped; SVG/HTML export and print are not planned
   unless asked for.
3. **The rest of Navicat's BI**: Waterfall, Tornado, Treemap, Scatter, Heatmap, Trend, KPI, Gauge, Pivot Table ✅; dynamic date filters ✅; parameters ✅; interaction and Control ✅; custom fields ✅; joined tables ✅; shapes and lock ✅;
   Control; interaction (cross-filter, highlight, controls); parameters; custom fields; joined tables; shapes, groups,
   lock, alignment guides; dynamic date filters.
4. MongoDB data sources (collection, or aggregation pipeline), with charts over them.

## Testing
- Unit:
  - The chart query generator per dialect: buckets, aggregates, filters, binding, quoting, Top N and Others.
  - Read-only enforcement of source SQL.
  - Layout rows → positions.
- Integration (Docker): the same chart definitions against MySQL, PostgreSQL and SQLite seeds give the same
  aggregated rows. Archive snapshots match live.
- e2e (`make e2e SCRIPT=bi`): build a data source and a chart from the `shop` seed, place it on a dashboard, present
  it, and export PDF and HTML.
- `SCRIPT=mcp`: an agent creates a workspace, source, chart and dashboard, refreshes twice and reads the deltas, sets
  notes, and is refused when editing a user chart or reading a source on a database it can't see.

## Implementation notes (phase 3, chart types)
- A **Target** shelf joins the others: a KPI's target, a gauge's maximum, a scatter's Y axis. Its fields are
  aggregated like values (role `target` in the data).
- **Waterfall** (dimension, one value): each value is a step from the running total, rises in the diverging pair's
  blue and falls in its red, ending with a neutral Total bar from zero.
- **Tornado** (dimension, series with exactly two values, one value): the first series mirrored to the left on one
  symmetric, rounded scale. Other counts show a hint to filter the series to two.
- **Treemap** (dimension, one value): tiles sized by value, labelled, in palette order; past eight categories tiles
  share the neutral (use Top N with Others). Rows aren't filled with empty periods or series.
- **Scatter** (point dimension, optional series, value = X, target = Y, optional secondary = size by area, 8–32px):
  points stay as read (no filled series, no Others point); Top N still applies.
- **Heatmap** (dimension = X, series = Y, value = intensity): every cell filled; a one-hue sequential ramp per theme,
  near the surface for small values, with the range shown under the grid.
- **Trend** (date dimension grouped by period, one value): the latest period's value, its change from the one
  before (arrow, sign, %, status colour) and a 12-period sparkline. Extracted parts are refused.
- **KPI** (value, optional target field or `goal` property): the value, a meter towards the target (its track a
  lighter step of the fill), the share and the difference. **Gauge** (value, optional target as maximum; `min`/`max`
  properties): a meter arc with the value. `lowerIsBetter` turns a fall into the good direction.
- **Pivot Table** (rows = dimension, columns = series or value fields, values): totals per column, and per row when
  split by series, for sums and counts.
- MCP: `create_chart`/`edit_chart` take `target`, and the properties `goal`, `min`, `max`, `lowerIsBetter`.

## Implementation notes (phase 3, dynamic dates)
- Filters on dates take three relative operators, worked out from the machine's date in the source's time zone each
  time a chart is read, so reports stay current:
  - `in_last` [n, unit]: the last n days, weeks, months, quarters or years, the current one included.
  - `in_period` [this | last | next, unit]: one whole calendar period, e.g. last month or next quarter.
  - `to_date` [unit]: week, month, quarter or year to date (through today).
- Weeks start on Sunday, quarters in January, April, July and October. Ranges are bound as dates (`>= from AND <
  to`), and a date axis is filled across the whole period.
- Figures (Value, KPI) show "—" when the filter matches no rows.

## Implementation notes (phase 3, parameters)
- A parameter has a name (letters, digits, `_`), a type (text, number, date), allowed values (any, a list with
  optional descriptions, or a number range with min, max and step) and a current value. **New Parameter** in the
  workspace toolbar opens its dialog; parameters are listed with the other objects (filter: Parameter), and their
  Used By lists the sources, charts and dashboards that refer to them. Navicat's "where it may be used" flags aren't
  kept: a parameter can be used anywhere.
- `[$name]` is replaced:
  - in a data source query, by a **bound placeholder** (numbered before the chart query's own values), never by its
    text. A reference inside quotes is refused with a hint; references in comments are ignored. The query is checked
    and its fields read with the current values.
  - in a filter value (source or chart) that is exactly `[$name]`, by the value, checked when the chart is read.
  - in chart titles and dashboard text, by the value (unknown names stay as written).
- Reads take overrides for that read only: a dashboard's **parameter bar** (the parameters its charts, sources and
  text use: a list, a slider for a range, a box otherwise) changes values for the session, in the designer, Present
  and exports, without saving them. Values are checked against the type and allowed values.
- MCP: `create_parameter` and `edit_parameter` (agent-made only); `get_workspace` lists parameters;
  `get_chart_data` and `refresh_dashboard` take `parameters` (by name) for one read.

## Implementation notes (phase 3, interaction)
- **Click a point** on a dashboard (bar, line point, slice, tile, cell, scatter point): the page's other charts on the
  same data source are filtered to that dimension value, or with **Highlight** the charts with the same dimension dim
  every other category; the clicked chart keeps its point highlighted. Dashboard Properties › "Clicking a point"
  sets filter (default), highlight or none (`interaction`).
  - A date bucket filters to its whole period (`between` its first and last day; an hour to its 60 minutes);
    extracted parts (all Marches) and Others don't filter. NULL filters `is null`.
  - A chip names each selection; clicking it, clicking the point again, or Esc clears it. Selections belong to the
    page while it's shown (designer and Present), not to the saved dashboard or to exports.
- **Control** charts (dimension only: the field's distinct values) filter other charts: a list (several values),
  a drop-down (one) or a range (numbers and dates, from and to); by default a range for numbers and dates and a
  list otherwise. A control filters the charts picked as its tile's **Target charts**, or with none picked every
  chart on the page whose data source has its field.
- Filters from clicks and controls are added to the chart's own when it's read; nothing is saved.
- MCP: `create_dashboard`/`edit_dashboard` take `interaction`; agents can create Control charts like any other.

## Implementation notes (phase 3, custom fields and joins)
- **Custom fields** (Custom Fields pane of the source designer; `custom_fields` over MCP) are computed in every
  chart and preview query, live or archived, by wrapping the source in one subquery layer per field, so each may use
  the fields before it and filters, buckets and aggregates work on them like on columns. Their type is worked out
  when the source is saved.
  - **Calculated**: Chloe's own small expression language (fields by name or `[in brackets]`, `[$parameter]`,
    `+ - * / % &`, comparisons, AND OR NOT, ROUND ABS FLOOR CEIL COALESCE NULLIF IF UPPER LOWER TRIM LENGTH YEAR
    MONTH DAY), parsed and compiled per dialect: field names checked and quoted, literals and parameters bound,
    division by zero NULL and never integer-truncated.
  - **Mapped** (rules from → to, case-sensitive or not; unmapped values kept, emptied or set to Other),
    **Concatenated** (fields with a separator; NULL as empty), **Type-Changed** (to text, number or date/time) and
    **Custom-Sorted** (a hand-made order of values: those first, the rest after in label order). Sorting by another
    field's values isn't built.
- **Joined tables** (Joins pane for table sources; `joins` over MCP): more tables of the same database, each inner,
  left, right or full (PostgreSQL) join on one or more pairs of matching fields. The first table's columns keep their
  names; joined columns come through as `table.column` and can be matched by later joins. Their columns are read
  when the source is saved, previewed or its fields are read. Cross-connection joins stay out.

## Implementation notes (phase 3, shapes and lock)
- **Add Shape** puts a shape tile on the grid: line, arrow, rectangle, ellipse, triangle, right triangle, right
  arrow, and database, cloud, server, desktop, mobile and person icons for diagrams. Each has a colour (default the
  theme's secondary ink), line width, filled or outlined, and dashed or solid; lines and outlines stretch to the
  tile with strokes kept at their width, icons keep their proportions. Shapes have no card behind them.
- Any tile can be **locked** (its properties: Lock position and size): it can't be dragged or resized and shows a
  small lock in the designer. Navicat's free-canvas groups and alignment guides aren't needed on the grid.

## Implementation notes (phase 3, after the tester pass)
- A column with no values to look at when the source is read (a parameter or join that matches nothing right now)
  keeps the type it had; a table source's column is typed from its table instead.
- `joins[].columns` picks which of a joined table's columns come through (default all); the left side of a match
  must be a column of the first table or `table.column` of an earlier join.
- An archive source's query ran when its snapshot was taken: overriding a parameter it uses is refused with a hint
  to refresh the data or go live. Sources naming a parameter the workspace doesn't have are refused when saved.
- Filters are checked against their field: relative dates need a date/time field, and a number field compares with
  numbers (a `[$parameter]` value is checked once filled in). A gauge's minimum must be below its maximum.
- Top N keeps the largest values whatever the dimension (it sorts by value unless a sort is set). Control lists,
  and split series without a date part or custom order, sort case-insensitively; date parts (Sun…Sat) and
  custom-sorted fields keep their order as series too; heatmap rows read top-down.
- Number format `percent` shows 0.2 as 20%.
- MCP: `refresh_dashboard` fills parameters into page text and returns each chart's `title`; it and
  `export_dashboard` (which takes `parameters` now) check parameter values once. `delete_parameter` deletes an
  agent's parameter that nothing uses. `edit_data_source` refuses a change that would break the source's charts,
  naming them. Wrong property types and double-quoted text in expressions get plain errors.
- KPI, Trend and Gauge tiles scale their text to the tile; very short tiles drop the extras (meter details,
  sparkline, gauge labels).
- Not built: ratios of totals (calculated fields are per row), custom sort by another field, MCP control targets.

