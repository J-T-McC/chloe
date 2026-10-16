package bi

import (
	"fmt"
	"regexp"
	"strings"
	"time"
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

// location is a checked zone's location; UTC when unset.
func location(tz string) *time.Location {
	if loc, err := time.LoadLocation(tz); err == nil && tz != "" {
		return loc
	}
	return time.UTC
}

var zoned = []string{"2006-01-02 15:04:05.999999999-07", "2006-01-02 15:04:05.999999999-07:00", "2006-01-02T15:04:05.999999999Z07:00",
	"2006-01-02 15:04:05.999999999Z07:00", "2006-01-02 15:04:05.999999999 -0700"}
var plain = []string{"2006-01-02 15:04:05.999999999", "2006-01-02T15:04:05.999999999", "2006-01-02 15:04", "2006-01-02T15:04", "2006-01-02"}

// ParseDateTime reads a date/time display string. A time with a zone is
// returned in UTC; one without is taken as UTC.
func ParseDateTime(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	for _, l := range zoned {
		if t, err := time.Parse(l, s); err == nil {
			return t.UTC(), true
		}
	}
	for _, l := range plain {
		if t, err := time.Parse(l, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// InZone converts a stored date/time to wall-clock time in tz, as SQLite's
// chloe_tz function does for live SQLite sources and archives.
func InZone(s, tz string) (string, bool) {
	t, ok := ParseDateTime(s)
	if !ok {
		return "", false
	}
	return t.In(location(tz)).Format("2006-01-02 15:04:05"), true
}

// col is a field as the chart sees it: a date/time converted to the data
// source's time zone (dates without a time aren't shifted).
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
