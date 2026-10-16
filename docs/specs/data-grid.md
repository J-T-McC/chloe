# Data grid (table view) — Phase 1 spec

Source: decoded table-view, grid, form-view, limit/paging, filter & sort, value-picker and data-editor screens, plus Preferences > Grid.

## Layout
- **Toolbar** (top): Begin Transaction, Text, Filter, Sort, Import, Export, Data Generation (later phases), plus a search field.
- **Filter & Sort bar** (toggle **⌘F**, "Show/Hide Filter & Sort"): a mode switch between a **visual builder** and a **SQL text editor** (separate WHERE and ORDER BY editors with completion), shown stacked or side by side. Filter conditions: field ▾, operator ▾, value, with add/remove, AND/OR grouping and enable checkboxes. Sort rows: field ▾, ASC/DESC. Sort menu: Add, Copy (⌘C), Copy All Sorts, Paste, Clear All Sorts, Clear All Filters & Sorts.
- **Grid**: a row-selector column on the left, **frozen columns** pane, main scroll grid.
- **Pending-changes bar**: "N records modified" with **Apply** / **Discard** (when auto-apply is off). Pending rows are tinted with the "Pending Records Color".
- **Bottom bar**: + (add record), − (delete), ✓ (post/apply), ✗ (cancel), ⟳ refresh, ■ stop. Record navigation: first/prev/next/last. Paging: page number field, first/prev/next/last page, **Limit** toggle + "records per page" [1000]. View switch: **Grid** / **Form**. A zoom button. Warning icon when the table has **no primary key**, which makes it read-only or matched on all columns.
- Status: SQL text of the current query and the record count/position.

## Views
- **Grid view**: spreadsheet editing.
- **Form view**: one record at a time, as label + editor per field. Choose Columns. Same navigation. Context menu: Add Record (⌘+), Delete Record (⌘−), Copy As, Show/Hide Columns…, Go to Record…, filter/sort.
- (Mongo: Grid / Tree / JSON views. Phase 2.)

## Cell editing
- Inline text editing. Type-aware pickers:
  - **Date/time picker** (graphical + textual)
  - **Enum picker**: list with OK/Cancel
  - **Set picker**: checkbox list with Select All, Deselect All, **Empty Set**, **Null**
  - **Foreign-key value picker**: a popup grid of the referenced table, with a column chooser, a filter field, "Showing first N records" + Show All. Picking a row fills the FK value.
- **Cell editor pane** ("Edit in Cell Editor"): **Text**, **Hex** (hex editor), **Image** (load/save/clear, fit/full), **Web** (HTML preview), JSON beautify.
- **Set to Empty String**, **Set to NULL**, **Discard Changes** (per cell).
- NULL is shown distinctly. Pref: NOT NULL indicator color.

## Context menus (exact items)
**Cell selection:**
- Set to Empty String, Set to NULL, Copy, Copy Field Name
- **Copy As** ▸ Insert Statement, Update Statement, TSV (Data only / Field Name only / Field Name and Data), CSV (same three), JSON
- Paste, Save Data As… (BLOB to file), Show/Hide Filter & Sort
- **Filter** ▸ Field = Value, Field != Value, Field like Value, Field unlike Value, Field < Value, Field > Value, Custom Filter, Remove Filter, Remove All Filters
- **Sort** ▸ Sort Ascending, Sort Descending, Remove All Sorts, Remove All Filters & Sorts
- Refresh

**Column header:** Copy, Copy Field Name, Copy As ▸ …, Paste, Show/Hide Columns…, Hide Column, **Freeze Column**, Unfreeze All Columns, Set Column Width…, Set All Columns Width…, Size Column to Fit, Size All Columns to Fit.

**Grid background:** Copy/Copy As/Paste, Set Row Height…

## Find / replace in grid
A bar with a **Find Data** / **Find Field** mode, Highlight All, Incremental Search, Match Case, prev/next, and **Replace** / **Replace All** mode with Done.

## Transactions & apply semantics
- Pref **Auto begin transaction**: edits run inside an explicit transaction until Commit/Rollback.
- Pref **Auto-apply single-record changes**: leaving an edited row posts it immediately. Otherwise changes queue up for Apply/Discard.
- Generated DML: an UPDATE/DELETE keyed by the PK (or by all original column values when there's no PK, with a warning). INSERT for new rows. Chloe: show a "Preview SQL" option.

## Preferences > Grid (exact options)
| Option | Default |
|---|---|
| Limit records + "Records: [N] records per page" | on, 1000 |
| Auto begin transaction | off |
| Auto-apply single-record changes | on (inferred) |
| Grid font (Use default font) | |
| Row Shading | **None** / Alternate Rows / Every Third Row |
| Zoom | % list |
| Colors: Pending Records Color, NOT NULL color, Data Profiling Bar / Spotlight colors | |
| MongoDB Time Zone Offset | +00:00 |

## Grid coloring (optional, later)
"Enable Coloring": up to 6 conditional color rules, each with a type selector.

## Chloe implementation guidance
- **Canvas-based virtualized grid.** Glide Data Grid is MIT and canvas-based, with frozen columns, custom cells and editors. Rows are fetched by viewport window from a Go-side result cursor/page cache, so large results are never fully serialized to JS.
- Paging defaults to LIMIT/OFFSET 1000 per page for table views. Query results stream in chunks.
- Copy As formats are generated in Go so they work for large selections.
- **Editable query results.** A result is editable when its statement is a plain single-table `SELECT` (no joins,
  GROUP BY, DISTINCT or set operations) and it includes the table's whole primary key (`core.ResultEditTarget`).
  Column references, `*` and `t.*` map to table columns; expressions stay read-only. Without a primary key the
  result is read-only, unlike the table view, because a query's columns can't safely stand in for a row identity.
  Apply writes through `ApplyChanges` and patches the buffered result (`PatchResult`) instead of re-running the
  query. Otherwise the status bar shows a read-only badge with the reason.
