package sqlq

import "testing"

func TestPGString(t *testing.T) {
	for in, want := range map[string]string{
		"":                     "''",
		"it's":                 "'it''s'",
		`a\b`:                  `E'a\\b'`,
		`\'; DROP TABLE x; --`: `E'\\''; DROP TABLE x; --'`,
		`\x00ff`:               `E'\\x00ff'`,
	} {
		if got := PGString(in); got != want {
			t.Errorf("PGString(%q) = %s, want %s", in, got, want)
		}
	}
}
