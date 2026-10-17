#!/bin/sh
# A sandbox for a tester agent: the browser build with a seeded games database,
# an MCP endpoint (Reports on) and a browser kept open so exports render.
#   make server && e2e/tester.sh        # then: node e2e/mcp-call.mjs <tool> '<json>'
# TESTER_REDIS=1 also shares the Docker Redis (reseeded) read-write as "cache", with Monitor on;
# TESTER_MONGO=1 does the same for the Docker MongoDB (reseeded) as "docs".
# TESTER_WRITE=1 shares games read-write, with Saved queries on.
# Writes are approved automatically and each approval dialog's text goes to e2e/.tester/approvals.log.
# Stop it with: kill $(lsof -a -ti tcp:${CHLOE_E2E_PORT:-8080} -sTCP:LISTEN -c Chloe); pkill -f tester-setup
set -e
root=$(cd "$(dirname "$0")/.." && pwd)
out="$root/e2e/.tester"
cd "$root"
[ -d e2e/node_modules ] || (cd e2e && npm install --silent && npx playwright install chromium)
port="${CHLOE_E2E_PORT:-8080}"
lsof -a -ti "tcp:$port" -sTCP:LISTEN -c Chloe | xargs kill 2>/dev/null || true
pkill -f tester-setup || true
rm -rf "$out" && mkdir -p "$out/config" "$out/reports" "$out/images"
sqlite3 "$out/games.db" <<'SQL'
CREATE TABLE rooms (id INTEGER PRIMARY KEY, game TEXT, created_at TEXT, started INTEGER, finished INTEGER, ended_early INTEGER, rematched INTEGER, events INTEGER);
CREATE TABLE users (id INTEGER PRIMARY KEY, anonymous INTEGER, created_at TEXT, last_active TEXT);
WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i + 1 FROM n WHERE i < 420)
INSERT INTO rooms SELECT i,
  CASE i % 7 WHEN 0 THEN 'frontier' WHEN 1 THEN 'warriors' WHEN 2 THEN 'singularity' WHEN 3 THEN 'yacht' WHEN 4 THEN 'cats' WHEN 5 THEN 'smear' ELSE 'frontier' END,
  strftime('%Y-%m-%d %H:%M:%S', '2026-09-10', '+' || (i * 2327 % 3500000) || ' seconds'),
  (i % 3 <> 0), (i % 5 = 1), (i % 11 = 4), (i % 13 = 2), 5 + (i * 37 % 90)
FROM n;
WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i + 1 FROM n WHERE i < 300)
INSERT INTO users SELECT i, (i % 4 <> 0), strftime('%Y-%m-%d %H:%M:%S', '2026-09-12', '+' || (i * 9173 % 3000000) || ' seconds'),
  strftime('%Y-%m-%d %H:%M:%S', '2026-10-01', '+' || (i * 4111 % 1200000) || ' seconds') FROM n;
-- A big table indexed only by room, so a filter on time or kind scans it.
CREATE TABLE room_events (id INTEGER PRIMARY KEY, room_id INTEGER, kind TEXT, created_at TEXT);
CREATE INDEX room_events_room_id ON room_events (room_id);
WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i + 1 FROM n WHERE i < 300000)
INSERT INTO room_events SELECT i, 1 + i % 420,
  CASE i % 9 WHEN 0 THEN 'join' WHEN 1 THEN 'leave' WHEN 2 THEN 'chat' WHEN 3 THEN 'win' ELSE 'move' END,
  strftime('%Y-%m-%d %H:%M:%S', '2026-09-10', '+' || (i * 11 % 3500000) || ' seconds') FROM n;
SQL
if [ -n "$TESTER_REDIS" ]; then
  docker exec -i chloe-redis redis-cli -a chloe --no-auth-warning < testdata/seed_redis.txt >/dev/null
fi
if [ -n "$TESTER_MONGO" ]; then
  docker exec -i chloe-mongo mongosh -u root -p chloe --quiet < testdata/seed_mongo.js >/dev/null
fi
# CHLOE_E2E_PORT moves the server off 8080 when something else holds it.
export WAILS_SERVER_PORT="$port" BASE="http://localhost:$port/"
CHLOE_CONFIG_DIR="$out/config" CHLOE_SECRETS=memory CHLOE_REPORTS_DIR="$out/reports" ./bin/Chloe-server > "$out/server.log" 2>&1 &
sleep 1.5
cd e2e && TESTER_REDIS="$TESTER_REDIS" TESTER_MONGO="$TESTER_MONGO" TESTER_WRITE="$TESTER_WRITE" nohup node tester-setup.mjs "$out" > "$out/setup.log" 2>&1 &
for i in $(seq 1 60); do [ -f "$out/endpoint.json" ] && break; sleep 0.5; done
cat "$out/endpoint.json" 2>/dev/null || (cat "$out/setup.log"; exit 1)
