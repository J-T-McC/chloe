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

func TestBalanced(t *testing.T) {
	for _, c := range []struct {
		s         string
		backslash bool
		want      bool
	}{
		{"a = 1", false, true},
		{"(a) OR (b)", false, true},
		{"x = ')'", false, true},
		{`x = "("`, false, true},
		{"`a)` > 0", false, true},
		{"x = 'it''s)'", false, true},
		{`x = 'a\') OR (b'`, true, true},
		{`x = 'a\') OR (b'`, false, false},
		{"a) OR (b", false, false},
		{"x = '(", false, false},
		{"(a", false, false},
	} {
		if got := Balanced(c.s, c.backslash); got != c.want {
			t.Errorf("Balanced(%q, %v) = %v", c.s, c.backslash, got)
		}
	}
}
