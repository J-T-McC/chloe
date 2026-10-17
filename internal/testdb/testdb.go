// Package testdb has the addresses of the Docker test databases used by the
// integration tests (docker-compose.yml). CHLOE_DB_SLOT=N picks the set started
// with the same variable: every port moves up by N*10 (testdata/slot.sh).
package testdb

import (
	"os"
	"strconv"
)

var slot, _ = strconv.Atoi(os.Getenv("CHLOE_DB_SLOT"))

func port(base int) int { return base + slot*10 }

// Host is where the test databases listen.
const Host = "127.0.0.1"

// Ports of the slot's MySQL, PostgreSQL, Redis, MongoDB and SSH jump host.
var (
	MySQL    = port(33306)
	Postgres = port(55432)
	Redis    = port(63790)
	Mongo    = port(37017)
	SSH      = port(2222)
)
