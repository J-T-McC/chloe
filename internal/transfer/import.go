package transfer

import (
	"bufio"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/xuri/excelize/v2"
	"golang.org/x/text/encoding/htmlindex"
	"golang.org/x/text/transform"
)

// ImportOptions describe how to read a source file.
type ImportOptions struct {
	Format         Format `json:"format"`
	Encoding       string `json:"encoding,omitempty"`       // e.g. utf-8, windows-1252, shift_jis; default utf-8
	FieldDelimiter string `json:"fieldDelimiter,omitempty"` // default "," (CSV) / "\t" (TXT)
	// HeaderRow is the 1-based line holding field names; 0 means the file has none.
	HeaderRow int `json:"headerRow"`
	// FirstDataRow is the 1-based first line of data (default HeaderRow+1).
	FirstDataRow int    `json:"firstDataRow,omitempty"`
	Sheet        string `json:"sheet,omitempty"` // Excel sheet; default the first
	EmptyAsNull  bool   `json:"emptyAsNull"`
	NullText     string `json:"nullText,omitempty"` // a cell equal to this is NULL (e.g. "NULL", "\N")
}

// Source yields the rows of a file.
type Source interface {
	Columns() []string
	// Next returns the next row, or io.EOF.
	Next() ([]*string, error)
	Close() error
}

// Open reads a file for import.
func Open(path string, o ImportOptions) (Source, error) {
	switch o.Format {
	case CSV, TXT:
		return openDelimited(path, o)
	case JSON, JSONL:
		return openJSON(path, o)
	case XLSX:
		return openXLSX(path, o)
	}
	return nil, fmt.Errorf("unsupported import format %q", o.Format)
}

// DetectFormat guesses a format from the file extension.
func DetectFormat(path string) Format {
	p := strings.ToLower(path)
	switch {
	case strings.HasSuffix(p, ".csv"):
		return CSV
	case strings.HasSuffix(p, ".tsv"), strings.HasSuffix(p, ".tab"), strings.HasSuffix(p, ".txt"):
		return TXT
	case strings.HasSuffix(p, ".jsonl"), strings.HasSuffix(p, ".ndjson"):
		return JSONL
	case strings.HasSuffix(p, ".json"):
		return JSON
	case strings.HasSuffix(p, ".xlsx"), strings.HasSuffix(p, ".xlsm"):
		return XLSX
	}
	return CSV
}

// Preview reads the columns and up to n rows.
func Preview(path string, o ImportOptions, n int) ([]string, [][]*string, error) {
	src, err := Open(path, o)
	if err != nil {
		return nil, nil, err
	}
	defer src.Close()
	var rows [][]*string
	for len(rows) < n {
		r, err := src.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return src.Columns(), rows, err
		}
		rows = append(rows, r)
	}
	return src.Columns(), rows, nil
}

func (o ImportOptions) cell(s string) *string {
	if (o.EmptyAsNull && s == "") || (o.NullText != "" && s == o.NullText) {
		return nil
	}
	return &s
}

// ---- delimited text -------------------------------------------------------------

type delimitedSource struct {
	f    *os.File
	r    *csv.Reader
	cols []string
	o    ImportOptions
	line int
}

func decoder(r io.Reader, encoding string) (io.Reader, error) {
	br := bufio.NewReader(r)
	// Strip a UTF-8 byte order mark, common in files saved by Excel.
	if b, _ := br.Peek(3); len(b) == 3 && b[0] == 0xEF && b[1] == 0xBB && b[2] == 0xBF {
		br.Discard(3)
	}
	if encoding == "" || strings.EqualFold(encoding, "utf-8") || strings.EqualFold(encoding, "utf8") {
		return br, nil
	}
	enc, err := htmlindex.Get(encoding)
	if err != nil {
		return nil, fmt.Errorf("unknown encoding %q", encoding)
	}
	return transform.NewReader(br, enc.NewDecoder()), nil
}

func openDelimited(path string, o ImportOptions) (Source, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	rd, err := decoder(f, o.Encoding)
	if err != nil {
		f.Close()
		return nil, err
	}
	delim := o.FieldDelimiter
	if delim == "" {
		delim = ","
		if o.Format == TXT {
			delim = "\t"
		}
	}
	r, size := utf8.DecodeRuneInString(delim)
	if size != len(delim) {
		f.Close()
		return nil, fmt.Errorf("field delimiter must be a single character")
	}
	cr := csv.NewReader(rd)
	cr.Comma = r
	cr.FieldsPerRecord = -1
	cr.LazyQuotes = true // real-world files often have stray quotes
	s := &delimitedSource{f: f, r: cr, o: o}
	first := o.FirstDataRow
	if first == 0 {
		first = o.HeaderRow + 1
	}
	// Advance to the header and first data row.
	for s.line+1 < first {
		rec, err := cr.Read()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			f.Close()
			return nil, err
		}
		s.line++
		if s.line == o.HeaderRow {
			s.cols = append([]string(nil), rec...)
		}
	}
	// Without a header row, columns are named by position as rows are read.
	return s, nil
}

func (s *delimitedSource) Columns() []string { return s.cols }

func (s *delimitedSource) Next() ([]*string, error) {
	rec, err := s.r.Read()
	if err != nil {
		return nil, err
	}
	s.line++
	if s.cols == nil || len(rec) > len(s.cols) {
		for i := len(s.cols); i < len(rec); i++ {
			s.cols = append(s.cols, fmt.Sprintf("column%d", i+1))
		}
	}
	row := make([]*string, len(s.cols))
	for i := range s.cols {
		if i < len(rec) {
			row[i] = s.o.cell(rec[i])
		}
	}
	return row, nil
}

func (s *delimitedSource) Close() error { return s.f.Close() }

// ---- JSON ---------------------------------------------------------------------------

// jsonSource reads a JSON array of objects, or one object per line (JSONL). Columns
// are the union of keys, in order of first appearance; the file is scanned once up
// front for the keys, then streamed.
type jsonSource struct {
	f    *os.File
	dec  *json.Decoder
	cols []string
	idx  map[string]int
	o    ImportOptions
	arr  bool
}

func openJSON(path string, o ImportOptions) (Source, error) {
	cols, err := jsonKeys(path)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	rd, err := decoder(f, o.Encoding)
	if err != nil {
		f.Close()
		return nil, err
	}
	s := &jsonSource{f: f, dec: json.NewDecoder(rd), cols: cols, idx: map[string]int{}, o: o}
	s.dec.UseNumber()
	for i, c := range cols {
		s.idx[c] = i
	}
	if err := s.start(); err != nil {
		f.Close()
		return nil, err
	}
	return s, nil
}

func (s *jsonSource) start() error {
	tok, err := s.dec.Token()
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err != nil {
		return err
	}
	if d, ok := tok.(json.Delim); ok && d == '[' {
		s.arr = true
		return nil
	}
	if d, ok := tok.(json.Delim); ok && d == '{' {
		// JSONL: re-open so Next can decode the first object whole.
		if _, err := s.f.Seek(0, io.SeekStart); err != nil {
			return err
		}
		rd, err := decoder(s.f, s.o.Encoding)
		if err != nil {
			return err
		}
		s.dec = json.NewDecoder(rd)
		s.dec.UseNumber()
		return nil
	}
	return errors.New("JSON import expects an array of objects or one object per line")
}

// jsonKeys scans the file once and returns the union of object keys in order of
// first appearance (Go maps don't keep order, so objects are walked token by token).
func jsonKeys(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	dec := json.NewDecoder(bufio.NewReaderSize(f, 1<<16))
	seen := map[string]bool{}
	var keys []string
	add := func(k string) {
		if !seen[k] {
			seen[k] = true
			keys = append(keys, k)
		}
	}
	tok, err := dec.Token()
	if errors.Is(err, io.EOF) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}
	switch tok {
	case json.Delim('['):
		for dec.More() {
			if err := expectDelim(dec, '{'); err != nil {
				return nil, err
			}
			if err := objectKeys(dec, add); err != nil {
				return nil, err
			}
		}
	case json.Delim('{'):
		// JSON Lines: a sequence of top-level objects.
		for {
			if err := objectKeys(dec, add); err != nil {
				return nil, err
			}
			if err := expectDelim(dec, '{'); errors.Is(err, io.EOF) {
				break
			} else if err != nil {
				return nil, err
			}
		}
	default:
		return nil, errors.New("JSON import expects an array of objects or one object per line")
	}
	return keys, nil
}

func expectDelim(dec *json.Decoder, want json.Delim) error {
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	if tok != want {
		return fmt.Errorf("invalid JSON: expected %q, got %v", want, tok)
	}
	return nil
}

// objectKeys reads the rest of an object whose '{' was consumed, reporting its keys.
func objectKeys(dec *json.Decoder, add func(string)) error {
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		k, ok := tok.(string)
		if !ok {
			return fmt.Errorf("invalid JSON object key %v", tok)
		}
		add(k)
		var skip json.RawMessage
		if err := dec.Decode(&skip); err != nil {
			return err
		}
	}
	_, err := dec.Token() // closing '}'
	return err
}

func (s *jsonSource) Columns() []string { return s.cols }

func (s *jsonSource) Next() ([]*string, error) {
	if s.arr && !s.dec.More() {
		return nil, io.EOF
	}
	var obj map[string]json.RawMessage
	if err := s.dec.Decode(&obj); err != nil {
		return nil, err
	}
	row := make([]*string, len(s.cols))
	for k, raw := range obj {
		i, ok := s.idx[k]
		if !ok {
			continue
		}
		row[i] = jsonCell(raw, s.o)
	}
	return row, nil
}

func jsonCell(raw json.RawMessage, o ImportOptions) *string {
	t := strings.TrimSpace(string(raw))
	switch {
	case t == "null":
		return nil
	case strings.HasPrefix(t, `"`):
		var s string
		_ = json.Unmarshal(raw, &s)
		return o.cell(s)
	}
	// numbers, booleans, nested objects/arrays are kept as JSON text
	return &t
}

func (s *jsonSource) Close() error { return s.f.Close() }

// ---- Excel --------------------------------------------------------------------------

type xlsxSource struct {
	f    *excelize.File
	rows *excelize.Rows
	cols []string
	o    ImportOptions
	line int
}

// Sheets lists the sheets of a workbook.
func Sheets(path string) ([]string, error) {
	f, err := excelize.OpenFile(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return f.GetSheetList(), nil
}

func openXLSX(path string, o ImportOptions) (Source, error) {
	f, err := excelize.OpenFile(path)
	if err != nil {
		return nil, err
	}
	sheet := o.Sheet
	if sheet == "" {
		sheet = f.GetSheetName(0)
	}
	rows, err := f.Rows(sheet)
	if err != nil {
		f.Close()
		return nil, err
	}
	s := &xlsxSource{f: f, rows: rows, o: o}
	first := o.FirstDataRow
	if first == 0 {
		first = o.HeaderRow + 1
	}
	for s.line+1 < first && rows.Next() {
		s.line++
		rec, err := rows.Columns()
		if err != nil {
			f.Close()
			return nil, err
		}
		if s.line == o.HeaderRow {
			s.cols = rec
		}
	}
	return s, nil
}

func (s *xlsxSource) Columns() []string { return s.cols }

func (s *xlsxSource) Next() ([]*string, error) {
	if !s.rows.Next() {
		if err := s.rows.Error(); err != nil {
			return nil, err
		}
		return nil, io.EOF
	}
	rec, err := s.rows.Columns()
	if err != nil {
		return nil, err
	}
	for i := len(s.cols); i < len(rec); i++ {
		s.cols = append(s.cols, fmt.Sprintf("column%d", i+1))
	}
	row := make([]*string, len(s.cols))
	for i := range s.cols {
		if i < len(rec) {
			row[i] = s.o.cell(rec[i])
		} else if !s.o.EmptyAsNull {
			e := ""
			row[i] = &e
		}
	}
	return row, nil
}

func (s *xlsxSource) Close() error {
	s.rows.Close()
	return s.f.Close()
}

// ---- type detection ------------------------------------------------------------------

var (
	intRe      = regexp.MustCompile(`^[-+]?\d+$`)
	decRe      = regexp.MustCompile(`^[-+]?(\d*)\.(\d+)$`)
	dateRe     = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
	dateTimeRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}[ T]\d{2}:\d{2}(:\d{2}(\.\d+)?)?(Z|[+-]\d{2}:?\d{2})?$`)
)

// InferTypes proposes a column type for each column from sample rows.
func InferTypes(rows [][]*string, ncols int, dialect string) []string {
	out := make([]string, ncols)
	for c := 0; c < ncols; c++ {
		isInt, isDec, isBool, isDate, isDT, any := true, true, true, true, true, false
		maxLen, maxInt, maxScale := 0, 0, 0
		var big bool
		for _, r := range rows {
			if c >= len(r) || r[c] == nil || *r[c] == "" {
				continue
			}
			v := strings.TrimSpace(*r[c])
			any = true
			if l := utf8.RuneCountInString(v); l > maxLen {
				maxLen = l
			}
			if intRe.MatchString(v) {
				if n, err := strconv.ParseInt(v, 10, 64); err != nil || n > 2147483647 || n < -2147483648 {
					big = true
				}
				if d := len(strings.TrimLeft(v, "+-")); d > maxInt {
					maxInt = d
				}
			} else {
				isInt = false
				if m := decRe.FindStringSubmatch(v); m != nil {
					if len(m[1]) > maxInt {
						maxInt = len(m[1])
					}
					if len(m[2]) > maxScale {
						maxScale = len(m[2])
					}
				} else {
					isDec = false
				}
			}
			switch strings.ToLower(v) {
			case "true", "false":
			default:
				isBool = false
			}
			if !dateRe.MatchString(v) {
				isDate = false
			}
			if !dateTimeRe.MatchString(v) {
				isDT = false
			}
		}
		switch {
		case !any:
			out[c] = textType(255, dialect)
		case isBool:
			out[c] = "boolean"
		case isInt && big:
			out[c] = "bigint"
		case isInt:
			out[c] = "int"
			if dialect == "postgres" {
				out[c] = "integer"
			}
		case isDec:
			out[c] = fmt.Sprintf("decimal(%d,%d)", min(maxInt+maxScale+2, 65), min(maxScale, 30))
		case isDate:
			out[c] = "date"
		case isDT:
			out[c] = "datetime"
			if dialect == "postgres" {
				out[c] = "timestamp"
			}
		default:
			out[c] = textType(maxLen, dialect)
		}
	}
	return out
}

func textType(maxLen int, dialect string) string {
	if dialect == "postgres" {
		return "text"
	}
	if maxLen > 2000 {
		return "text"
	}
	for _, n := range []int{50, 100, 255, 500, 1000, 2000} {
		if maxLen <= n {
			return fmt.Sprintf("varchar(%d)", n)
		}
	}
	return "text"
}

// Encodings lists commonly used text encodings for the import dialog.
func Encodings() []string {
	e := []string{"utf-8", "utf-16le", "utf-16be", "windows-1252", "iso-8859-1", "iso-8859-15", "windows-1250", "windows-1251",
		"koi8-r", "shift_jis", "euc-jp", "iso-2022-jp", "gbk", "gb18030", "big5", "euc-kr"}
	sort.Strings(e[1:])
	return e
}
