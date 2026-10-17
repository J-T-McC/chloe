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
	for s, want := range map[string]bool{
		"a = 1":             true,
		"(a) OR (b)":        true,
		"x = ')'":           true,
		`x = "("`:           true,
		"`a)` > 0":          true,
		`x <> _latin1\')\'`: true,
		"x = 'it''s)'":      true,
		"a) OR (b":          false,
		"x = '(":            false,
		"(a":                false,
	} {
		if got := Balanced(s); got != want {
			t.Errorf("Balanced(%q) = %v", s, got)
		}
	}
}
