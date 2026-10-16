# MongoDB

- `mongo_databases`, `mongo_collections`, then `mongo_describe`: field paths and types from a sample of documents
  (fields that are rare may be missing from it), the indexes and the validator, or a view's pipeline.
- `mongo_find` for documents matching a filter; `mongo_aggregate` for anything grouped, joined or reshaped. Both
  return at most 1000 documents, so group in the pipeline rather than counting returned documents. `mongo_find` with
  `count` also counts every match.
- Filters and pipelines take Extended JSON for types JSON doesn't have: `{"$oid": "…"}`, `{"$date":
  "2026-01-01T00:00:00Z"}`, `{"$numberLong": "…"}`.
- Keep pipelines kind to the server: `$match` (on indexed fields, see `mongo_describe`) and `$project` first, `$sort`
  on an index or after `$group`, `$limit` as early as the result allows. A `$lookup` without an index on the foreign
  field runs a scan per document.
- Reads never write: `$out`, `$merge`, server-wide stages and lookups into other databases are refused.
- Writes (`mongo_insert`, `mongo_update`, `mongo_delete`) ask the user unless the endpoint is in danger mode. One
  insert takes up to 1000 documents; update and delete change the first match, or with `many` every match (at most
  1000). Use `_id` in the filter when you can. Collection and index changes aren't available over MCP.
