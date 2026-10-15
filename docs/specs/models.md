# Models — spec

Data models (ER diagrams) live in a library Chloe manages, like Navicat, which keeps its model workspaces in its
profile folder and lists them when you press Model in the main toolbar (behaviour from Navicat's bundled help).
Models are independent of connections: one can be built from a database and synchronized to several others.

## Library
- `.chloemodel` files in `models/` in Chloe's config folder. Each file is named after its model (made safe for a
  file name, numbered when taken).
- **Model** in the main toolbar shows the library in the Objects tab: Name, Type (MySQL/PostgreSQL), Tables,
  Database, Created By, Modified, and a detail panel (file, link, table list). Selecting something in the
  connection tree goes back to its objects.
- Toolbar: Open Model, New MySQL Model, New PostgreSQL Model, Delete Model, Open External…, Refresh, Filter.
  Row menu: Open Model, Rename…, Duplicate, Delete…, Save As External File…, Show in Finder.
- A model keeps a **link** to the database it was built from or last synchronized to (connection, database,
  schema). The list shows it, and Synchronize to Database starts on it. A deleted connection shows as such.
- **Created By** is "AI (endpoint)" for models an agent created over MCP; agents may edit only those
  (`docs/specs/mcp-server.md`).

## Saving
- A new or reverse-engineered model is unsaved until Save, which asks for a name and adds it to the library.
- Save writes the model back to its file. **Save As…** (⇧⌘S) writes a copy to a file the user picks (Navicat's
  Save As External File); the tab stays on its own file.
- **Open External…** opens a `.chloemodel` kept anywhere. Save writes back to that file; **Save to Chloe** adds a
  copy to the library and switches the tab to it.
- An open model reloads when an agent edits its file over MCP, unless it has unsaved changes; then the status bar
  says so.
