#!/bin/sh
# Browser end-to-end checks against the server build (bin/Chloe-server on :8080).
#   wails3 task build:server && docker compose up -d && e2e/run.sh [script]
# Scripts print OK/FAIL per step; screenshots (and fail-*.png) go to e2e/.out/shots.
set -e
root=$(cd "$(dirname "$0")/.." && pwd)
out="$root/e2e/.out"
cd "$root"
[ -d e2e/node_modules ] || (cd e2e && npm install --silent && npx playwright install chromium)

docker exec -i chloe-mysql mysql -uroot -pchloe < testdata/seed_mysql.sql 2>/dev/null
docker exec -i chloe-pg psql -q -U postgres -v ON_ERROR_STOP=1 -f - < testdata/seed_postgres.sql >/dev/null 2>&1
docker exec -i chloe-mongo mongosh -u root -p chloe --quiet < testdata/seed_mongo.js >/dev/null 2>&1 || true
docker exec -i chloe-redis redis-cli -a chloe --no-auth-warning < testdata/seed_redis.txt >/dev/null 2>&1 || true

rm -rf "$out" && mkdir -p "$out/config" "$out/shots" "$out/lite"
# SQLite scripts read and create files under $LITE; pagesize wants a 250-row table.
sqlite3 "$out/lite/big.db" "CREATE TABLE n(id INTEGER PRIMARY KEY, v TEXT);
  WITH RECURSIVE c(i) AS (SELECT 1 UNION ALL SELECT i+1 FROM c WHERE i < 250) INSERT INTO n SELECT i, 'row ' || i FROM c;"

pkill -f Chloe-server || true
sleep 0.5
CHLOE_CONFIG_DIR="$out/config" CHLOE_SECRETS=memory CHLOE_REPORTS_DIR="$out/reports" CHLOE_EXPORT_DIR="$out/exports" ./bin/Chloe-server > "$out/server.log" 2>&1 &
trap 'pkill -f Chloe-server || true' EXIT
sleep 1.5
cd e2e && LITE="$out/lite" node "${1:-smoke}.mjs" "$out/shots"
