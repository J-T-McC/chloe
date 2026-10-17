package mdb

import (
	"bufio"
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// ExportOptions select documents and the file format of a collection export.
type ExportOptions struct {
	// Format: json (an array), jsonl (one document per line) or csv.
	Format string `json:"format"`
	// Canonical writes canonical Extended JSON (every number typed); otherwise relaxed.
	Canonical bool `json:"canonical"`
	// Filter, Sort and Projection are documents in shell syntax.
	Filter     string `json:"filter"`
	Sort       string `json:"sort"`
	Projection string `json:"projection"`
	Limit      int64  `json:"limit"`
	// Fields are the CSV columns as dotted paths; empty takes them from the first 1000 documents.
	Fields []string `json:"fields"`
}

// Export writes a collection's documents to w; progress is called with the count so far.
func (c *Client) Export(ctx context.Context, db, coll string, o ExportOptions, w io.Writer, progress func(n int64)) (int64, error) {
	q := Query{DB: db, Coll: coll, Filter: o.Filter, Sort: o.Sort, Projection: o.Projection}
	filter, sort, proj, err := q.parse()
	if err != nil {
		return 0, err
	}
	fo := options.Find()
	if sort != nil {
		fo.SetSort(sort)
	}
	if proj != nil {
		fo.SetProjection(proj)
	}
	if o.Limit > 0 {
		fo.SetLimit(o.Limit)
	}
	col := c.cl.Database(db).Collection(coll)
	if o.Format == "csv" && len(o.Fields) == 0 {
		if o.Fields, err = fieldPaths(ctx, col, filter, proj); err != nil {
			return 0, err
		}
	}
	cur, err := col.Find(ctx, filter, fo)
	if err != nil {
		return 0, err
	}
	defer cur.Close(ctx)
	bw := bufio.NewWriterSize(w, 1<<16)
	var n int64
	var cw *csv.Writer
	switch o.Format {
	case "json":
		bw.WriteString("[")
	case "csv":
		cw = csv.NewWriter(bw)
		if err := cw.Write(o.Fields); err != nil {
			return 0, err
		}
	case "jsonl":
	default:
		return 0, fmt.Errorf("unsupported export format %q", o.Format)
	}
	for cur.Next(ctx) {
		if o.Format == "csv" {
			var d bson.D
			if err := bson.Unmarshal(cur.Current, &d); err != nil {
				return n, err
			}
			row := make([]string, len(o.Fields))
			for i, f := range o.Fields {
				row[i] = csvCell(lookup(d, f))
			}
			if err := cw.Write(row); err != nil {
				return n, err
			}
		} else {
			b, err := bson.MarshalExtJSON(cur.Current, o.Canonical, false)
			if err != nil {
				return n, err
			}
			if o.Format == "json" {
				if n > 0 {
					bw.WriteString(",")
				}
				bw.WriteString("\n  ")
			}
			bw.Write(b)
			if o.Format == "jsonl" {
				bw.WriteByte('\n')
			}
		}
		n++
		if n%500 == 0 && progress != nil {
			progress(n)
		}
	}
	if err := cur.Err(); err != nil {
		return n, err
	}
	if cw != nil {
		cw.Flush()
		if err := cw.Error(); err != nil {
			return n, err
		}
	}
	if o.Format == "json" {
		if n > 0 {
			bw.WriteString("\n")
		}
		bw.WriteString("]\n")
	}
	if progress != nil {
		progress(n)
	}
	return n, bw.Flush()
}

// fieldPaths collects the dotted paths of scalar values in the first documents
// (nested documents are flattened, arrays stay whole).
func fieldPaths(ctx context.Context, col *mongo.Collection, filter, proj bson.D) ([]string, error) {
	fo := options.Find().SetLimit(1000)
	if proj != nil {
		fo.SetProjection(proj)
	}
	cur, err := col.Find(ctx, filter, fo)
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)
	seen := map[string]bool{}
	var out []string
	var walk func(prefix string, d bson.D)
	walk = func(prefix string, d bson.D) {
		for _, e := range d {
			p := prefix + e.Key
			if sub, ok := e.Value.(bson.D); ok && len(sub) > 0 {
				walk(p+".", sub)
				continue
			}
			if !seen[p] {
				seen[p] = true
				out = append(out, p)
			}
		}
	}
	for cur.Next(ctx) {
		var d bson.D
		if err := bson.Unmarshal(cur.Current, &d); err != nil {
			return nil, err
		}
		walk("", d)
	}
	return out, cur.Err()
}

// lookup follows a dotted path through nested documents; ok is false when it's missing.
func lookup(d bson.D, path string) any {
	var cur any = d
	for _, part := range strings.Split(path, ".") {
		doc, ok := cur.(bson.D)
		if !ok {
			return missing{}
		}
		found := false
		for _, e := range doc {
			if e.Key == part {
				cur, found = e.Value, true
				break
			}
		}
		if !found {
			return missing{}
		}
	}
	return cur
}

type missing struct{}

// csvCell writes plain values as text and documents or arrays as relaxed Extended JSON.
func csvCell(v any) string {
	switch x := v.(type) {
	case missing, nil, bson.Null:
		return ""
	case string:
		return x
	case bson.D, bson.A:
		b, err := bson.MarshalExtJSON(bson.D{{Key: "v", Value: x}}, false, false)
		if err != nil {
			return ""
		}
		// {"v":...} -> ...
		return string(b[5 : len(b)-1])
	}
	return Cell(ToVal(v))
}

// ---- import ------------------------------------------------------------------------------

// ImportOptions read a file into a collection.
type ImportOptions struct {
	// Format: json (an array or one document per line) or csv.
	Format string `json:"format"`
	// Mode: append (insert), upsert (replace documents with the same _id, insert the rest)
	// or copy (empty the collection first).
	Mode string `json:"mode"`
	// CSV: infer numbers and booleans; otherwise every value is a string.
	InferTypes bool `json:"inferTypes"`
	// CSV: leave out empty values instead of writing empty strings.
	SkipEmpty bool `json:"skipEmpty"`
	// StopOnError ends the import at the first rejected batch.
	StopOnError bool `json:"stopOnError"`
}

// ImportResult counts what an import did.
type ImportResult struct {
	Read     int64    `json:"read"`
	Inserted int64    `json:"inserted"`
	Updated  int64    `json:"updated"`
	Failed   int64    `json:"failed"`
	Errors   []string `json:"errors"`
}

const importBatch = 500

// Import reads documents from r into db.coll.
func (c *Client) Import(ctx context.Context, db, coll string, o ImportOptions, r io.Reader, progress func(ImportResult)) (ImportResult, error) {
	col := c.cl.Database(db).Collection(coll)
	var res ImportResult
	if o.Mode == "copy" {
		if _, err := col.DeleteMany(ctx, bson.D{}); err != nil {
			return res, err
		}
	}
	var batch []bson.D
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		err := c.writeBatch(ctx, col, batch, o.Mode == "upsert", &res)
		batch = batch[:0]
		if progress != nil {
			progress(res)
		}
		if err != nil && o.StopOnError {
			return err
		}
		return ctx.Err()
	}
	next, err := docReader(r, o)
	if err != nil {
		return res, err
	}
	for {
		d, err := next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return res, fmt.Errorf("document %d: %w", res.Read+1, err)
		}
		res.Read++
		batch = append(batch, d)
		if len(batch) == importBatch {
			if err := flush(); err != nil {
				return res, err
			}
		}
	}
	return res, flush()
}

func (c *Client) writeBatch(ctx context.Context, col *mongo.Collection, docs []bson.D, upsert bool, res *ImportResult) error {
	note := func(err error) error {
		msg := WriteError(err).Error()
		if len(res.Errors) < 50 {
			res.Errors = append(res.Errors, msg)
		}
		return errors.New(msg)
	}
	if !upsert {
		all := make([]any, len(docs))
		for i, d := range docs {
			all[i] = d
		}
		r, err := col.InsertMany(ctx, all, options.InsertMany().SetOrdered(false))
		if r != nil {
			res.Inserted += int64(len(r.InsertedIDs))
		}
		if err != nil {
			var bwe mongo.BulkWriteException
			if errors.As(err, &bwe) {
				res.Failed += int64(len(bwe.WriteErrors))
				if r == nil {
					res.Inserted += int64(len(docs) - len(bwe.WriteErrors))
				}
			} else {
				res.Failed += int64(len(docs))
			}
			return note(err)
		}
		return nil
	}
	models := make([]mongo.WriteModel, len(docs))
	for i, d := range docs {
		id, ok := optVal(d, "_id")
		if !ok {
			models[i] = mongo.NewInsertOneModel().SetDocument(d)
			continue
		}
		models[i] = mongo.NewReplaceOneModel().SetFilter(bson.D{{Key: "_id", Value: id}}).SetReplacement(d).SetUpsert(true)
	}
	r, err := col.BulkWrite(ctx, models, options.BulkWrite().SetOrdered(false))
	if r != nil {
		res.Inserted += r.InsertedCount + r.UpsertedCount
		res.Updated += r.ModifiedCount
	}
	if err != nil {
		var bwe mongo.BulkWriteException
		if errors.As(err, &bwe) {
			res.Failed += int64(len(bwe.WriteErrors))
		} else {
			res.Failed += int64(len(docs))
		}
		return note(err)
	}
	return nil
}

// docReader returns a function yielding the file's documents one by one (io.EOF at the end).
func docReader(r io.Reader, o ImportOptions) (func() (bson.D, error), error) {
	br := bufio.NewReaderSize(r, 1<<16)
	// Skip a UTF-8 byte order mark.
	if b, _ := br.Peek(3); bytes.Equal(b, []byte{0xEF, 0xBB, 0xBF}) {
		br.Discard(3)
	}
	if o.Format == "csv" {
		cr := csv.NewReader(br)
		cr.FieldsPerRecord = -1
		header, err := cr.Read()
		if err != nil {
			return nil, fmt.Errorf("reading the header: %w", err)
		}
		return func() (bson.D, error) {
			rec, err := cr.Read()
			if err != nil {
				return nil, err
			}
			d := bson.D{}
			for i, h := range header {
				if i >= len(rec) {
					break
				}
				if rec[i] == "" && o.SkipEmpty {
					continue
				}
				d = setPath(d, strings.Split(h, "."), csvValue(rec[i], o.InferTypes))
			}
			return d, nil
		}, nil
	}
	// JSON: an array of documents, or documents one after another (JSON Lines).
	dec := json.NewDecoder(br)
	dec.UseNumber()
	first := true
	inArray := false
	return func() (bson.D, error) {
		if first {
			first = false
			b, err := peekNonSpace(br)
			if err != nil {
				return nil, err
			}
			if b == '[' {
				if _, err := dec.Token(); err != nil {
					return nil, err
				}
				inArray = true
			}
		}
		if inArray && !dec.More() {
			return nil, io.EOF
		}
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return nil, err
		}
		var d bson.D
		if err := bson.UnmarshalExtJSON(raw, false, &d); err != nil {
			return nil, fmt.Errorf("not a document: %w", err)
		}
		return d, nil
	}, nil
}

func peekNonSpace(br *bufio.Reader) (byte, error) {
	for i := 1; ; i++ {
		b, err := br.Peek(i)
		if err != nil {
			if len(b) == 0 {
				return 0, err
			}
			return 0, io.EOF
		}
		c := b[i-1]
		if c != ' ' && c != '\n' && c != '\r' && c != '\t' {
			return c, nil
		}
	}
}

// csvValue reads a CSV field: numbers and booleans when inferring, JSON documents and
// arrays (as an export writes them), otherwise text.
func csvValue(s string, infer bool) any {
	if !infer {
		return s
	}
	t := strings.TrimSpace(s)
	switch t {
	case "true":
		return true
	case "false":
		return false
	case "":
		return s
	}
	if n, err := strconv.ParseInt(t, 10, 64); err == nil && !(len(t) > 1 && t[0] == '0') {
		if n >= math.MinInt32 && n <= math.MaxInt32 {
			return int32(n)
		}
		return n
	}
	if f, err := strconv.ParseFloat(t, 64); err == nil && strings.ContainsAny(t, ".eE") && !strings.HasPrefix(t, ".") {
		return f
	}
	if (t[0] == '{' && t[len(t)-1] == '}') || (t[0] == '[' && t[len(t)-1] == ']') {
		var d bson.D
		if err := bson.UnmarshalExtJSON([]byte(`{"v":`+t+`}`), false, &d); err == nil && len(d) == 1 {
			return d[0].Value
		}
	}
	return s
}

// setPath sets a dotted path in d, creating nested documents.
func setPath(d bson.D, path []string, v any) bson.D {
	for i := range d {
		if d[i].Key == path[0] {
			if len(path) == 1 {
				d[i].Value = v
				return d
			}
			sub, _ := d[i].Value.(bson.D)
			d[i].Value = setPath(sub, path[1:], v)
			return d
		}
	}
	if len(path) == 1 {
		return append(d, bson.E{Key: path[0], Value: v})
	}
	return append(d, bson.E{Key: path[0], Value: setPath(bson.D{}, path[1:], v)})
}
