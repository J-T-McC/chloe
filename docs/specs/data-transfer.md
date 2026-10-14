# Data Transfer, Structure Sync, Data Sync — spec

Source: Navicat Premium 18 window/module screens for Data Transfer, Structure Synchronization and Data Synchronization (labels only), the app's
localized option strings, its bundled offline help (`data_transfer`, `structure_sync`, `data_sync`) and screenshots, plus Navicat's public blog
screenshots of the Windows wizard. Checkbox defaults are filled in at runtime, so they're not in the screen files. Defaults marked **inferred**
come from screenshots or help text, not from the app's resources.

## Data Transfer (Tools > Data Transfer)
Copies objects (structure and/or data) from one database/schema to another, or to a file: SQL for RDBMS, JavaScript for MongoDB. The target can be
on the same server or a different one, and also a different server type. A MongoDB source can only go to MongoDB.
Shortcut: dragging tables/collections onto a database/schema in the navigation tree copies them directly when the target is in the same connection, and opens this wizard otherwise.

### Window
- **Header strip**: source (type over database name, icon) → target (type/db, or **File** and its file name). Title is "Data Transfer", with a leading `*` while there are unsaved profile changes.
- **Footer** on every step: **Save Profile** (split button: **Save** / **Save As**), **Load Profile**, **Options**, then **Back** / **Next** (**Start** on the summary step, **Close** at the end).

### Step 1: Source and target
- Two columns. **Source**: **Connection** ▾, **Database** ▾, **Schema** ▾ (only for schema-capable engines). **Target**: a radio **Connection** / **File**.
  - Connection: **Connection** ▾, **Database** ▾, **Schema** ▾.
  - File: path field + **…** browse. **SQL Format**: a **Same as source server** checkbox, server type ▾ and version ▾. **Encoding** ▾ (default UTF-8).
- A **swap** button between the columns exchanges source and target.
- An **Information** box under the source: connection type, name, host, port, server version.

### Step 2: Database Objects
- A tree grouped by kind: **Tables**, **Views**, **Functions** (procedures are listed together with functions), **Events**, and the engine's other kinds
  (sequences, types… **inferred**). MongoDB shows **Collections** (and views).
- Every group node has a tri-state checkbox. **All objects start unchecked**.
  - Group label `(*)`: the whole group is checked. This means "all objects at run time", so objects created later are included when the profile runs again.
  - Group label `(n/m)`: only the ticked objects are transferred. New objects are ignored until the list is edited.
- The right pane shows **Transfer Mode** for the selected table/view, with radios **Auto** (default) and **Advanced**. An item in Advanced mode shows `(Advanced)` after its name. Advanced options:
  - **Target Name** (rename).
  - **All fields** / **Custom fields**: a field list with + and a target-name column.
  - **All rows** / **Custom recordsets**: filter expressions added with +. **Recordset Generator…** asks for Field Name, Type, Start Value,
    End Value and Number of Recordsets (1–10000; integer or date fields only) and splits the range evenly. **SQL Preview** shows each recordset's SELECT.
  - **Use transaction for each recordset**. **Number of rows per batch** [1000], off by default. When it's off, the whole table goes as one transaction.
  - For views: **Transfer as table** (create a table with the view's rows instead of the view).

### Step 3: Summary
- A text line ("All tables, All views", …). A grid with **Source Object** | **Target Object** | **Mode**. `(*)` groups show as "All tables during execution (*)".
- Quick checkboxes repeated from Options: **Continue on error**, **Drop target objects before create**.
- Navicat 18 also has a **Data Masking** area here, with masking rules per field plus **Input Preview** / **Output Preview** ("Transfer masked data to another database"). Phase 2 at the earliest.

### Step 4: Progress
- Counters: **Objects**, **Processed Records**, **Transferred Records**, **Errors**, **Time**. Title shows `NN% - Data Transfer`.
- **Message Log**: one line per action, prefixed `[DTF]` and a running object number, e.g. `[DTF] 8> orders: Getting table structure`, `… Drop table`,
  `… Create table`, record-transfer lines, and a final `[DTF] Finished successfully` (or `Finished unsuccessfully` / `Finished with error`). Errors are listed inline.
- With a file target the end step offers **Show in Finder** and **Open with External Editor**.

### Options dialog
The **Options** button opens a modal with three titled groups. Only the options the source/target pair supports are shown. Defaults are **inferred** from the public screenshot of the Windows build (MySQL → SQL file), whose author had already changed some of them:

**Table Options**
- **Create tables** ✓ (off = append into existing tables). For MongoDB: **Create collections**. Indented under it, only enabled when it's checked:
  - **Include indexes** ✓, **Include foreign key constraints** ✓, **Include checks** ✓, **Include uniques** ✓, **Include excludes** (PG) ✓,
    **Include rules** (PG) ✓, **Include engine/table type** (MySQL) ✓, **Include character set** ✓, **Include auto increment** ✓,
    **Include other table options** ☐ (unchecked in the screenshot), **Include triggers** ✓. For MongoDB: **Include other collection options**.
- **Convert object name to** ☐, with radios **Lower case** (default) / **Upper case**, enabled only when it's checked.

**Record Options**
- **Create records** ✓ (help and blog: untick it for a structure-only transfer). Indented under it:
  - **Empty target tables before creating records** ☐ (MongoDB: "…collections…"; new in 18, default **inferred**).
  - **Lock target tables** ☐, **Use transaction** ✓.
  - **Use complete insert statements** ✓ (column list in each INSERT), **Use extended insert statements** ✓ (multi-row VALUES).
  - **Maximum Statement Size** (KB) for extended inserts. A row that would exceed it is written as a normal INSERT. Default value not seen.
  - **Use delayed insert statements** ☐ (MySQL), **Use ignore insert statements** ☐, **Use replace insert statements** ☐ (MySQL; **inferred** off).
  - **Run multiple insert statements** ✓ (**inferred**), **Use hexadecimal format for BLOB** ✓ (**inferred**).

**Other Options**
- **Continue on error** ☐ (**inferred**: the help describes it as opt-in; the screenshot shows it ticked by the author).
- **Lock source tables** ☐ (**inferred**, same reason).
- **Use DDL from SHOW CREATE TABLE** (MySQL→MySQL) ☐ / **Use DDL from sqlite_master** (SQLite→SQLite) ☐: copy the server's own DDL verbatim instead of regenerating it.
- **Include definers** ☐ (MySQL), **Include owners** ☐ (PG; **inferred** off).
- **Use single transaction** ☐ (consistent snapshot of the source; InnoDB).
- **Drop target objects before create** ✓, **Drop with CASCADE** ☐ (PG).
- **Create target database/schema if not exist** ☐ (**inferred**). The label becomes "Create target database if not exist" when the engine has no schemas.

### Cross-engine behaviour
- Tables and data can go between any supported RDBMS (e.g. MySQL → PostgreSQL). Views, functions and events are copied as their DDL text, so between engines they usually fail and get logged (**inferred**).
- Table DDL is rebuilt in the target dialect, not copied (the "Use DDL from…" options only exist when source and target are the same engine). Each column type maps to the closest target type
  (**inferred**: e.g. `TINYINT(1)`→`smallint`/`boolean`, `DATETIME`→`timestamp`, `ENUM`→`varchar`, `TEXT` variants→`text`, `BLOB`→`bytea`).
- Auto increment becomes the target's own mechanism (PG identity/serial, SQLite `INTEGER PRIMARY KEY AUTOINCREMENT`). **Include auto increment** carries the current counter (**inferred**).
- Defaults: literal defaults are kept, and function defaults that the target can't express are dropped (**inferred**).
- Indexes and uniques are recreated by column list. Engine-specific index kinds (FULLTEXT, GIN…) are skipped or logged as errors (**inferred**).
- Foreign keys are created after all tables are loaded, so load order doesn't matter (**inferred** from the option order and the log).
- **Convert object name to** exists mainly for MySQL → PG/Oracle-style case-folding.

## Structure Synchronization (Tools > Structure Synchronization)
Compares the DDL of two databases/schemas and generates ALTER/CREATE/DROP statements that make the target match the source. Supported pairs are the same engine, or MySQL ↔ MariaDB. **Not available for SQLite** according to the help's list.

- **Step 1, General**: the same Source/Target pickers (Connection, Database, Schema) with swap, **Manage Connections**, and an **Information** box. Footer: Save/Save As, Load Profile, **Options**, **Compare**.
- **Compare Options** dialog (each item only where the engine has it): **Compare tables**, **Compare primary keys**, **Compare foreign keys**, **Compare indexes**,
  **Compare uniques**, **Compare checks**, **Compare excludes**, **Compare character set**, **Compare collation**, **Compare auto increment value**, **Compare partitions**,
  **Compare table options**, **Compare storage**, **Compare tablespace and physical attributes**, **Compare views**, **Compare materialized views**, **Compare functions**,
  **Compare sequences**, **Compare sequence last values** / **Compare identity last value**, **Compare triggers**, **Compare events**, **Compare rules**, **Compare definers**,
  **Compare owners**, **Drop with CASCADE**. Also **Identifier Case Sensitivity:** ▾ **Default** / **Case Sensitive** / **Case Insensitive**.
  Defaults (**inferred**): object and constraint comparisons on; counter values (auto increment, sequence last values), definers, owners and Drop with CASCADE off.
- A comparison progress sheet appears while comparing ("Start/Finished getting tables info", "Start/Finished checking tables structure").
- **Step 2, Compare results**: a tree with columns **Source Object** | **Operation** | **Target Object**, checkboxes on each row and group, and **Select All** / **Deselect All**.
  - Grouping toggle: **Group by Object Type** or by operation. The operations are *modify* (in both, different), *create* (only in source), *drop* (only in target) and *same* (identical, no action).
    The different/create/drop rows start checked. Same rows can't be checked (**inferred**).
  - Expanding a table lists its column and index differences.
  - Bottom tabs: **DDL Comparison** (source DDL and target DDL side by side, with added/changed/removed lines highlighted) and **Deployment Script** (statements for the selected object).
  - **Recompare** reruns the comparison.
- **Step 3, Deployment**: the full script in a read-only editor. Buttons: **Deployment Options** (**Continue on error** ☐, **Include deployment query in message log** ☐), **Reorder Script**
  (a dialog for moving statements), **Copy Script to Clipboard**, **Open Script in Query Editor**. **Start** runs it. Some statements can be edited first (**inferred**).
- **Step 4, Message Log**: **Progress**, **Succeeded**, **Failed** and **Time** counters plus the log, ending "Finished deployment (Total: n, Success: n, Fail: n)". **Pause** / **Stop** are available while it runs.

## Data Synchronization (Tools > Data Synchronization)
Makes the target's rows match the source's, comparing by key. Supported: same engine (MySQL ↔ MariaDB allowed), SQLite and MongoDB included.

- **Step 1, General**: the same pickers. **Options** dialog with three checkboxes, all ✓ (**inferred**): **Insert** (rows missing in the target), **Update** (rows that differ), **Delete** (extra rows in the target).
- **Step 2, Mapping**: one row per table/collection. Only those with the same name are mapped by default, and a ▾ per row can unmap or remap. Columns: source table, target table, **Key Mapping**
  (defaults to the primary key with the same name, editable), **Field Mapping** (same-name fields), **Conditions** (a WHERE filter applied to both sides, e.g. `id > 10000 AND id < 20000`).
  **Select All** / **Deselect All**. Buttons **Compare & Preview** and **Compare & Deploy** (which skips the preview).
- **Step 3, Compare results** (see `images/dataSyncList.png`): the header shows the source → target pair. **Show identical table and others** ✓ (untick it to hide identical tables and tables whose structures differ).
  - Grid: ☑ | **Source Table** | **Target Table** | **Insert** | **Update** | **Delete** | **Same** | **Message**. Each action cell has its own checkbox and count. A partial selection shows `k/n`.
  - Rows that can't be compared are greyed out with a message, e.g. "Different number of…" or "Source table does not exist". Tables with differences and all their actions start checked.
  - Bottom pane for the selected table: source and target records side by side, with the differing values highlighted and a checkbox per record to leave it out. A filter ▾: **Difference** (default), **Insert**,
    **Update**, **Delete**, **Same**, **All Rows**. Paged at "N records per page". The context menu has **Show Assistant Viewer** for long values.
  - **Recompare**, **Back**, **Next** (generates the script).
- **Step 4, Deployment**: script view and buttons as in structure sync. **Deployment Options**: **Continue on error** ☐, **Run multiple queries in each execution** ✓ (**inferred**),
  **Use transaction** ✓ (**inferred**; rolls everything back on error), **Include deployment query in message log** ☐.
- **Step 5, Message Log**: as in structure sync ("Data Synchronization "name" start" … "Finished deployment (…)").

Profiles for all three tools: **Save** / **Save As** (named), **Load Profile** (a list of saved names). They're stored per tool in the shared profiles folder and can run as scheduled jobs.

## Chloe notes
- **Engines**: transfer between MySQL/MariaDB, PostgreSQL and SQLite in every direction, MongoDB → MongoDB, and Redis → Redis (keys; Navicat doesn't offer that). Structure sync: same dialect only (plus MySQL↔MariaDB),
  and SQLite too, using `design.RebuildPrefix` for rebuilds. Data sync: same engine, compared by primary key; Mongo by `_id`.
- **Structure path**: read the source tables into `design.Table` (the same path as reverse-engineering a model). Then render them with the target dialect's `design.Generator`. This is the cross-engine case,
  so it needs a type-mapping table built around `design.CanonType`. "Use DDL from SHOW CREATE TABLE / sqlite_master" can reuse the `core.Dump` DDL path.
- **Structure sync = `datamodel.Plan` with a live source**: the plan's create/alter/drop actions are the comparison tree (group by object type / by operation), and
  `SyncPlan.Statements()` is the deployment script. Run it through `core.ExecuteDDL`, which already handles MySQL's non-transactional DDL and SQLite's FK checks.
- **Data path**: stream rows from the source session into the existing import batch writer (`core` importer `writeBatch`). Extended vs complete inserts and the max statement size are
  generator options. Use the Jobs panel for progress (`progress.logf` for `[DTF]`-style lines, counters for Objects/Processed/Transferred/Errors), and the existing cancel for **Stop**.
- **File target**: almost the same as the existing SQL dump, with a target dialect added. Reuse it.
- **Data sync**: keyed merge-compare over both result sets sorted by PK (chunked), producing insert/update/delete counts per table, a lazy record diff for the bottom grid, then generate DML.
- **Profiles**: save the wizard state as JSON under the app config dir, like saved queries. Leave out data masking and scheduling for now.
- **UI**: one wizard component shared by the three tools (source/target pickers with swap, a footer with profile buttons). Draggable splits for compare tree vs DDL tabs, and record grid. Verify with an e2e script.
