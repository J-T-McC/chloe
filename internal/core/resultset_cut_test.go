package core

import (
	"slices"
	"testing"
)

func TestCutCells(t *testing.T) {
	s := func(v string) *string { return &v }
	long, wide := "abcdefgh", "héllo wörld"
	rows := [][]*string{{s("ok"), nil}, {s(long), s("x")}, {s(wide), s("é")}}
	before := *rows[1][0]
	out, cut := cutCells(rows, 5)
	if !slices.EqualFunc(cut, [][]int{{1, 0}, {2, 0}}, slices.Equal) {
		t.Fatalf("cut %v", cut)
	}
	if *out[1][0] != "abcde" || *out[2][0] != "héllo" || *out[2][1] != "é" || out[0][1] != nil {
		t.Fatalf("out %q %q", *out[1][0], *out[2][0])
	}
	if *rows[1][0] != before || &out[0][0] == nil || out[0][0] != rows[0][0] {
		t.Fatal("the buffered rows changed")
	}
	if out2, cut2 := cutCells(rows[:1], 5); cut2 != nil || &out2[0] != &rows[0] {
		t.Fatal("rows without long values are copied")
	}
}
