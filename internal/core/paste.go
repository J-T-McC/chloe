package core

import (
	"strconv"
	"time"
	"unicode/utf8"
)

// identLimit is the longest identifier an engine keeps, counted in bytes or in
// characters. The zero value means no limit.
type identLimit struct {
	max   int
	bytes bool
}

// identLimits is keyed on Dialect.Name; MariaDB uses the mysql dialect. Postgres
// silently truncates past 63 bytes, so a longer name must never reach it.
var identLimits = map[string]identLimit{
	"mysql":    {max: 64},
	"postgres": {max: 63, bytes: true},
}

// copyName is the name a pasted table gets: <table>_copy_YYYYMMDD_HHMMSS in
// local time, then _2, _3… until taken reports it free. Only the <table> part
// is cut to fit the limit, so every candidate keeps its whole suffix.
func copyName(table string, at time.Time, limit identLimit, taken func(string) bool) string {
	suffix := "_copy_" + at.Local().Format("20060102_150405")
	for n := 1; ; n++ {
		s := suffix
		if n > 1 {
			s += "_" + strconv.Itoa(n)
		}
		if name := limit.fit(table, s); !taken(name) {
			return name
		}
	}
}

// fit returns base+suffix, with base cut on a rune boundary when the whole
// would pass the limit.
func (l identLimit) fit(base, suffix string) string {
	if l.max == 0 {
		return base + suffix
	}
	room := l.max - l.size(suffix)
	used := 0
	for i := 0; i < len(base); {
		_, w := utf8.DecodeRuneInString(base[i:])
		unit := 1
		if l.bytes {
			unit = w
		}
		if used+unit > room {
			return base[:i] + suffix
		}
		used += unit
		i += w
	}
	return base + suffix
}

func (l identLimit) size(s string) int {
	if l.bytes {
		return len(s)
	}
	return utf8.RuneCountInString(s)
}
