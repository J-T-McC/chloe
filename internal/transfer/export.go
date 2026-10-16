// Package transfer reads and writes data files (CSV, JSON, SQL, Excel, Markdown)
// for import, export and dumps. It works on streams of rows so large tables never
// have to fit in memory.
package transfer

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"

	"github.com/xuri/excelize/v2"

	"chloe/internal/model"
	"chloe/internal/sqlq"
)

type Format string

const (
	CSV      Format = "csv"
	TXT      Format = "txt" // tab separated
	JSON     Format = "json"
	JSONL    Format = "jsonl"
	SQL      Format = "sql"
	XLSX     Format = "xlsx"
	Markdown Format = "md"
)

// Extension returns the usual file extension for a format.
func (f Format) Extension() string {
	if f == TXT {
		return "tsv"
	}
	return string(f)
}

type ExportOptions struct {
	Format         Format `json:"format"`
	IncludeHeaders bool   `json:"includeHeaders"`
	// Delimited text.
	FieldDelimiter  string `json:"fieldDelimiter,omitempty"`  // default "," (CSV) or "\t" (TXT)
	RecordDelimiter string `json:"recordDelimiter,omitempty"` // default "\n"
	TextQualifier   string `json:"textQualifier,omitempty"`   // default `"`; "none" disables quoting
	NullText        string `json:"nullText,omitempty"`        // how NULL is written in text formats
	// SQL INSERT export.
	TableName     string `json:"tableName,omitempty"`     // target table, already quoted
	RowsPerInsert int    `json:"rowsPerInsert,omitempty"` // extended inserts; default 100
	Dialect       string `json:"dialect,omitempty"`       // mysql, postgres or sqlite
	// OverridingSystemValue lets PostgreSQL inserts set GENERATED ALWAYS identity columns.
	OverridingSystemValue bool `json:"overridingSystemValue,omitempty"`
}

// RowWriter writes one result as a file.
type RowWriter interface {
	Begin(cols []model.ResultColumn) error
	Write(row []*string) error
	Close() error
}

// NewWriter creates a writer for the export format.
func NewWriter(w io.Writer, o ExportOptions) (RowWriter, error) {
	switch o.Format {
	case CSV, TXT:
		return newDelimited(w, o), nil
	case JSON, JSONL:
		return &jsonWriter{w: bufio.NewWriterSize(w, 1<<16), lines: o.Format == JSONL}, nil
	case SQL:
		if o.TableName == "" {
			return nil, fmt.Errorf("SQL export needs a table name")
		}
		n := o.RowsPerInsert
		if n <= 0 {
			n = 100
		}
		return &sqlWriter{w: bufio.NewWriterSize(w, 1<<16), table: o.TableName, perInsert: n, dialect: o.Dialect, overriding: o.OverridingSystemValue}, nil
	case XLSX:
		return &xlsxWriter{out: w, headers: o.IncludeHeaders}, nil
	case Markdown:
		return &mdWriter{w: bufio.NewWriterSize(w, 1<<16)}, nil
	}
	return nil, fmt.Errorf("unsupported export format %q", o.Format)
}

// ---- delimited text ------------------------------------------------------------

type delimited struct {
	w       *bufio.Writer
	o       ExportOptions
	field   string
	record  string
	qualify string
	needsQ  string
	headers bool
}

func newDelimited(w io.Writer, o ExportOptions) *delimited {
	d := &delimited{w: bufio.NewWriterSize(w, 1<<16), o: o, field: o.FieldDelimiter, record: o.RecordDelimiter, qualify: o.TextQualifier, headers: o.IncludeHeaders}
	if d.field == "" {
		d.field = ","
		if o.Format == TXT {
			d.field = "\t"
		}
	}
	if d.record == "" {
		d.record = "\n"
	}
	if d.qualify == "" {
		d.qualify = `"`
	} else if d.qualify == "none" {
		d.qualify = ""
	}
	d.needsQ = d.field + "\r\n" + d.qualify
	return d
}

func (d *delimited) cell(s string) string {
	if d.qualify == "" || !strings.ContainsAny(s, d.needsQ) && !strings.HasPrefix(s, " ") && !strings.HasSuffix(s, " ") {
		return s
	}
	return d.qualify + strings.ReplaceAll(s, d.qualify, d.qualify+d.qualify) + d.qualify
}

func (d *delimited) line(cells []string) error {
	for i, c := range cells {
		if i > 0 {
			d.w.WriteString(d.field)
		}
		d.w.WriteString(c)
	}
	_, err := d.w.WriteString(d.record)
	return err
}

func (d *delimited) Begin(cols []model.ResultColumn) error {
	if !d.headers {
		return nil
	}
	cells := make([]string, len(cols))
	for i, c := range cols {
		cells[i] = d.cell(c.Name)
	}
	return d.line(cells)
}

func (d *delimited) Write(row []*string) error {
	cells := make([]string, len(row))
	for i, v := range row {
		if v == nil {
			cells[i] = d.o.NullText
		} else {
			cells[i] = d.cell(*v)
		}
	}
	return d.line(cells)
}

func (d *delimited) Close() error { return d.w.Flush() }

// ---- JSON ------------------------------------------------------------------------

type jsonWriter struct {
	w     *bufio.Writer
	lines bool
	names [][]byte // column names as JSON strings
	kinds []valueKind
	n     int
}

type valueKind int

const (
	kindString valueKind = iota
	kindNumber
	kindBool
	kindJSON
)

func kindOf(t string) valueKind {
	t = strings.ToUpper(t)
	switch {
	case strings.Contains(t, "INT") || t == "DECIMAL" || t == "NUMERIC" || t == "FLOAT" || t == "DOUBLE" || t == "REAL" ||
		strings.HasPrefix(t, "FLOAT") || t == "YEAR" || t == "SERIAL" || t == "BIGSERIAL":
		return kindNumber
	case t == "BOOL" || t == "BOOLEAN":
		return kindBool
	case t == "JSON" || t == "JSONB":
		return kindJSON
	}
	return kindString
}

func (j *jsonWriter) Begin(cols []model.ResultColumn) error {
	j.names = make([][]byte, len(cols))
	j.kinds = make([]valueKind, len(cols))
	for i, c := range cols {
		j.names[i], _ = json.Marshal(c.Name)
		j.kinds[i] = kindOf(c.Type)
	}
	if !j.lines {
		_, err := j.w.WriteString("[\n")
		return err
	}
	return nil
}

func (j *jsonWriter) value(v *string, k valueKind) []byte {
	if v == nil {
		return []byte("null")
	}
	switch k {
	case kindNumber:
		if _, err := strconv.ParseFloat(*v, 64); err == nil {
			return []byte(*v)
		}
	case kindBool:
		switch strings.ToLower(*v) {
		case "true", "t", "1":
			return []byte("true")
		case "false", "f", "0":
			return []byte("false")
		}
	case kindJSON:
		if json.Valid([]byte(*v)) {
			return []byte(*v)
		}
	}
	b, _ := json.Marshal(*v)
	return b
}

func (j *jsonWriter) Write(row []*string) error {
	if !j.lines {
		if j.n > 0 {
			j.w.WriteString(",\n")
		}
		j.w.WriteString("  ")
	}
	j.w.WriteByte('{')
	for i, v := range row {
		if i > 0 {
			j.w.WriteByte(',')
		}
		j.w.Write(j.names[i])
		j.w.WriteByte(':')
		j.w.Write(j.value(v, j.kinds[i]))
	}
	j.w.WriteByte('}')
	if j.lines {
		j.w.WriteByte('\n')
	}
	j.n++
	return nil
}

func (j *jsonWriter) Close() error {
	if !j.lines {
		if j.n > 0 {
			j.w.WriteString("\n")
		}
		j.w.WriteString("]\n")
	}
	return j.w.Flush()
}

// ---- SQL INSERT --------------------------------------------------------------------

type sqlWriter struct {
	w          *bufio.Writer
	table      string
	perInsert  int
	dialect    string
	header     string
	types      []string
	overriding bool
	inBatch    int
}

func (s *sqlWriter) Begin(cols []model.ResultColumn) error {
	q := `"`
	if s.dialect == "mysql" || s.dialect == "" {
		q = "`"
	}
	names := make([]string, len(cols))
	s.types = make([]string, len(cols))
	for i, c := range cols {
		names[i] = q + strings.ReplaceAll(c.Name, q, q+q) + q
		s.types[i] = strings.ToUpper(c.Type)
	}
	over := ""
	if s.overriding {
		over = " OVERRIDING SYSTEM VALUE"
	}
	s.header = "INSERT INTO " + s.table + " (" + strings.Join(names, ", ") + ")" + over + " VALUES\n"
	return nil
}

var (
	// Replacers are built once: building one per cell cost more than the replacing.
	mysqlEscaper = strings.NewReplacer(`\`, `\\`, `'`, `''`, "\n", `\n`, "\r", `\r`, "\x00", `\0`)
	mdEscaper    = strings.NewReplacer("|", `\|`, "\n", "<br>", "\r", "")

	// Raw numbers must be finite decimals: ParseFloat also takes NaN, Inf and hex floats.
	decimalRe  = regexp.MustCompile(`^[+-]?(?:[0-9]+\.?[0-9]*|\.[0-9]+)(?:[eE][+-]?[0-9]+)?$`)
	mysqlHexRe = regexp.MustCompile(`^0x[0-9A-Fa-f]*$`)
	blobHexRe  = regexp.MustCompile(`^0x(?:[0-9A-Fa-f]{2})*$`)
	byteaHexRe = regexp.MustCompile(`^\\x[0-9A-Fa-f]*$`)
)

// Literal renders a display value as a SQL literal for the dialect. Display values come
// from the database (SQLite lets any column hold text), so a value is only written raw
// once it is known to be a number, boolean or hex string; anything else is quoted.
func Literal(v *string, typ string, dialect string) string {
	if v == nil {
		return "NULL"
	}
	t := strings.ToUpper(typ)
	switch kindOf(t) {
	case kindNumber:
		if decimalRe.MatchString(*v) {
			return *v
		}
	case kindBool:
		if dialect == "postgres" {
			switch strings.ToLower(*v) {
			case "t", "f", "true", "false":
				return *v
			}
		}
	}
	if dialect == "sqlite" {
		// Standard strings (no backslash escapes) and X'…' blobs.
		if strings.Contains(t, "BLOB") && blobHexRe.MatchString(*v) {
			return "X'" + (*v)[2:] + "'"
		}
		return "'" + strings.ReplaceAll(*v, "'", "''") + "'"
	}
	if dialect == "postgres" {
		if t == "BYTEA" && byteaHexRe.MatchString(*v) {
			return sqlq.PGString(*v) + "::bytea"
		}
		return sqlq.PGString(*v)
	}
	if (strings.Contains(t, "BLOB") || strings.Contains(t, "BINARY") || t == "BIT" || t == "GEOMETRY") && mysqlHexRe.MatchString(*v) {
		if *v == "0x" {
			return "X''"
		}
		return *v // MySQL hex literal
	}
	return "'" + mysqlEscaper.Replace(*v) + "'"
}

func (s *sqlWriter) Write(row []*string) error {
	if s.inBatch == 0 {
		s.w.WriteString(s.header)
	} else {
		s.w.WriteString(",\n")
	}
	s.w.WriteString("  (")
	for i, v := range row {
		if i > 0 {
			s.w.WriteString(", ")
		}
		s.w.WriteString(Literal(v, s.types[i], s.dialect))
	}
	s.w.WriteString(")")
	s.inBatch++
	if s.inBatch == s.perInsert {
		s.w.WriteString(";\n")
		s.inBatch = 0
	}
	return nil
}

func (s *sqlWriter) Close() error {
	if s.inBatch > 0 {
		s.w.WriteString(";\n")
	}
	return s.w.Flush()
}

// ---- Excel ------------------------------------------------------------------------

type xlsxWriter struct {
	out     io.Writer
	headers bool
	f       *excelize.File
	sw      *excelize.StreamWriter
	kinds   []valueKind
	row     int
}

func (x *xlsxWriter) Begin(cols []model.ResultColumn) error {
	x.f = excelize.NewFile()
	sw, err := x.f.NewStreamWriter("Sheet1")
	if err != nil {
		return err
	}
	x.sw = sw
	x.kinds = make([]valueKind, len(cols))
	for i, c := range cols {
		x.kinds[i] = kindOf(c.Type)
	}
	x.row = 1
	if x.headers {
		cells := make([]any, len(cols))
		bold, _ := x.f.NewStyle(&excelize.Style{Font: &excelize.Font{Bold: true}})
		for i, c := range cols {
			cells[i] = excelize.Cell{Value: c.Name, StyleID: bold}
		}
		if err := x.sw.SetRow("A1", cells); err != nil {
			return err
		}
		x.row = 2
		_ = x.sw.SetPanes(&excelize.Panes{Freeze: true, YSplit: 1, TopLeftCell: "A2", ActivePane: "bottomLeft"})
	}
	return nil
}

func (x *xlsxWriter) Write(row []*string) error {
	if x.row > 1_048_576 {
		return fmt.Errorf("Excel sheets are limited to 1,048,576 rows")
	}
	cells := make([]any, len(row))
	for i, v := range row {
		switch {
		case v == nil:
			cells[i] = nil
		case x.kinds[i] == kindNumber:
			if f, err := strconv.ParseFloat(*v, 64); err == nil && len(*v) < 16 {
				cells[i] = f
			} else {
				cells[i] = *v // keep precision of large decimals as text
			}
		default:
			cells[i] = *v
		}
	}
	cell, _ := excelize.CoordinatesToCellName(1, x.row)
	x.row++
	return x.sw.SetRow(cell, cells)
}

func (x *xlsxWriter) Close() error {
	if x.f == nil {
		return nil
	}
	if err := x.sw.Flush(); err != nil {
		return err
	}
	_, err := x.f.WriteTo(x.out)
	x.f.Close()
	return err
}

// ---- Markdown ------------------------------------------------------------------------

type mdWriter struct{ w *bufio.Writer }

func mdCell(s string) string {
	return mdEscaper.Replace(s)
}

func (m *mdWriter) Begin(cols []model.ResultColumn) error {
	names := make([]string, len(cols))
	seps := make([]string, len(cols))
	for i, c := range cols {
		names[i] = mdCell(c.Name)
		seps[i] = "---"
		if kindOf(c.Type) == kindNumber {
			seps[i] = "---:"
		}
	}
	fmt.Fprintf(m.w, "| %s |\n| %s |\n", strings.Join(names, " | "), strings.Join(seps, " | "))
	return nil
}

func (m *mdWriter) Write(row []*string) error {
	cells := make([]string, len(row))
	for i, v := range row {
		if v == nil {
			cells[i] = "*NULL*"
		} else {
			cells[i] = mdCell(*v)
		}
	}
	_, err := fmt.Fprintf(m.w, "| %s |\n", strings.Join(cells, " | "))
	return err
}

func (m *mdWriter) Close() error { return m.w.Flush() }
