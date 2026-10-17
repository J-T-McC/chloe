package transfer

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"chloe/internal/model"
)

func p(s string) *string { return &s }

var cols = []model.ResultColumn{{Name: "id", Type: "INT"}, {Name: "name", Type: "VARCHAR"}, {Name: "price", Type: "DECIMAL"}, {Name: "meta", Type: "JSON"}}
var rows = [][]*string{
	{p("1"), p("Ada, \"the\" first"), p("9.50"), p(`{"a":1}`)},
	{p("2"), nil, p("10"), nil},
	{p("3"), p("multi\nline"), p("0"), p("not json")},
}

func export(t *testing.T, o ExportOptions) string {
	t.Helper()
	var buf bytes.Buffer
	w, err := NewWriter(&buf, o)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Begin(cols); err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if err := w.Write(r); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

func TestExportCSV(t *testing.T) {
	got := export(t, ExportOptions{Format: CSV, IncludeHeaders: true, NullText: "NULL"})
	want := "id,name,price,meta\n1,\"Ada, \"\"the\"\" first\",9.50,\"{\"\"a\"\":1}\"\n2,NULL,10,NULL\n3,\"multi\nline\",0,not json\n"
	if got != want {
		t.Fatalf("got\n%q\nwant\n%q", got, want)
	}
	tsv := export(t, ExportOptions{Format: TXT, RecordDelimiter: "\r\n"})
	if !strings.HasPrefix(tsv, "1\t\"Ada, \"\"the\"\" first\"\t9.50") || !strings.Contains(tsv, "\r\n") {
		t.Fatalf("tsv %q", tsv)
	}
}

func TestExportJSON(t *testing.T) {
	got := export(t, ExportOptions{Format: JSON})
	for _, want := range []string{`{"id":1,"name":"Ada, \"the\" first","price":9.50,"meta":{"a":1}}`, `"name":null`, `"meta":"not json"`} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %s in %s", want, got)
		}
	}
	if !strings.HasPrefix(got, "[\n") || !strings.HasSuffix(got, "]\n") {
		t.Fatalf("array framing %q", got)
	}
	if lines := strings.Count(export(t, ExportOptions{Format: JSONL}), "\n"); lines != 3 {
		t.Fatalf("jsonl lines %d", lines)
	}
}

func TestExportSQL(t *testing.T) {
	got := export(t, ExportOptions{Format: SQL, TableName: "`items`", RowsPerInsert: 2, Dialect: "mysql"})
	want := "INSERT INTO `items` (`id`, `name`, `price`, `meta`) VALUES\n  (1, 'Ada, \"the\" first', 9.50, '{\"a\":1}'),\n  (2, NULL, 10, NULL);\n" +
		"INSERT INTO `items` (`id`, `name`, `price`, `meta`) VALUES\n  (3, 'multi\\nline', 0, 'not json');\n"
	if got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
	if Literal(p(`\x0102`), "bytea", "postgres") != `E'\\x0102'::bytea` {
		t.Fatal("bytea literal")
	}
}

func TestExportMarkdown(t *testing.T) {
	got := export(t, ExportOptions{Format: Markdown})
	if !strings.Contains(got, "| id | name | price | meta |\n| ---: | --- | ---: | --- |") || !strings.Contains(got, "multi<br>line") || !strings.Contains(got, "*NULL*") {
		t.Fatal(got)
	}
}

func readAll(t *testing.T, path string, o ImportOptions) ([]string, [][]*string) {
	t.Helper()
	src, err := Open(path, o)
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	var out [][]*string
	for {
		r, err := src.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, r)
	}
	return src.Columns(), out
}

func write(t *testing.T, name string, data []byte) string {
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestImportCSV(t *testing.T) {
	// BOM, quoted delimiter, embedded newline, empty cell, ragged row.
	path := write(t, "a.csv", []byte("\xEF\xBB\xBFid,name,note\n1,\"a, b\",\"x\ny\"\n2,,\n3,c\n"))
	c, r := readAll(t, path, ImportOptions{Format: CSV, HeaderRow: 1, EmptyAsNull: true})
	if strings.Join(c, "|") != "id|name|note" || len(r) != 3 {
		t.Fatalf("cols %v rows %d", c, len(r))
	}
	if *r[0][1] != "a, b" || *r[0][2] != "x\ny" || r[1][1] != nil || r[2][2] != nil {
		t.Fatalf("cells %v", r)
	}
	// No header: positional names; skipping a title line with FirstDataRow.
	path = write(t, "b.tsv", []byte("title line\n1\tx\n2\ty\n"))
	c, r = readAll(t, path, ImportOptions{Format: TXT, HeaderRow: 0, FirstDataRow: 2})
	if strings.Join(c, "|") != "column1|column2" || len(r) != 2 {
		t.Fatalf("cols %v rows %v", c, r)
	}
	// Windows-1252.
	path = write(t, "c.csv", []byte("name\ncaf\xe9\n"))
	_, r = readAll(t, path, ImportOptions{Format: CSV, HeaderRow: 1, Encoding: "windows-1252"})
	if *r[0][0] != "café" {
		t.Fatalf("encoding %q", *r[0][0])
	}
}

func TestImportJSON(t *testing.T) {
	path := write(t, "a.json", []byte(`[{"b":1,"a":"x"},{"a":null,"c":{"n":[1,2]}}]`))
	c, r := readAll(t, path, ImportOptions{Format: JSON})
	if strings.Join(c, "|") != "b|a|c" || len(r) != 2 {
		t.Fatalf("cols %v rows %d", c, len(r))
	}
	if *r[0][0] != "1" || *r[0][1] != "x" || r[1][1] != nil || *r[1][2] != `{"n":[1,2]}` {
		t.Fatalf("cells %v", r)
	}
	path = write(t, "a.jsonl", []byte("{\"x\":1}\n{\"y\":2,\"x\":3}\n"))
	c, r = readAll(t, path, ImportOptions{Format: JSONL})
	if strings.Join(c, "|") != "x|y" || len(r) != 2 || *r[1][0] != "3" {
		t.Fatalf("jsonl %v %v", c, r)
	}
}

func TestExcelRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.xlsx")
	f, _ := os.Create(path)
	w, _ := NewWriter(f, ExportOptions{Format: XLSX, IncludeHeaders: true})
	w.Begin(cols)
	for _, r := range rows {
		w.Write(r)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	f.Close()
	sheets, err := Sheets(path)
	if err != nil || len(sheets) != 1 {
		t.Fatalf("sheets %v %v", sheets, err)
	}
	c, r := readAll(t, path, ImportOptions{Format: XLSX, HeaderRow: 1, EmptyAsNull: true})
	if strings.Join(c, "|") != "id|name|price|meta" || len(r) != 3 {
		t.Fatalf("cols %v rows %d", c, len(r))
	}
	if *r[0][1] != `Ada, "the" first` || *r[0][2] != "9.5" || r[1][1] != nil {
		t.Fatalf("cells %q %q %v", *r[0][1], *r[0][2], r[1][1])
	}
}

func TestInferTypes(t *testing.T) {
	data := [][]*string{
		{p("1"), p("1.5"), p("true"), p("2026-01-02"), p("2026-01-02 10:00:00"), p("hello"), p("9999999999")},
		{p("-20"), p("10"), p("false"), p("2025-12-31"), p("2026-01-02T10:00:00Z"), p(""), p("1")},
	}
	got := InferTypes(data, 7, "mysql")
	want := []string{"int", "decimal(5,1)", "boolean", "date", "datetime", "varchar(50)", "bigint"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("got %v want %v", got, want)
	}
}

// Display values come from the database, so anything that isn't a well-formed number,
// boolean or hex string must come out quoted.
func TestLiteralQuotesUntrustedValues(t *testing.T) {
	p := func(s string) *string { return &s }
	for _, c := range []struct{ v, typ, dialect, want string }{
		{"0x0aFF", "BLOB", "sqlite", "X'0aFF'"},
		{"0x'); DROP TABLE users; --", "BLOB", "sqlite", "'0x''); DROP TABLE users; --'"},
		{"0xABC", "BLOB", "sqlite", "'0xABC'"},
		{"0xABC", "VARBINARY(4)", "mysql", "0xABC"},
		{"0x", "BLOB", "mysql", "X''"},
		{"0x1 OR 1=1", "BLOB", "mysql", "'0x1 OR 1=1'"},
		{"-1.5e3", "INT", "mysql", "-1.5e3"},
		{"NaN", "DOUBLE", "postgres", "'NaN'"},
		{"0x1p3", "DOUBLE", "sqlite", "'0x1p3'"},
		{"1; DROP TABLE x", "INTEGER", "sqlite", "'1; DROP TABLE x'"},
		{"true", "BOOLEAN", "postgres", "true"},
		{"t) OR (1=1", "BOOLEAN", "postgres", "'t) OR (1=1'"},
		{`\'; DROP TABLE x; --`, "TEXT", "postgres", `E'\\''; DROP TABLE x; --'`},
		{`\x01'`, "BYTEA", "postgres", `E'\\x01'''`},
		// Without type affinity, numbers stay numbers and only the driver's own
		// formatting counts as one.
		{"10", "", "sqlite", "10"},
		{"-2.5", "", "sqlite", "-2.5"},
		{"1e+21", "", "sqlite", "1e+21"},
		{"10", "BLOB", "sqlite", "10"},
		{"007", "", "sqlite", "'007'"},
		{"1.0", "", "sqlite", "'1.0'"},
		{"+1", "", "sqlite", "'+1'"},
		{"0x0A", "", "sqlite", "X'0A'"},
		{"10", "TEXT", "sqlite", "'10'"},
		{"1 OR 1=1", "", "sqlite", "'1 OR 1=1'"},
	} {
		if got := Literal(p(c.v), c.typ, c.dialect); got != c.want {
			t.Errorf("Literal(%q, %s, %s) = %s, want %s", c.v, c.typ, c.dialect, got, c.want)
		}
	}
}

func BenchmarkLiteralMySQLString(b *testing.B) {
	v := "O'Brien\nline two"
	for b.Loop() {
		Literal(&v, "VARCHAR(40)", "mysql")
	}
}

// Number cells that aren't JSON numbers (Postgres NaN and Infinity) are written as strings.
func TestJSONNumbersStayValid(t *testing.T) {
	var j jsonWriter
	for v, want := range map[string]string{"9.50": "9.50", "-0.5e3": "-0.5e3", "NaN": `"NaN"`, "Infinity": `"Infinity"`, "+1": `"+1"`, ".5": `".5"`, "0x10": `"0x10"`} {
		if got := string(j.value(&v, kindNumber)); got != want {
			t.Errorf("value(%q) = %s, want %s", v, got, want)
		}
	}
}
