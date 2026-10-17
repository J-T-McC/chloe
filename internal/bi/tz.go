package bi

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"chloe/internal/driver"
)

// A data source's time zone: its date/time fields are bucketed and filtered as
// wall-clock time there. Stored values without a zone are taken as UTC.

var tzName = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_+\-/]*$`)

// CheckTimezone accepts "" (no conversion) or an IANA zone name such as
// America/Los_Angeles. Names are written into SQL, so only plain names pass.
func CheckTimezone(tz string) error {
	if tz == "" {
		return nil
	}
	if !tzName.MatchString(tz) {
		return fmt.Errorf("%q isn't a time zone name (e.g. America/Los_Angeles, Europe/London, UTC)", tz)
	}
	if _, err := time.LoadLocation(tz); err != nil {
		return fmt.Errorf("unknown time zone %q (use an IANA name such as America/Los_Angeles)", tz)
	}
	return nil
}

// col is a field as the chart sees it: a date/time converted to the data
// source's time zone (dates without a time aren't shifted).
// newQB starts a query over src. The zone is written into the SQL, so it is checked here
// as well as when the source is saved: a workspace file may not have come through Chloe.
func newQB(d driver.Dialect, src DataSource, args []any) (qb, error) {
	if err := CheckTimezone(src.Timezone); err != nil {
		return qb{}, err
	}
	return qb{d: d, tz: src.Timezone, args: slices.Clone(args)}, nil
}

func (b *qb) col(f Field) string {
	col := b.d.QuoteIdent(f.Name)
	t := strings.ToLower(f.DBType)
	if b.tz == "" || f.Type != TypeDateTime || t == "date" {
		return col
	}
	switch b.d.Name {
	case "postgres":
		if t == "timestamptz" || strings.Contains(t, "with time zone") {
			return "(" + col + " AT TIME ZONE '" + b.tz + "')"
		}
		return "(CAST(" + col + " AS timestamp) AT TIME ZONE 'UTC' AT TIME ZONE '" + b.tz + "')"
	case "mysql":
		if t == "timestamp" {
			return "CONVERT_TZ(" + col + ", @@session.time_zone, '" + b.tz + "')"
		}
		return "CONVERT_TZ(" + col + ", '+00:00', '" + b.tz + "')"
	}
	return "chloe_tz(" + col + ", '" + b.tz + "')"
}

// cmp is a field for comparing with values: the stored column and a function
// that turns a value in the data source's time zone into what it's compared
// with. Shifting the value instead of the column keeps the comparison on the
// bare column, so an index on it is used. Only MySQL and PostgreSQL date/time
// columns qualify; SQLite stores dates as text in more than one format, so it
// (and anything else) compares the converted column.
func (b *qb) cmp(f Field) (string, func(string) string) {
	col := b.d.QuoteIdent(f.Name)
	same := func(v string) string { return v }
	t := strings.ToLower(f.DBType)
	if b.tz == "" || f.Type != TypeDateTime || t == "date" {
		return col, same
	}
	switch b.d.Name {
	case "postgres":
		switch {
		case t == "timestamptz" || strings.Contains(t, "with time zone"):
			return col, func(v string) string { return "(CAST(" + v + " AS timestamp) AT TIME ZONE '" + b.tz + "')" }
		case t == "timestamp" || strings.HasPrefix(t, "timestamp without"):
			return col, func(v string) string {
				return "(CAST(" + v + " AS timestamp) AT TIME ZONE '" + b.tz + "' AT TIME ZONE 'UTC')"
			}
		}
	case "mysql":
		switch t {
		case "timestamp":
			return col, func(v string) string { return "CONVERT_TZ(" + v + ", '" + b.tz + "', @@session.time_zone)" }
		case "datetime":
			return col, func(v string) string { return "CONVERT_TZ(" + v + ", '" + b.tz + "', '+00:00')" }
		}
	}
	return b.col(f), same
}
