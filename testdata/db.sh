#!/bin/sh
# Starts the Docker test databases of slot $CHLOE_DB_SLOT (see slot.sh) and loads the seed data.
set -e
cd "$(dirname "$0")/.."
. testdata/slot.sh
docker compose up -d
# Retry seeding: both images restart their server once during first-boot initialisation.
for i in $(seq 60); do docker exec -i "$MYSQL_C" mysql -uroot -pchloe < testdata/seed_mysql.sql 2>/dev/null && break; [ "$i" = 60 ] && exit 1; sleep 2; done
for i in $(seq 60); do docker exec -i "$PG_C" psql -q -U postgres -v ON_ERROR_STOP=1 -f - < testdata/seed_postgres.sql >/dev/null 2>&1 && break; [ "$i" = 60 ] && exit 1; sleep 2; done
for i in $(seq 30); do docker exec -i "$REDIS_C" redis-cli -a chloe --no-auth-warning < testdata/seed_redis.txt >/dev/null 2>&1 && break; [ "$i" = 30 ] && exit 1; sleep 1; done
for i in $(seq 60); do docker exec -i "$MONGO_C" mongosh -u root -p chloe --quiet < testdata/seed_mongo.js >/dev/null 2>&1 && break; [ "$i" = 60 ] && exit 1; sleep 2; done
echo "slot ${CHLOE_DB_SLOT:-0}: MySQL :$CHLOE_MYSQL_PORT, Postgres :$CHLOE_PG_PORT, Redis :$CHLOE_REDIS_PORT, MongoDB :$CHLOE_MONGO_PORT, sshd :$CHLOE_SSH_PORT"
