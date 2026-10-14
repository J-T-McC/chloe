package mdb

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// ---- indexes ---------------------------------------------------------------------

// IndexKey is one key of an index. Type is 1, -1, text, 2dsphere, 2d or hashed.
type IndexKey struct {
	Field string `json:"field"`
	Type  string `json:"type"`
}

type IndexInfo struct {
	Name   string     `json:"name"`
	Keys   []IndexKey `json:"keys"`
	Unique bool       `json:"unique"`
	Sparse bool       `json:"sparse"`
	Hidden bool       `json:"hidden"`
	// TTL is expireAfterSeconds; -1 when the index doesn't expire documents.
	TTL int64 `json:"ttl"`
	// Partial is the partialFilterExpression in shell syntax.
	Partial   string `json:"partial,omitempty"`
	Collation string `json:"collation,omitempty"`
	// Spec is the full index description from listIndexes.
	Spec Val `json:"spec"`
	spec bson.D
}

func (c *Client) Indexes(ctx context.Context, db, coll string) ([]IndexInfo, error) {
	cur, err := c.cl.Database(db).Collection(coll).Indexes().List(ctx)
	if err != nil {
		return nil, err
	}
	var specs []bson.D
	if err := cur.All(ctx, &specs); err != nil {
		return nil, err
	}
	out := make([]IndexInfo, 0, len(specs))
	for _, s := range specs {
		ix := IndexInfo{TTL: -1, Spec: ToVal(s)}
		for _, e := range s {
			switch e.Key {
			case "name":
				ix.Name, _ = e.Value.(string)
			case "key":
				if kd, ok := e.Value.(bson.D); ok {
					for _, k := range kd {
						ix.Keys = append(ix.Keys, IndexKey{Field: k.Key, Type: keyType(k.Value)})
					}
				}
			case "unique":
				ix.Unique, _ = e.Value.(bool)
			case "sparse":
				ix.Sparse, _ = e.Value.(bool)
			case "hidden":
				ix.Hidden, _ = e.Value.(bool)
			case "expireAfterSeconds":
				ix.TTL = toInt64(e.Value)
			case "partialFilterExpression":
				ix.Partial = Format(ToVal(e.Value), "", false)
			case "collation":
				ix.Collation = Format(ToVal(e.Value), "", false)
			}
		}
		// Keep what createIndexes accepts, for copying the index elsewhere.
		for _, e := range s {
			if e.Key != "v" && e.Key != "ns" {
				ix.spec = append(ix.spec, e)
			}
		}
		out = append(out, ix)
	}
	return out, nil
}

func keyType(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	}
	return strconv.FormatInt(toInt64(v), 10)
}

func toInt64(v any) int64 {
	switch x := v.(type) {
	case int32:
		return int64(x)
	case int64:
		return x
	case float64:
		return int64(x)
	case int:
		return int64(x)
	}
	return 0
}

// IndexSpec describes an index to create.
type IndexSpec struct {
	Name   string     `json:"name"`
	Keys   []IndexKey `json:"keys"`
	Unique bool       `json:"unique"`
	Sparse bool       `json:"sparse"`
	Hidden bool       `json:"hidden"`
	TTL    int64      `json:"ttl"` // -1 or 0 with TTLSet false: none
	TTLSet bool       `json:"ttlSet"`
	// Partial and Collation are documents in shell syntax.
	Partial   string `json:"partial"`
	Collation string `json:"collation"`
}

// IndexDoc builds the createIndexes entry for spec.
func IndexDoc(s IndexSpec) (bson.D, error) {
	if len(s.Keys) == 0 {
		return nil, fmt.Errorf("an index needs at least one field")
	}
	keys := bson.D{}
	for _, k := range s.Keys {
		f := strings.TrimSpace(k.Field)
		if f == "" {
			return nil, fmt.Errorf("index field name is empty")
		}
		var v any
		switch k.Type {
		case "", "1":
			v = int32(1)
		case "-1":
			v = int32(-1)
		default:
			v = k.Type
		}
		keys = append(keys, bson.E{Key: f, Value: v})
	}
	d := bson.D{{Key: "key", Value: keys}}
	name := s.Name
	if name == "" {
		parts := make([]string, len(keys))
		for i, k := range keys {
			parts[i] = k.Key + "_" + fmt.Sprint(k.Value)
		}
		name = strings.Join(parts, "_")
	}
	d = append(d, bson.E{Key: "name", Value: name})
	if s.Unique {
		d = append(d, bson.E{Key: "unique", Value: true})
	}
	if s.Sparse {
		d = append(d, bson.E{Key: "sparse", Value: true})
	}
	if s.Hidden {
		d = append(d, bson.E{Key: "hidden", Value: true})
	}
	if s.TTLSet && s.TTL >= 0 {
		d = append(d, bson.E{Key: "expireAfterSeconds", Value: int32(s.TTL)})
	}
	if strings.TrimSpace(s.Partial) != "" {
		v, err := EvalDoc(s.Partial)
		if err != nil {
			return nil, fmt.Errorf("partial filter: %w", err)
		}
		d = append(d, bson.E{Key: "partialFilterExpression", Value: v})
	}
	if strings.TrimSpace(s.Collation) != "" {
		v, err := EvalDoc(s.Collation)
		if err != nil {
			return nil, fmt.Errorf("collation: %w", err)
		}
		d = append(d, bson.E{Key: "collation", Value: v})
	}
	return d, nil
}

func (c *Client) CreateIndex(ctx context.Context, db, coll string, s IndexSpec) error {
	d, err := IndexDoc(s)
	if err != nil {
		return err
	}
	_, err = c.RunCommand(ctx, db, bson.D{{Key: "createIndexes", Value: coll}, {Key: "indexes", Value: bson.A{d}}})
	return err
}

func (c *Client) DropIndex(ctx context.Context, db, coll, name string) error {
	_, err := c.RunCommand(ctx, db, bson.D{{Key: "dropIndexes", Value: coll}, {Key: "index", Value: name}})
	return err
}

// ---- documents ---------------------------------------------------------------------

// Query selects documents of a collection. Filter, Sort and Projection are
// documents in shell syntax; empty means none.
type Query struct {
	DB         string `json:"db"`
	Coll       string `json:"coll"`
	Filter     string `json:"filter"`
	Sort       string `json:"sort"`
	Projection string `json:"projection"`
	Skip       int64  `json:"skip"`
	Limit      int64  `json:"limit"`
}

// FieldInfo is a top-level field seen in a page of documents, with its most common type.
type FieldInfo struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

type Page struct {
	Docs   []Val       `json:"docs"`
	Fields []FieldInfo `json:"fields"`
	// Total counts the documents matching the filter; -1 when not counted.
	Total int64 `json:"total"`
}

func (q Query) parse() (filter, sort, proj bson.D, err error) {
	filter = bson.D{}
	if strings.TrimSpace(q.Filter) != "" {
		if filter, err = EvalDoc(q.Filter); err != nil {
			return nil, nil, nil, fmt.Errorf("filter: %w", err)
		}
	}
	if strings.TrimSpace(q.Sort) != "" {
		if sort, err = EvalDoc(q.Sort); err != nil {
			return nil, nil, nil, fmt.Errorf("sort: %w", err)
		}
	}
	if strings.TrimSpace(q.Projection) != "" {
		if proj, err = EvalDoc(q.Projection); err != nil {
			return nil, nil, nil, fmt.Errorf("projection: %w", err)
		}
	}
	return filter, sort, proj, nil
}

// Find returns one page of documents; with count it also counts the matches.
func (c *Client) Find(ctx context.Context, q Query, count bool) (Page, error) {
	filter, sort, proj, err := q.parse()
	if err != nil {
		return Page{}, err
	}
	o := options.Find().SetSkip(q.Skip)
	if q.Limit > 0 {
		o.SetLimit(q.Limit)
	}
	if sort != nil {
		o.SetSort(sort)
	}
	if proj != nil {
		o.SetProjection(proj)
	}
	coll := c.cl.Database(q.DB).Collection(q.Coll)
	cur, err := coll.Find(ctx, filter, o)
	if err != nil {
		return Page{}, err
	}
	var docs []bson.D
	if err := cur.All(ctx, &docs); err != nil {
		return Page{}, err
	}
	p := PageOf(docs)
	p.Total = -1
	if count {
		if len(filter) == 0 {
			p.Total, err = coll.EstimatedDocumentCount(ctx)
		} else {
			p.Total, err = coll.CountDocuments(ctx, filter)
		}
		if err != nil {
			return Page{}, err
		}
	}
	return p, nil
}

// PageOf converts documents and collects their top-level fields, _id first.
func PageOf(docs []bson.D) Page {
	p := Page{Docs: make([]Val, len(docs)), Total: int64(len(docs))}
	idx := map[string]int{}
	types := []map[string]int{}
	for i, d := range docs {
		v := ToVal(d)
		p.Docs[i] = v
		for _, f := range v.F {
			j, ok := idx[f.K]
			if !ok {
				j = len(p.Fields)
				idx[f.K] = j
				p.Fields = append(p.Fields, FieldInfo{Name: f.K})
				types = append(types, map[string]int{})
			}
			types[j][f.V.T]++
		}
	}
	for j := range p.Fields {
		best, n := "", 0
		for t, c := range types[j] {
			if c > n || c == n && t < best {
				best, n = t, c
			}
		}
		p.Fields[j].Type = best
	}
	if j, ok := idx["_id"]; ok && j > 0 {
		id := p.Fields[j]
		copy(p.Fields[1:j+1], p.Fields[:j])
		p.Fields[0] = id
	}
	return p
}

// InsertDocs inserts a document, or each document of an array, given in shell syntax.
func (c *Client) InsertDocs(ctx context.Context, db, coll, text string) (int, error) {
	v, err := EvalShell(text)
	if err != nil {
		return 0, err
	}
	var docs []any
	switch x := v.(type) {
	case bson.D:
		docs = []any{x}
	case bson.A:
		for _, e := range x {
			if _, ok := e.(bson.D); !ok {
				return 0, fmt.Errorf("every array element must be a document")
			}
		}
		docs = x
	default:
		return 0, fmt.Errorf("expected a document or an array of documents")
	}
	if len(docs) == 0 {
		return 0, nil
	}
	r, err := c.cl.Database(db).Collection(coll).InsertMany(ctx, docs)
	if err != nil {
		return 0, writeError(err)
	}
	return len(r.InsertedIDs), nil
}

func idFilter(id Val) (bson.D, error) {
	v, err := FromVal(id)
	if err != nil {
		return nil, err
	}
	return bson.D{{Key: "_id", Value: v}}, nil
}

var errNoMatch = errors.New("the document no longer exists")

// ReplaceDoc replaces the document with _id id by text (shell syntax). The new
// document keeps id when it has no _id of its own.
func (c *Client) ReplaceDoc(ctx context.Context, db, coll string, id Val, text string) error {
	f, err := idFilter(id)
	if err != nil {
		return err
	}
	doc, err := EvalDoc(text)
	if err != nil {
		return err
	}
	r, err := c.cl.Database(db).Collection(coll).ReplaceOne(ctx, f, doc)
	if err != nil {
		return writeError(err)
	}
	if r.MatchedCount == 0 {
		return errNoMatch
	}
	return nil
}

func (c *Client) DeleteDocs(ctx context.Context, db, coll string, ids []Val) (int64, error) {
	in := make(bson.A, 0, len(ids))
	for _, id := range ids {
		v, err := FromVal(id)
		if err != nil {
			return 0, err
		}
		in = append(in, v)
	}
	r, err := c.cl.Database(db).Collection(coll).DeleteMany(ctx, bson.D{{Key: "_id", Value: bson.D{{Key: "$in", Value: in}}}})
	if err != nil {
		return 0, err
	}
	return r.DeletedCount, nil
}

// FieldEdit changes one field of a document.
type FieldEdit struct {
	ID Val `json:"id"`
	// Path is the dotted field path.
	Path string `json:"path"`
	// Unset removes the field; otherwise Value is its new value.
	Unset bool `json:"unset"`
	Value Val  `json:"value"`
}

func (c *Client) EditField(ctx context.Context, db, coll string, e FieldEdit) error {
	f, err := idFilter(e.ID)
	if err != nil {
		return err
	}
	var upd bson.D
	if e.Unset {
		upd = bson.D{{Key: "$unset", Value: bson.D{{Key: e.Path, Value: ""}}}}
	} else {
		v, err := FromVal(e.Value)
		if err != nil {
			return err
		}
		upd = bson.D{{Key: "$set", Value: bson.D{{Key: e.Path, Value: v}}}}
	}
	r, err := c.cl.Database(db).Collection(coll).UpdateOne(ctx, f, upd)
	if err != nil {
		return writeError(err)
	}
	if r.MatchedCount == 0 {
		return errNoMatch
	}
	return nil
}

// GetDoc reads one document by _id.
func (c *Client) GetDoc(ctx context.Context, db, coll string, id Val) (Val, error) {
	f, err := idFilter(id)
	if err != nil {
		return Val{}, err
	}
	var d bson.D
	if err := c.cl.Database(db).Collection(coll).FindOne(ctx, f).Decode(&d); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return Val{}, errNoMatch
		}
		return Val{}, err
	}
	return ToVal(d), nil
}

// SampleFields lists field paths (dotted, nested documents included) found in
// a sample of documents, for completion and filter builders.
func (c *Client) SampleFields(ctx context.Context, db, coll string, n int) ([]FieldInfo, error) {
	cur, err := c.cl.Database(db).Collection(coll).Aggregate(ctx, bson.A{bson.D{{Key: "$sample", Value: bson.D{{Key: "size", Value: n}}}}})
	if err != nil {
		return nil, err
	}
	var docs []bson.D
	if err := cur.All(ctx, &docs); err != nil {
		return nil, err
	}
	seen := map[string]int{}
	var out []FieldInfo
	var walk func(prefix string, d bson.D, depth int)
	walk = func(prefix string, d bson.D, depth int) {
		for _, e := range d {
			p := prefix + e.Key
			if _, ok := seen[p]; !ok {
				seen[p] = len(out)
				out = append(out, FieldInfo{Name: p, Type: ToVal(e.Value).T})
			}
			if sub, ok := e.Value.(bson.D); ok && depth < 4 {
				walk(p+".", sub, depth+1)
			}
		}
	}
	for _, d := range docs {
		walk("", d, 0)
	}
	return out, nil
}

// writeError turns a driver write exception into its server message; a validation
// failure also names the properties it was about.
func writeError(err error) error {
	if err == nil {
		return nil
	}
	var msg string
	var code int
	var details bson.Raw
	var we mongo.WriteException
	var bwe mongo.BulkWriteException
	switch {
	case errors.As(err, &we) && len(we.WriteErrors) > 0:
		msg, code, details = we.WriteErrors[0].Message, we.WriteErrors[0].Code, we.WriteErrors[0].Details
	case errors.As(err, &bwe) && len(bwe.WriteErrors) > 0:
		msg, code, details = bwe.WriteErrors[0].Message, bwe.WriteErrors[0].Code, bwe.WriteErrors[0].Details
		if n := len(bwe.WriteErrors); n > 1 {
			msg += fmt.Sprintf(" (and %d more)", n-1)
		}
	default:
		return err
	}
	if code == 121 && details != nil {
		if s := validationSummary(details); s != "" {
			msg += ": " + s
		}
	}
	return errors.New(msg)
}

// validationSummary lists the properties a failed $jsonSchema validation mentions.
func validationSummary(details bson.Raw) string {
	var missing, failing []string
	var walk func(v bson.RawValue, depth int)
	walk = func(v bson.RawValue, depth int) {
		if depth > 12 {
			return
		}
		switch v.Type {
		case bson.TypeEmbeddedDocument:
			elems, _ := v.Document().Elements()
			for _, e := range elems {
				switch e.Key() {
				case "missingProperties":
					vals, _ := e.Value().Array().Values()
					for _, m := range vals {
						missing = append(missing, m.StringValue())
					}
				case "propertyName":
					if s, ok := e.Value().StringValueOK(); ok {
						failing = append(failing, s)
					}
				}
				walk(e.Value(), depth+1)
			}
		case bson.TypeArray:
			vals, _ := v.Array().Values()
			for _, x := range vals {
				walk(x, depth+1)
			}
		}
	}
	walk(bson.RawValue{Type: bson.TypeEmbeddedDocument, Value: details}, 0)
	var parts []string
	if len(missing) > 0 {
		parts = append(parts, "missing "+strings.Join(missing, ", "))
	}
	if len(failing) > 0 {
		parts = append(parts, "invalid "+strings.Join(failing, ", "))
	}
	return strings.Join(parts, "; ")
}
