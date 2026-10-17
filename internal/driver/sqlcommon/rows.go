// Package sqlcommon holds helpers shared by database/sql based drivers.
package sqlcommon

import (
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"chloe/internal/model"
)

// Formatter converts a scanned value to its display string. typeName is the
// upper-case database type name of the column.
type Formatter func(v any, typeName string) string

// Rows adapts *sql.Rows to driver.Rows.
type Rows struct {
	rows     *sql.Rows
	cols     []model.ResultColumn
	types    []string
	cells    []cell
	scan     []any // pointers to cells
	fmt      Formatter
	affected int64
	err      error
}

// NewRows wraps rows; rows may be nil for statements without a result set.
func NewRows(rows *sql.Rows, affected int64, f Formatter) (*Rows, error) {
	r := &Rows{rows: rows, affected: affected, fmt: f}
	if rows == nil {
		return r, nil
	}
	cts, err := rows.ColumnTypes()
	if err != nil {
		rows.Close()
		return nil, err
	}
	r.cols = make([]model.ResultColumn, len(cts))
	r.types = make([]string, len(cts))
	r.cells = make([]cell, len(cts))
	r.scan = make([]any, len(cts))
	for i, ct := range cts {
		nullable, _ := ct.Nullable()
		tn := strings.ToUpper(ct.DatabaseTypeName())
		r.types[i] = tn
		r.cols[i] = model.ResultColumn{Name: ct.Name(), Type: tn, Nullable: nullable}
		r.scan[i] = &r.cells[i]
	}
	return r, nil
}

func (r *Rows) Columns() []model.ResultColumn { return r.cols }
func (r *Rows) HasRows() bool                 { return r.rows != nil }
func (r *Rows) RowsAffected() int64           { return r.affected }

func (r *Rows) Next(dst []*string) bool {
	if r.rows == nil || r.err != nil {
		return false
	}
	if !r.rows.Next() {
		return false
	}
	if err := r.rows.Scan(r.scan...); err != nil {
		r.err = err
		return false
	}
	// One allocation holds the row's strings; dst points into it.
	vals := make([]string, len(dst))
	for i := range r.cells {
		c := &r.cells[i]
		if c.v == nil {
			dst[i] = nil
			continue
		}
		vals[i] = r.fmt(c.v, r.types[i])
		dst[i] = &vals[i]
		c.v = nil
	}
	return true
}

// cell takes the driver's value as is. Scanning into *any would copy []byte
// values first, but they are formatted (into new strings) before the next row
// replaces them, so the copy isn't needed.
type cell struct{ v any }

func (c *cell) Scan(src any) error {
	c.v = src
	return nil
}

func (r *Rows) Err() error {
	if r.err != nil {
		return r.err
	}
	if r.rows != nil {
		return r.rows.Err()
	}
	return nil
}

func (r *Rows) Close() error {
	if r.rows != nil {
		return r.rows.Close()
	}
	return nil
}

// IsBinaryType reports whether a column type holds raw bytes.
func IsBinaryType(t string) bool {
	switch t {
	case "BLOB", "TINYBLOB", "MEDIUMBLOB", "LONGBLOB", "BINARY", "VARBINARY", "BYTEA", "GEOMETRY", "BIT":
		return true
	}
	return false
}

// FormatValue is the default formatting shared by engines.
func FormatValue(v any, typeName string) string {
	switch x := v.(type) {
	case []byte:
		if IsBinaryType(typeName) || !utf8.Valid(x) {
			return "0x" + strings.ToUpper(hex.EncodeToString(x))
		}
		return string(x)
	case string:
		return x
	case int64:
		return strconv.FormatInt(x, 10)
	case int32:
		return strconv.FormatInt(int64(x), 10)
	case int16:
		return strconv.FormatInt(int64(x), 10)
	case int8:
		return strconv.FormatInt(int64(x), 10)
	case int:
		return strconv.Itoa(x)
	case uint64:
		return strconv.FormatUint(x, 10)
	case uint32:
		return strconv.FormatUint(uint64(x), 10)
	case float64:
		return strconv.FormatFloat(x, 'g', -1, 64)
	case float32:
		return strconv.FormatFloat(float64(x), 'g', -1, 32)
	case bool:
		if x {
			return "true"
		}
		return "false"
	case time.Time:
		return FormatTime(x, typeName)
	case *big.Int:
		return x.String()
	case map[string]any, []any:
		b, err := json.Marshal(x)
		if err == nil {
			return string(b)
		}
	case fmt.Stringer:
		return x.String()
	}
	return fmt.Sprint(v)
}

// FormatTime formats temporal values the way database clients conventionally show them.
func FormatTime(t time.Time, typeName string) string {
	switch typeName {
	case "DATE":
		return t.Format("2006-01-02")
	case "TIME":
		return t.Format("15:04:05.999999")
	case "TIMESTAMPTZ":
		return t.Format("2006-01-02 15:04:05.999999-07")
	}
	return t.Format("2006-01-02 15:04:05.999999")
}

// EnumValues extracts the values of a MySQL enum('a','b') or set(...) column
// type; other types return nil. Quotes inside values are doubled ('it”s').
func EnumValues(colType string) []string {
	lower := strings.ToLower(colType)
	var rest string
	switch {
	case strings.HasPrefix(lower, "enum("):
		rest = colType[5:]
	case strings.HasPrefix(lower, "set("):
		rest = colType[4:]
	default:
		return nil
	}
	var out []string
	for i := 0; i < len(rest); i++ {
		if rest[i] != '\'' {
			continue
		}
		var b strings.Builder
		for i++; i < len(rest); i++ {
			if rest[i] == '\'' {
				if i+1 < len(rest) && rest[i+1] == '\'' {
					b.WriteByte('\'')
					i++
					continue
				}
				break
			}
			if rest[i] == '\\' && i+1 < len(rest) {
				i++
			}
			b.WriteByte(rest[i])
		}
		out = append(out, b.String())
	}
	return out
}
