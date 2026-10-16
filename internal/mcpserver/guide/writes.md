# Changing data

## Approvals
Unless the endpoint is in danger mode, every write tool call waits while Chloe shows the user what it will do (the
statements, the table, how many rows) and asks them to approve it. Results say how the change was cleared
(`approval`).
- Declined: the user has answered. Don't try the same change another way (other tool, smaller pieces, different
  conditions); ask what they want instead.
- No answer before the request timed out: nothing was done. Check with the user before trying again.
- Say what you're about to change, and why, before the call, so the dialog isn't a surprise.

## One call, one approval
- `insert_rows` takes up to 1000 rows in one transaction and one approval, and returns each row's primary key.
  Twenty calls of one row are twenty dialogs.
- `update_rows` sets the same values on every row its conditions match. Changing many rows to the same value is one
  call (`in` takes a list); different values per row need a call per value.
- `update_rows` and `delete_rows` count the rows first and ask about that number. If the count changes before the
  statement runs, everything is rolled back and nothing changes.
- Run `select_rows` with the same conditions first when you aren't sure what they match.

## Choosing rows
- Use the primary key (`describe_table`) whenever you can. Conditions are required and are joined with AND.
- Pass big integers (IDs over 2^53) as strings, so they aren't rounded.
- Values are bound, never pasted into SQL, so quotes and odd characters are safe. They're literal: for "now" or
  another value the server computes, read it first (`run_query` with `SELECT CURRENT_TIMESTAMP`) and send the result,
  or leave the column out when its default does it.

## Without the tools
Bulk changes, multi-table changes and anything with SQL logic (UPDATE … JOIN, INSERT … SELECT) aren't possible
through the row tools. Write the SQL and give it to the user through `open_query_tab` (Saved queries capability) or
in your reply.
