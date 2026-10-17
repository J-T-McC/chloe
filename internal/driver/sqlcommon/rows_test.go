package sqlcommon

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"io"
	"testing"
)

// fakeDriver serves rows of text-protocol []byte cells (as MySQL sends them),
// reusing one buffer the way drivers do.
type fakeDriver struct{}
type fakeConn struct{}
type fakeRows struct {
	n, left int
	buf     []byte
}

func (fakeDriver) Open(string) (driver.Conn, error)  { return fakeConn{}, nil }
func (fakeConn) Prepare(string) (driver.Stmt, error) { return nil, driver.ErrSkip }
func (fakeConn) Close() error                        { return nil }
func (fakeConn) Begin() (driver.Tx, error)           { return nil, driver.ErrSkip }
func (fakeConn) QueryContext(context.Context, string, []driver.NamedValue) (driver.Rows, error) {
	return &fakeRows{n: 10, left: 1000, buf: []byte("some text value")}, nil
}
func (r *fakeRows) Columns() []string {
	return []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j"}[:r.n]
}
func (r *fakeRows) Close() error { return nil }
func (r *fakeRows) Next(dst []driver.Value) error {
	if r.left == 0 {
		return io.EOF
	}
	r.left--
	r.buf[0]++ // the next row overwrites the buffer
	for i := range dst {
		if i == 3 {
			dst[i] = nil
		} else {
			dst[i] = r.buf
		}
	}
	return nil
}

func init() { sql.Register("sqlcommon-fake", fakeDriver{}) }

func readFake(t testing.TB) [][]*string {
	db, _ := sql.Open("sqlcommon-fake", "")
	defer db.Close()
	sr, err := db.Query("x")
	if err != nil {
		t.Fatal(err)
	}
	r, err := NewRows(sr, 0, FormatValue)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	var out [][]*string
	for {
		row := make([]*string, len(r.Columns()))
		if !r.Next(row) {
			break
		}
		out = append(out, row)
	}
	if err := r.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// Cells are copied out of the driver's buffer before it's reused.
func TestRowsCopyDriverBytes(t *testing.T) {
	rows := readFake(t)
	if len(rows) != 1000 || rows[3][3] != nil {
		t.Fatalf("%d rows", len(rows))
	}
	if a, b := *rows[0][0], *rows[1][0]; a == b || a[1:] != "ome text value" || *rows[0][9] != a {
		t.Fatalf("rows share the driver's buffer: %q %q", a, b)
	}
}

func BenchmarkRowsNext(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		readFake(b)
	}
}
