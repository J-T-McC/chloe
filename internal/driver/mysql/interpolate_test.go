package mysql

import "testing"

func TestInterpolateSafe(t *testing.T) {
	for cs, want := range map[string]bool{"utf8mb4": true, "UTF8": true, "latin1": true, "gbk": false, "big5": false, "sjis": false, "cp932": false} {
		if got := interpolateSafe(cs); got != want {
			t.Errorf("interpolateSafe(%q) = %v, want %v", cs, got, want)
		}
	}
}
