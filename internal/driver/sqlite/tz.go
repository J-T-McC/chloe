package sqlite

import (
	sqldriver "database/sql/driver"
	"time"

	gosqlite "modernc.org/sqlite"

	"chloe/internal/tz"
)

// chloe_tz(value, zone) converts a stored date/time (UTC unless it carries a
// zone) to wall-clock time in an IANA zone, for BI data sources with a time
// zone; SQLite has no time zones of its own. Other values come back as NULL.
func init() {
	gosqlite.MustRegisterDeterministicScalarFunction("chloe_tz", 2, func(_ *gosqlite.FunctionContext, args []sqldriver.Value) (sqldriver.Value, error) {
		zone, _ := args[1].(string)
		var s string
		switch v := args[0].(type) {
		case string:
			s = v
		case []byte:
			s = string(v)
		case time.Time:
			s = v.UTC().Format("2006-01-02 15:04:05")
		default:
			return nil, nil
		}
		if out, ok := tz.In(s, zone); ok {
			return out, nil
		}
		return nil, nil
	})
}
