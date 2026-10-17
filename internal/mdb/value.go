package mdb

import (
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// Val is a BSON value as the UI sees it: a type tag plus text, fields or items.
// It round-trips losslessly through FromVal.
type Val struct {
	// T is the BSON type: object, array, string, int, long, double, decimal, bool, null,
	// oid, date, binary, uuid, regex, timestamp, minKey, maxKey, undefined, code, symbol.
	T string `json:"t"`
	// V is the scalar text: the string, the number, the ObjectId in hex, the date in
	// ISO-8601 UTC, base64 binary data, the regex pattern, the timestamp's seconds.
	V string `json:"v,omitempty"`
	// S qualifies V: binary subtype, regex options, timestamp increment.
	S string  `json:"s,omitempty"`
	F []Field `json:"f,omitempty"`
	A []Val   `json:"a,omitempty"`
}

// Field is one name/value pair of a document.
type Field struct {
	K string `json:"k"`
	V Val    `json:"v"`
}

const isoMillis = "2006-01-02T15:04:05.000Z"

// ToVal converts a decoded BSON value (bson.D, bson.A, primitives) to a Val.
func ToVal(v any) Val {
	switch x := v.(type) {
	case nil, bson.Null:
		return Val{T: "null"}
	case bson.D:
		f := make([]Field, len(x))
		for i, e := range x {
			f[i] = Field{K: e.Key, V: ToVal(e.Value)}
		}
		return Val{T: "object", F: f}
	case bson.M:
		d := make(bson.D, 0, len(x))
		for k, v := range x {
			d = append(d, bson.E{Key: k, Value: v})
		}
		return ToVal(d)
	case bson.Raw:
		var d bson.D
		if err := bson.Unmarshal(x, &d); err != nil {
			return Val{T: "string", V: err.Error()}
		}
		return ToVal(d)
	case bson.A:
		a := make([]Val, len(x))
		for i, e := range x {
			a[i] = ToVal(e)
		}
		return Val{T: "array", A: a}
	case []any:
		return ToVal(bson.A(x))
	case string:
		return Val{T: "string", V: x}
	case bool:
		return Val{T: "bool", V: strconv.FormatBool(x)}
	case int32:
		return Val{T: "int", V: strconv.FormatInt(int64(x), 10)}
	case int:
		return Val{T: "long", V: strconv.Itoa(x)}
	case int64:
		return Val{T: "long", V: strconv.FormatInt(x, 10)}
	case float64:
		return Val{T: "double", V: formatDouble(x)}
	case bson.Decimal128:
		return Val{T: "decimal", V: x.String()}
	case bson.ObjectID:
		return Val{T: "oid", V: x.Hex()}
	case bson.DateTime:
		return Val{T: "date", V: x.Time().UTC().Format(isoMillis)}
	case time.Time:
		return Val{T: "date", V: x.UTC().Format(isoMillis)}
	case bson.Binary:
		if x.Subtype == 4 && len(x.Data) == 16 {
			return Val{T: "uuid", V: formatUUID(x.Data)}
		}
		return Val{T: "binary", V: base64.StdEncoding.EncodeToString(x.Data), S: strconv.Itoa(int(x.Subtype))}
	case []byte:
		return Val{T: "binary", V: base64.StdEncoding.EncodeToString(x), S: "0"}
	case bson.Regex:
		return Val{T: "regex", V: x.Pattern, S: x.Options}
	case bson.Timestamp:
		return Val{T: "timestamp", V: strconv.FormatUint(uint64(x.T), 10), S: strconv.FormatUint(uint64(x.I), 10)}
	case bson.MinKey:
		return Val{T: "minKey"}
	case bson.MaxKey:
		return Val{T: "maxKey"}
	case bson.Undefined:
		return Val{T: "undefined"}
	case bson.JavaScript:
		return Val{T: "code", V: string(x)}
	case bson.CodeWithScope:
		return Val{T: "code", V: string(x.Code)}
	case bson.Symbol:
		return Val{T: "symbol", V: string(x)}
	case bson.DBPointer:
		return Val{T: "object", F: []Field{{K: "$ref", V: Val{T: "string", V: x.DB}}, {K: "$id", V: Val{T: "oid", V: x.Pointer.Hex()}}}}
	}
	return Val{T: "string", V: fmt.Sprint(v)}
}

func formatDouble(f float64) string {
	switch {
	case math.IsNaN(f):
		return "NaN"
	case math.IsInf(f, 1):
		return "Infinity"
	case math.IsInf(f, -1):
		return "-Infinity"
	}
	return strconv.FormatFloat(f, 'g', -1, 64)
}

func formatUUID(b []byte) string {
	h := hex.EncodeToString(b)
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}

func parseUUID(s string) ([]byte, error) {
	b, err := hex.DecodeString(strings.ReplaceAll(s, "-", ""))
	if err != nil || len(b) != 16 {
		return nil, fmt.Errorf("invalid UUID %q", s)
	}
	return b, nil
}

// FromVal converts a Val back to a BSON value.
func FromVal(v Val) (any, error) {
	switch v.T {
	case "object":
		d := make(bson.D, len(v.F))
		for i, f := range v.F {
			x, err := FromVal(f.V)
			if err != nil {
				return nil, err
			}
			d[i] = bson.E{Key: f.K, Value: x}
		}
		return d, nil
	case "array":
		a := make(bson.A, len(v.A))
		for i, e := range v.A {
			x, err := FromVal(e)
			if err != nil {
				return nil, err
			}
			a[i] = x
		}
		return a, nil
	}
	return ParseScalar(v.T, v.V, v.S)
}

// ParseScalar parses text as a scalar of BSON type t (see Val.T); s is Val.S.
func ParseScalar(t, text, s string) (any, error) {
	text0 := text
	text = strings.TrimSpace(text)
	switch t {
	case "string":
		return text0, nil
	case "null":
		return nil, nil
	case "bool":
		switch strings.ToLower(text) {
		case "true", "1":
			return true, nil
		case "false", "0":
			return false, nil
		}
		return nil, fmt.Errorf("%q is not a boolean", text)
	case "int":
		n, err := strconv.ParseInt(text, 10, 32)
		if err != nil {
			return nil, fmt.Errorf("%q is not a 32-bit integer", text)
		}
		return int32(n), nil
	case "long":
		n, err := strconv.ParseInt(text, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("%q is not a 64-bit integer", text)
		}
		return n, nil
	case "double":
		switch text {
		case "NaN":
			return math.NaN(), nil
		case "Infinity":
			return math.Inf(1), nil
		case "-Infinity":
			return math.Inf(-1), nil
		}
		f, err := strconv.ParseFloat(text, 64)
		if err != nil {
			return nil, fmt.Errorf("%q is not a number", text)
		}
		return f, nil
	case "decimal":
		d, err := bson.ParseDecimal128(text)
		if err != nil {
			return nil, fmt.Errorf("%q is not a decimal", text)
		}
		return d, nil
	case "oid":
		id, err := bson.ObjectIDFromHex(text)
		if err != nil {
			return nil, fmt.Errorf("%q is not an ObjectId", text)
		}
		return id, nil
	case "date":
		tm, err := ParseDate(text)
		if err != nil {
			return nil, err
		}
		return bson.NewDateTimeFromTime(tm), nil
	case "binary":
		data, err := base64.StdEncoding.DecodeString(text)
		if err != nil {
			return nil, fmt.Errorf("binary data is not base64")
		}
		sub, _ := strconv.Atoi(s)
		return bson.Binary{Subtype: byte(sub), Data: data}, nil
	case "uuid":
		b, err := parseUUID(text)
		if err != nil {
			return nil, err
		}
		return bson.Binary{Subtype: 4, Data: b}, nil
	case "regex":
		return bson.Regex{Pattern: text0, Options: s}, nil
	case "timestamp":
		t, err1 := strconv.ParseUint(text, 10, 32)
		i, err2 := strconv.ParseUint(strings.TrimSpace(s), 10, 32)
		if err1 != nil || (s != "" && err2 != nil) {
			return nil, fmt.Errorf("invalid timestamp")
		}
		return bson.Timestamp{T: uint32(t), I: uint32(i)}, nil
	case "minKey":
		return bson.MinKey{}, nil
	case "maxKey":
		return bson.MaxKey{}, nil
	case "undefined":
		return bson.Undefined{}, nil
	case "code":
		return bson.JavaScript(text0), nil
	case "symbol":
		return bson.Symbol(text0), nil
	}
	return nil, fmt.Errorf("unknown type %q", t)
}

var dateLayouts = []string{
	time.RFC3339Nano, "2006-01-02T15:04:05", "2006-01-02 15:04:05.999999999", "2006-01-02 15:04:05",
	"2006-01-02T15:04", "2006-01-02 15:04", "2006-01-02",
}

// ParseDate accepts ISO-8601 dates with or without a time and zone (UTC when absent).
func ParseDate(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	for _, l := range dateLayouts {
		if t, err := time.Parse(l, s); err == nil {
			return t, nil
		}
	}
	if ms, err := strconv.ParseInt(s, 10, 64); err == nil {
		return time.UnixMilli(ms).UTC(), nil
	}
	return time.Time{}, fmt.Errorf("%q is not a date", s)
}

// ---- shell syntax ----------------------------------------------------------------

// Format renders v in mongo shell syntax. indent "" gives a single line.
// quoteKeys writes every key as a double-quoted string (JSON view style);
// otherwise identifiers stay bare and strings use single quotes (mongosh style).
func Format(v Val, indent string, quoteKeys bool) string {
	var b strings.Builder
	f := formatter{b: &b, indent: indent, json: quoteKeys}
	f.val(v, 0)
	return b.String()
}

type formatter struct {
	b      *strings.Builder
	indent string
	json   bool
}

func (f formatter) str(s string) string {
	if f.json {
		return strconv.Quote(s)
	}
	q := strconv.Quote(s)
	q = strings.ReplaceAll(q[1:len(q)-1], `\"`, `"`)
	return "'" + strings.ReplaceAll(q, "'", `\'`) + "'"
}

func (f formatter) key(k string) string {
	if !f.json && isIdent(k) {
		return k
	}
	return f.str(k)
}

func isIdent(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		if !(r == '_' || r == '$' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || i > 0 && r >= '0' && r <= '9') {
			return false
		}
	}
	return true
}

func (f formatter) nl(depth int) {
	if f.indent == "" {
		f.b.WriteByte(' ')
		return
	}
	f.b.WriteByte('\n')
	f.b.WriteString(strings.Repeat(f.indent, depth))
}

func (f formatter) val(v Val, depth int) {
	b := f.b
	switch v.T {
	case "object":
		if len(v.F) == 0 {
			b.WriteString("{}")
			return
		}
		b.WriteByte('{')
		for i, fl := range v.F {
			if i > 0 {
				b.WriteByte(',')
			}
			f.nl(depth + 1)
			b.WriteString(f.key(fl.K))
			b.WriteString(": ")
			f.val(fl.V, depth+1)
		}
		f.nl(depth)
		b.WriteByte('}')
	case "array":
		if len(v.A) == 0 {
			b.WriteString("[]")
			return
		}
		b.WriteByte('[')
		for i, e := range v.A {
			if i > 0 {
				b.WriteByte(',')
			}
			f.nl(depth + 1)
			f.val(e, depth+1)
		}
		f.nl(depth)
		b.WriteByte(']')
	default:
		b.WriteString(f.scalar(v))
	}
}

func (f formatter) scalar(v Val) string {
	q := func(s string) string {
		if f.json {
			return strconv.Quote(s)
		}
		return f.str(s)
	}
	switch v.T {
	case "string":
		return f.str(v.V)
	case "int", "bool":
		return v.V
	case "double":
		// An integral double would read back as an int32, so it says what it is.
		if !strings.ContainsAny(v.V, ".eEN") && !strings.HasSuffix(v.V, "Infinity") {
			return "Double(" + v.V + ")"
		}
		return v.V
	case "long":
		return "NumberLong(" + q(v.V) + ")"
	case "decimal":
		return "NumberDecimal(" + q(v.V) + ")"
	case "null":
		return "null"
	case "oid":
		return "ObjectId(" + q(v.V) + ")"
	case "date":
		return "ISODate(" + q(v.V) + ")"
	case "binary":
		return "BinData(" + v.S + ", " + q(v.V) + ")"
	case "uuid":
		return "UUID(" + q(v.V) + ")"
	case "regex":
		if regexLiteral(v.V, v.S) {
			return "/" + v.V + "/" + v.S
		}
		// Options JavaScript lacks (x, l) or an awkward pattern: keep it a BSON regex.
		return "BSONRegExp(" + q(v.V) + ", " + q(v.S) + ")"
	case "timestamp":
		return "Timestamp(" + v.V + ", " + v.S + ")"
	case "minKey":
		return "MinKey()"
	case "maxKey":
		return "MaxKey()"
	case "undefined":
		return "undefined"
	case "code":
		return "Code(" + q(v.V) + ")"
	case "symbol":
		return q(v.V)
	}
	return q(v.V)
}

// regexLiteral reports whether /pattern/options reads back as the same regex:
// the JavaScript lexer must end the literal at the closing slash, so the
// pattern can't hold a slash or line break, end in a backslash, or leave a
// character class open.
func regexLiteral(pattern, opts string) bool {
	for _, r := range opts {
		if !strings.ContainsRune("imsu", r) {
			return false
		}
	}
	if pattern == "" || strings.ContainsAny(pattern, "/\n\r\u2028\u2029") {
		return false
	}
	escaped, class := false, false
	for _, r := range pattern {
		switch {
		case escaped:
			escaped = false
		case r == '\\':
			escaped = true
		case r == '[':
			class = true
		case r == ']':
			class = false
		}
	}
	return !escaped && !class
}

// Cell is the short text shown for a value in a grid cell.
func Cell(v Val) string {
	switch v.T {
	case "object":
		return fmt.Sprintf("(Document) %d Fields", len(v.F))
	case "array":
		return fmt.Sprintf("(Array) %d Elements", len(v.A))
	case "null":
		return "(Null)"
	case "binary":
		return "(Binary)"
	case "regex":
		return "/" + v.V + "/" + v.S
	case "timestamp":
		return "Timestamp(" + v.V + ", " + v.S + ")"
	case "minKey":
		return "MinKey"
	case "maxKey":
		return "MaxKey"
	case "undefined":
		return "(Undefined)"
	}
	return v.V
}
