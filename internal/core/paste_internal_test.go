package core

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestCopyName(t *testing.T) {
	at := time.Date(2026, 11, 26, 15, 30, 45, 0, time.Local)
	const stamp = "_copy_20261126_153045"
	none := func(string) bool { return false }
	takenSet := func(names ...string) func(string) bool {
		set := map[string]bool{}
		for _, n := range names {
			set[n] = true
		}
		return func(s string) bool { return set[s] }
	}
	// tenTaken reports the base name and _2 … _10 taken, whatever their cut.
	tenTaken := func(s string) bool { return !strings.HasSuffix(s, stamp+"_11") }
	mysql, pg, sqlite := identLimits["mysql"], identLimits["postgres"], identLimits["sqlite"]
	long70 := strings.Repeat("a", 70)

	cases := []struct {
		name   string
		table  string
		limit  identLimit
		taken  func(string) bool
		want   string // exact result, when the case pins one
		suffix string // the whole suffix the result must end in
		runes  int    // exact rune count, when the case pins one
		bytes  int    // exact byte count, when the case pins one
		maxB   int    // byte ceiling, when the case pins one
	}{
		{name: "free name is table plus local timestamp", table: "users", limit: mysql, taken: none,
			want: "users" + stamp},
		{name: "taken base name gets _2", table: "users", limit: mysql, taken: takenSet("users" + stamp),
			want: "users" + stamp + "_2"},
		{name: "taken base and _2 gets _3", table: "users", limit: mysql,
			taken: takenSet("users"+stamp, "users"+stamp+"_2"), want: "users" + stamp + "_3"},
		{name: "ten taken names at the mysql limit gives whole _11", table: long70, limit: mysql,
			taken: tenTaken, suffix: stamp + "_11", runes: 64},
		{name: "ten taken names at the postgres limit gives whole _11", table: long70, limit: pg,
			taken: tenTaken, suffix: stamp + "_11", bytes: 63},
		{name: "mysql cuts a long ascii table to 64 characters", table: long70, limit: mysql, taken: none,
			suffix: stamp, runes: 64},
		{name: "mysql counts multibyte characters, not bytes", table: strings.Repeat("é", 60), limit: mysql,
			taken: none, suffix: stamp, runes: 64},
		{name: "postgres cuts a long ascii table to 63 bytes", table: long70, limit: pg, taken: none,
			suffix: stamp, bytes: 63},
		{name: "postgres cuts multibyte table on a rune boundary", table: strings.Repeat("é", 40), limit: pg,
			taken: none, suffix: stamp, maxB: 63},
		{name: "sqlite keeps a long table whole", table: strings.Repeat("a", 200), limit: sqlite, taken: none,
			want: strings.Repeat("a", 200) + stamp},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := copyName(c.table, at, c.limit, c.taken)
			if c.want != "" && got != c.want {
				t.Fatalf("got %q, want %q", got, c.want)
			}
			if !utf8.ValidString(got) {
				t.Fatalf("%q is not valid UTF-8", got)
			}
			if c.suffix != "" && !strings.HasSuffix(got, c.suffix) {
				t.Fatalf("%q does not end in %q", got, c.suffix)
			}
			if c.suffix != "" && !strings.HasPrefix(c.table, strings.TrimSuffix(got, c.suffix)) {
				t.Fatalf("%q does not start with a prefix of the table name", got)
			}
			if n := utf8.RuneCountInString(got); c.runes != 0 && n != c.runes {
				t.Fatalf("%q has %d characters, want %d", got, n, c.runes)
			}
			if c.bytes != 0 && len(got) != c.bytes {
				t.Fatalf("%q has %d bytes, want %d", got, len(got), c.bytes)
			}
			if c.maxB != 0 && len(got) > c.maxB {
				t.Fatalf("%q has %d bytes, want at most %d", got, len(got), c.maxB)
			}
		})
	}
}

func TestCopyNameUsesLocalTime(t *testing.T) {
	local := time.Local
	time.Local = time.FixedZone("UTC-8", -8*3600)
	t.Cleanup(func() { time.Local = local })
	at := time.Date(2026, 11, 26, 23, 30, 45, 0, time.UTC)
	const want = "users_copy_20261126_153045"
	if got := copyName("users", at, identLimits["mysql"], func(string) bool { return false }); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}
