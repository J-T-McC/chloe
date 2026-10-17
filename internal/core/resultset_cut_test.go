package core

import (
	"slices"
	"strings"
	"testing"

	"chloe/internal/model"
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

// fakeRows serves n rows of one value.
type fakeRows struct {
	n int
	v string
}

func (r *fakeRows) Columns() []model.ResultColumn { return []model.ResultColumn{{Name: "v"}} }
func (r *fakeRows) HasRows() bool                 { return true }
func (r *fakeRows) RowsAffected() int64           { return 0 }
func (r *fakeRows) Err() error                    { return nil }
func (r *fakeRows) Close() error                  { return nil }
func (r *fakeRows) Next(dst []*string) bool {
	if r.n == 0 {
		return false
	}
	r.n--
	v := r.v
	dst[0] = &v
	return true
}

func TestFillByteBudget(t *testing.T) {
	read := func(rows, maxRows int, maxBytes int64) (int, bool) {
		r := &fakeRows{n: rows, v: strings.Repeat("x", 1000)}
		rs := newResultSet("r", r.Columns(), func() {})
		rs.fill(r, maxRows, maxBytes, nil)
		n, complete, truncated, err := rs.snapshot()
		if !complete || err != nil {
			t.Fatalf("complete %v err %v", complete, err)
		}
		return n, truncated
	}
	// Each row takes 1,048 bytes, so the 96th crosses 100 KB.
	if n, truncated := read(1000, 1_000_000, 100_000); n != 96 || !truncated {
		t.Fatalf("byte budget: %d rows, truncated %v", n, truncated)
	}
	if n, truncated := read(50, 1_000_000, 100_000); n != 50 || truncated {
		t.Fatalf("under budget: %d rows, truncated %v", n, truncated)
	}
	if n, truncated := read(1000, 10, 0); n != 10 || !truncated {
		t.Fatalf("row cap: %d rows, truncated %v", n, truncated)
	}
}
