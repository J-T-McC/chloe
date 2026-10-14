# Main window, navigation tree & object list — Phase 1 spec

Sources: decoded main-window, connection-tree, object-list, information-pane, DDL, find-in-database, and tab-bar screens, plus menu definitions and action labels from the binary.

## Main window layout
```
┌──────────── toolbar (Connection ▾, New Query, Table, View, Function, Users, Others ▾, …) ────────────┐
│ Navigation pane   │  Tab bar: [Objects] [query1] [table_x @db] …              │ Information pane │
│ (connection tree, │                                                             │ (General / DDL / │
│  search on top)   │  Objects tab: object list for the selected tree node       │  Detail tabs)    │
│                   │                                                             │                  │
├───────────────────┴─────────────────────────────────────────────────────────────┴──────────────────┤
│ status bar (selected connection / object, counts)                                                  │
└────────────────────────────────────────────────────────────────────────────────────────────────────┘
```
- Three-column split: **navigation pane** (⌘1 to toggle), **content tabs**, **information pane** (toggle; ⌘2 General). Panes are collapsible. **Focus mode** hides everything but the content.
- **Tabs**: the permanent **Objects** tab plus one tab per opened table/query/designer. Close Tab (⌘W), Close Other Tabs, Close Tabs to the Right, Close All. Tabs can open in the main window or a new window. Pref "Allow opening multiple forms for same object".
- **File menu**: New Connection ▸ (per DB type), New Query, Open File (⌘O), Open Recent ▸ Clear, Import Connections…, Export Connections…, Close Window (⇧⌘W), Close Tab (⌘W).
- **View menu**: Navigation Pane ▸ Show (⌘1), **Flatten Connection**, **Show Only Active Objects** (hide unopened connections/DBs), Show Filter On Top. Information Pane ▸ Show, General (⌘2). Objects ▸ **Detail** (list style), ER Diagram, **Flatten Object List**, Choose Columns…, Show Hidden Items. Show Status Bar, Show Toolbar, Enter Focus Mode, Full Screen.

## Navigation pane (connection tree)
- A search field (top or bottom per "Show Filter On Top") does incremental filtering of tree items.
- Hierarchy:
  - **MySQL**: Connection → Database → { Tables, Views, Functions (procedures + functions), Events, Queries (saved), Backups } → objects. With pref "Show objects under table": Table → { Fields, Indexes, Foreign Keys, Triggers }.
  - **PostgreSQL**: Connection → Database → Schema → { Tables, Views, Materialized Views, Functions, Sequences, Types, Domains, … } → objects. Pref "Show objects under schema in navigation pane" (on) controls whether schema children appear in the tree or only in the object list.
- Icons per object type. Opened connections/DBs show an "active" state. Connection color tag.
- Lazy loading: children load on expand. Refresh per node (⌘R).
- Drag & drop: reorder connections into groups; drag a table to another connection to start a data transfer (later phase).
- Double-click: connection → open. Database → open (load children). Table → **Open Table** (data grid). View → open. Function → design.
- **Custom database list** (per-connection setting) restricts which DBs appear.

### Context menus (core set)
- **Connection**: Open/Close Connection, Edit Connection…, New Connection ▸, Delete Connection, Duplicate Connection, **Connection Color** ▸, New Database…, New Query, Execute SQL File…, Refresh, **Connection Info**, Copy Navicat URI → (Chloe: Copy connection URL), Manage Group ▸.
- **Database / Schema**: Open/Close Database, Edit Database… (charset, collation), New Database, Delete Database, New Query, Execute SQL File…, **Dump SQL File ▸ Structure and Data / Structure Only**, Find in Database…, Refresh, Maintain ▸ (Analyze), Duplicate.
- **Table**: Open Table, Open Table (Quick: no blob prefetch), Design Table, New Table, Delete Table, **Empty Table**, **Truncate Table**, Duplicate Table ▸ (Structure and Data / Structure Only), Import Wizard…, Export Wizard…, Dump SQL File ▸, Copy, Rename, Maintain ▸ (Analyze/Check/Optimize/Repair for MySQL; Analyze/Vacuum/Reindex for PG), Reverse Table to Model, **Copy Field Name / Copy As** (later), Refresh, Object Information.
- **View**: Open View, Design View, New View, Delete View, Duplicate, Export, Dump SQL, Refresh.
- **Function/Procedure**: Design Function, New Function (opens the **function wizard** if the pref is on), Delete, Duplicate, Execute Function (prompts for params), Dump SQL.

## Objects tab (object list)
- Shows the children of the selected tree node, with its own toolbar for the node type: **Open Table / Design Table / New Table / Delete Table / Import / Export** (same idea for views and functions).
- Modes: **List**, **Detail** (columns such as Name, Rows, Data Length, Engine, Created/Modified Date, Collation, Comment; MySQL comes from `SHOW TABLE STATUS`), **ER Diagram** (later).
- **Choose Columns…**, sort by column, search filter, multi-select for bulk delete, export, or dump.

## Information pane (right)
Tabs (icon tab strip, collapsible):
- **General**: key/value properties of the selected object. Table: rows, engine, auto-increment, collation, sizes, create/update time, comment. Connection: host, port, user, server version, session count.
- **DDL**: read-only syntax-highlighted `CREATE …` for the object (copy button). Loading indicator, error message on failure.
- **Detail / column info**: column list with name, type, PK marker and description.
- (AI Chat. Skip.)

## Find in Database (later phase, simple)
Connection ▾, Database ▾, Schema ▾, **Find:** text (`\` as escape character), **Look In** (Data / Structure), **Search Mode** (Contains / Whole Word / Prefix / Regex, inferred), Case Insensitive. Include Objects: Tables, Views, Functions, Queries, Materialized Views, Events, Triggers, Indexes. Results: Name / Number of Matched Records, double-click to open.

## Saved queries
"Queries" node under each database: named SQL files saved per connection/DB, opened as query tabs. Autosave every 30 s (pref).

## Startup behavior (Preferences > Tabs)
- On Startup: **Open Objects tab only** / **Continue where you left off** / Open a specific tab or set of tabs (Set Tabs…).
- Open new tab in: **Main window** / Last tab window / Last tab window (except main) / New window.
- Always show the tab bar.
