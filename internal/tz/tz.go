// Package tz reads stored date/times and converts them between time zones. It is
// a leaf package, so drivers can use it without importing BI.
package tz

import (
	"strings"
	"time"
	// Zone names must load on machines without a zoneinfo database (Windows).
	_ "time/tzdata"
)

// Location is a checked zone's location; UTC when unset.
func Location(tz string) *time.Location {
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

// In converts a stored date/time to wall-clock time in tz, as SQLite's
// chloe_tz function does for live SQLite sources and archives.
func In(s, tz string) (string, bool) {
	t, ok := ParseDateTime(s)
	if !ok {
		return "", false
	}
	return t.In(Location(tz)).Format("2006-01-02 15:04:05"), true
}
