# ER models

A model is a design of tables kept in Chloe's model library. It never touches a database by itself.

- Start from what exists: `create_model` with a connection, database (and schema) reverse engineers its tables.
  Start empty (name and dialect) for a new design, or `copy_of` to change a model the user made (theirs can't be
  edited).
- `get_model` returns tables with columns, keys, indexes, foreign keys and options. `edit_model` adds or replaces
  whole tables (`set_tables`), drops tables or renames the model; tables you don't mention stay as they are.
- `generate_sql` turns the model into SQL without running it: CREATE statements, or, given a connection, the
  statements that would bring that database in line with the model. Read them: a changed column type can rewrite a
  table, and a dropped column loses data.
- Applying the SQL is the user's step: Synchronize to Database in Chloe, or `open_query_tab` (Saved queries
  capability) with the statements and a note on what they do.
- `open_model` shows the diagram to the user.
