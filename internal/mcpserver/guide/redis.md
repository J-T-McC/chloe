# Redis

- Pass the database number as `db` (`redis_databases` lists them with their key counts).
- `redis_overview` first: key counts by prefix and type, with an example key. Then `redis_scan` with a pattern for one
  prefix, a page at a time (pass back the cursor until it's absent). `KEYS` is refused: it blocks the server.
- `redis_get` reads a key with its type and expiry; big hashes, lists, sets, sorted sets and streams come a page at a
  time. Check a collection's size before reading all of it.
- `redis_command` runs one read command and returns what redis-cli would print; commands that write, block, switch
  databases or run scripts are refused.
- Writes (`redis_set`, `redis_edit`, `redis_delete`, `redis_expire`) ask the user unless the endpoint is in danger
  mode. `redis_set` replaces a key's whole value; use `redis_edit` to change part of one (a field, a member, a
  counter). A replaced key keeps its expiry unless you pass `ttl` (0 removes it).
