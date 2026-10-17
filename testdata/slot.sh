# Sourced by the test scripts. CHLOE_DB_SLOT=N (default 0) picks a set of Docker test
# databases of its own: ports move up by N*10 and containers are named chloe-mysql-N...,
# so several checkouts or agents can test at once. Slot 0 is the original set.
slot=${CHLOE_DB_SLOT:-0}
if [ "$slot" = 0 ]; then sfx=""; else sfx="-$slot"; fi
# One compose project per slot, whatever the checkout's folder is called.
export COMPOSE_PROJECT_NAME="navicat-clone$sfx" CHLOE_DB_SUFFIX="$sfx"
export CHLOE_MYSQL_PORT=$((33306 + slot * 10)) CHLOE_PG_PORT=$((55432 + slot * 10)) \
  CHLOE_REDIS_PORT=$((63790 + slot * 10)) CHLOE_MONGO_PORT=$((37017 + slot * 10)) CHLOE_SSH_PORT=$((2222 + slot * 10))
MYSQL_C="chloe-mysql$sfx" PG_C="chloe-pg$sfx" REDIS_C="chloe-redis$sfx" MONGO_C="chloe-mongo$sfx"
