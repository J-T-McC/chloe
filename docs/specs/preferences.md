# Preferences — Phase 1 spec

Source: decoded Preferences screens. Each section below is one pane. There's a **Restore Defaults** button per pane. Items marked *skip* are cloud, AI, licensing, or Oracle-environment items Chloe doesn't need in Phase 1.

## General
- **Appearance**: System Default (default) / Light / Dark
- Main Window: **Show objects under schema in navigation pane** (on); **Show objects under table in navigation pane**
- Object list font (Use default font)
- Confirm dialogs: **Use safe confirm dialog** (type the name to confirm drops); **Ask to save new queries/profiles before closing**; Ask to save new table profiles before closing; **Show SQL preview before saving** (designers); **Show function wizard**
- *skip*: Usage data sharing, auto update (Chloe: our own updater later)

## Tabs
- On Startup: Open Objects tab only / **Continue where you left off** / Open a specific tab or set of tabs (Set Tabs… picker: File Name, Location, Add, Use Current)
- Open new tab in: **Main window** / Last tab window / Last tab window (except main window) / New window
- Allow opening multiple forms for same object
- Tab bar style: Always show the tab bar

## Editor
See sql-editor-autocomplete.md. Line numbers, code folding, brace highlighting, syntax highlighting, the size limit (MB) for disabling highlighting and completion, tabs→spaces, tab width, word wrap, font, and syntax colors (Common/Keyword/Comment/String/Number/Background) with a preview.

## Code Completion
See sql-editor-autocomplete.md for the full option table.

## Grid (Records)
See data-grid.md. Limit records (1000/page), auto begin transaction, auto-apply, grid font, row shading (None/Alternate/Every Third), zoom, pending/NOT NULL colors, Mongo TZ offset.

## Auto Recovery
| Option | Values | Default |
|---|---|---|
| Autosave Modified Queries | Every 15 s / **30 s** / 1 min / 3 min / 5 min / Never | 30 s |
| Autosave Modified Models | … | 3 min |
| Autosave Modified BI Workspaces | *skip* | |
After a crash, offer an **Auto Recover** dialog listing unsaved query buffers.

## Logging
- Logs Location (Show in Finder)
- **Enable history logging**: records all executed SQL; viewable in a History Log window. Max number of files, max size per file (MB).
- Enable diagnostic logging: same limits.
- Changes need a restart (marked with *).

## Environment
- External Editor path.
- Tool paths for later phases: Mongo Shell, mongodump, mongorestore, mongoimport, mongoexport. SQLite3 dynamic library (*skip*; we bundle pure-Go SQLite). *skip*: OCI/ORACLE_HOME/TNS_ADMIN/SQL*Plus/SnowSQL until Phase 5.

## Other panes (*skip*)
AI (assistant providers), Output/Connectivity (cloud and update connectivity tests, URI protocol registration).

## Storage
Navicat persists preferences and connections as plist data via core callbacks (`SavePrefData`, `SaveConnData`), with secrets in the Keychain. Chloe: one JSON settings file in the OS config dir (`~/Library/Application Support/Chloe/`). Connections go in a separate `connections.json`. Secrets go in the keychain, never in JSON.
