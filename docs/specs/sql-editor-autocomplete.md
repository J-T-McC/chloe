# SQL editor & autocomplete — Phase 1 spec

Sources: the Preferences > Code Completion and Editor screens, the SQL editor screens, ObjC selectors and classes in the main binary, and exported APIs of the bundled libraries (`libnp2.dylib` is the parser and completion engine, `EE.framework` is the Scintilla-based editor). This describes behavior; no code was copied. The Ghidra deep analysis never finished (timed out twice at 15 min), so ranking details are marked **inferred**.

## How Navicat's engine works (observed architecture)
- **A real parser per dialect, not regexes.** libnp2 is built on **ANTLR4**. Grammar rule names match the MySQL Workbench MySQL grammar (`bitExpr`, `boolPri`, `columnInternalRef`, `changeReplicationSource…`) and the grammars-v4 PostgreSQL grammar (`bareColLabel`, `BeginDollarStringConstant`). Each connection type has its own grammar (MySQL, PG, MSSQL, Oracle, SQLite, Snowflake, Dameng).
- **Candidates come from the parser's state at the caret.** A custom error strategy walks the parser's internal state machine up to the caret token and collects the set of tokens and rules that could come next. This is the same technique as `antlr4-c3`. Expected keyword tokens become keyword suggestions. Expected identifier rules (table ref, column ref, schema ref…) become object suggestions.
- **A simple fallback parser.** When the full parse can't settle the context, a lighter parser with a default set of suggestion types is used.
- **Async with cancellation.** Parses are tagged with a parse ID and can be aborted. The newest request wins and stale results are dropped. The completion UI waits for the parse to update before showing.
- **Identifier resolution.** The parse tree is post-processed into **ID groups**. Every reference to the same object (alias ↔ table, CTE name ↔ uses, column ↔ its table) is linked, with "parent candidates" for each identifier. This drives:
  - alias resolution (`t.` → columns of the table aliased `t`)
  - highlighting every occurrence of the identifier under the caret
  - Cmd-click jump to the definition, resolving **locally first** (aliases, CTEs, derived tables), then to the database object
- **Issues.** The parse result carries a list of issues (syntax and semantic) with ranges, shown as squiggles.
- **Statement splitter.** A separate splitter supports custom delimiters (`DELIMITER //` for MySQL) and an MSSQL "GO-only" mode. It powers Run Current Statement, Run Selected, and Run All.
- **Each suggestion entry** carries: name, node type, sub-node type, a flag (system/desired), and a qualifying path (catalog/schema/table).

## Suggestion kinds (Chloe should support all)
| Kind | Source | Notes |
|---|---|---|
| Keywords | grammar's expected tokens | Context-sensitive: only keywords valid at the caret |
| Databases / schemas | metadata cache | |
| Tables, views, materialized views | metadata cache | Includes system objects only if the pref is on |
| Columns | metadata cache + FROM/JOIN scope | Columns of tables in scope. With `alias.`, only that table's columns |
| Aliases & CTE names | local parse | Table aliases, `WITH` names, derived-table aliases |
| Functions / procedures | metadata cache + built-in catalog | Built-ins carry a **signature + one-line description** (e.g. `GROUP_CONCAT` → "returns a string result with the concatenated non-NULL values from a group"), shown in a side "comment box" |
| Snippets | user + preset snippets | Filtered by DB type; show in the list and insert with placeholders |
| Dynamic keywords | object names | Object names also feed syntax highlighting ("become lexer keyword") |
| Mongo / Redis | separate after-dot / command lists | Redis commands carry a version tag (`<6.0.6+> …`). Phase 2/3 |

## Context rules
- After `FROM`, `JOIN`, `UPDATE`, `INTO`, `TABLE`: schemas and tables/views.
- After `SELECT`, `WHERE`, `ON`, `GROUP BY`, `ORDER BY`, `HAVING`, `SET`: columns in scope, aliases, functions, keywords.
- **After a dot**: resolves `parent.` / `grandparent.parent.` from an **identifier cache**. Fetches the children lazily from the server if they aren't cached yet, e.g. columns for a table never expanded.
- In `JOIN … ON`: suggest the join condition from foreign keys. **This is Chloe's improvement**; Navicat's binary shows no explicit FK-join suggestion.
- `INSERT INTO t (` → columns of `t`.
- Respects the current schema/database (`setCurrentSchema`), server version, and ANSI_QUOTES for MySQL.
- Quoting: inserted identifiers are quoted when needed, per dialect (`` ` `` for MySQL, `"` for PG).

## Ranking & filtering (**inferred**; verify by using the app)
- Each suggestion **type/tag has a priority** (`setAutoCompletePriority:type:` / `tag:`), and items sort by type priority, then name. Icons per type.
- The list filters as you type. The base is Scintilla's case-insensitive prefix matching.
- **Chloe target**: fuzzy matching (prefix > word-boundary > substring), frequency/recency boost, in-scope objects ranked above global ones.

## Triggers
- Automatic while typing an identifier, and after `.`. Ctrl+Space / Esc to force or close. The first suggestion is auto-selected if the pref is on.
- Hover over an SQL element (table/column/function) shows details after a delay. Pref "Hover Delay (ms)".

## Preferences > Code Completion (exact options)
| Option | Default |
|---|---|
| Use code completion | on |
| Auto update code completion info | on |
| Include system objects when updating code completion info | off |
| Auto select first suggestion | on |
| Show details when hovering over SQL elements | on |
| Hover Delay (ms) | numeric |
| Cmd-Click default action, Schema | **Edit** / Reveal in Navigation Pane |
| Cmd-Click default action, Table / View | **Design** / Open / Reveal in Navigation Pane |
| Cmd-Click default action, Field | **Design** / Open / Reveal in Navigation Pane / Jump to Parent |
| Cmd-click resolve identifier references locally first | on |
| Clear Code Completion Info (button) | Clears the metadata cache |

## Preferences > Editor
Show line number; Use code folding; Use brace highlighting; Use syntax highlighting; **Disable syntax highlight and code completion if SQL larger than [N] MB**; Insert spaces when pressing Tab; Tab Width; Use word wrap; Editor font (Use default font); Syntax colors: Common, Keyword, Comment, String, Number, Background, with a live Preview.

## Query tab layout (editor window)
- **Toolbar**: Save, Query Builder, Beautify SQL, Code Snippet toggle, Text, Export Result, **Run ▾** (Run / Run Selected / Run Current Statement), Stop, **Explain**, plus connection, database and schema selectors.
- **Editor** (Monaco in Chloe) with a **right dock**: Object List (searchable, docked), **Snippets** (label filter "All Labels", search, + new; context menu: New/Edit/Duplicate/Delete, Copy/Move To, Show Snippets For Other Database Type, Show Preset Snippets, Refresh), Code Outline.
- **Code Outline** panel: a tree of statements, clauses and variables, with expand/collapse, sort, refresh, and highlight of the selected statement in the editor.
- **Beautify SQL**: options are Upper case keywords and Short Brace Length (keep short parenthesized lists on one line), with Beautify / Save / Default.
- **Query parameters**: Navicat parses `:param`-style placeholders and prompts with an "Edit with Parameters" dialog (parameter table + editor) before running.

## Results area (below editor)
- One **Result N** tab per result set, plus a **Message/Summary** tab.
- **Summary**: Processed Query, Success, Error counts, Start Time, End Time, Elapsed Time. Message grid with "Show errors only".
- **Info** (per result): Connection, Catalog, Database, Time Stamp, Query Time, Fetch Time.
- Result grid = data grid (see data-grid.md). It's editable when the result maps to a single table with a PK. Bottom bar: + / − / ✓ / ✗, record navigation, Grid/Form view toggle, elapsed time.
- **Explain** tab: views **Visual** (a plan tree diagram with zoom, search, cost threshold %, node info panel with Copy All, export image), **Text**, **JSON**, **Info**, **Statistics** (Per Node Type: Node Type / Count / Cost / Cost %. Per Relation: Relation Name / Scan Count / Cost / Cost % / % In Relation).

## Chloe implementation guidance
- **Use `antlr4ng` + `antlr4-c3` in TypeScript**, with the MySQL Workbench MySQL grammar and the grammars-v4 PostgreSQL grammar. That's the same proven design as Navicat. A good shortcut is DTStack's `dt-sql-parser` (MIT), which already packages MySQL/PG grammars with caret-position suggestions for syntax entities (table, column, view, function, database) plus keyword candidates. Evaluate it first.
- Run parsing in a **Web Worker**, with request IDs for cancellation.
- Go serves the metadata cache: per connection/schema object lists, lazily loaded columns, and FKs for join suggestions. The frontend keeps an in-memory index.
- Built-in function catalog: generate our own JSON (name, signature, short description) per dialect from the official docs. Don't copy Navicat's text.
