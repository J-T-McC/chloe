package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"chloe/internal/mdb"
	"chloe/internal/model"
	"chloe/internal/store"
)

// MongoDB tools. Filters, projections, updates, pipelines and documents arrive
// as JSON (Extended JSON for $oid, $date…) and are never evaluated as
// JavaScript in Chloe. Every command runs under a 30 s context deadline, which
// the driver sends to the server as maxTimeMS.
//
// Chloe enforces only its own settings (database access levels, approvals);
// what the database user may do is up to the login the user shares.

const maxMongoWrite = 1000 // documents one insert, update or delete may touch

var mongoEngines = []model.DBType{model.MongoDB}

// Doc is a JSON object from the agent, kept as written: the SDK's typed
// arguments go through a Go map, which would sort keys, and key order matters
// to MongoDB ($sort stages, field order of inserted documents).
type Doc struct{ raw json.RawMessage }

func (d *Doc) UnmarshalJSON(b []byte) error {
	t := bytes.TrimSpace(b)
	if string(t) == "null" {
		return nil
	}
	if len(t) == 0 || t[0] != '{' {
		return errors.New("expected a JSON object")
	}
	d.raw = append(json.RawMessage(nil), t...)
	return nil
}

func (d Doc) set() bool { return len(d.raw) > 0 }

// bson parses the object as Extended JSON (relaxed or canonical).
func (d Doc) bson() (bson.D, error) {
	if !d.set() {
		return bson.D{}, nil
	}
	var out bson.D
	if err := bson.UnmarshalExtJSON(d.raw, false, &out); err != nil {
		return nil, err
	}
	return out, nil
}

var orderedSchemas = map[reflect.Type]*jsonschema.Schema{
	reflect.TypeFor[Doc]():             {Type: "object"},
	reflect.TypeFor[json.RawMessage](): {},
}

// addOrdered is add for tools whose arguments hold Docs: it decodes the raw
// arguments itself so object key order survives.
func addOrdered[In, Out any](s *Server, srv *mcp.Server, epID string, t *mcp.Tool, fn func(context.Context, *call, In) (Out, error)) {
	opts := &jsonschema.ForOptions{TypeSchemas: orderedSchemas}
	in, err := jsonschema.For[In](opts)
	if err != nil {
		panic(fmt.Sprintf("%s: %v", t.Name, err))
	}
	out, err := jsonschema.For[Out](opts)
	if err != nil {
		panic(fmt.Sprintf("%s: %v", t.Name, err))
	}
	t.InputSchema, t.OutputSchema = in, out
	fail := func(err error) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}}}, nil
	}
	srv.AddTool(t, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		ep, err := s.endpoint(epID)
		if err != nil {
			return fail(err)
		}
		var input In
		args := req.Params.Arguments
		if len(bytes.TrimSpace(args)) == 0 {
			args = []byte("{}")
		}
		dec := json.NewDecoder(bytes.NewReader(args))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&input); err != nil {
			return fail(fmt.Errorf("invalid arguments: %w", err))
		}
		c := &call{s: s, ep: ep, start: time.Now(),
			act: Activity{ID: uuid.NewString(), Time: time.Now().UTC().Format(time.RFC3339), Endpoint: ep.Name, Tool: t.Name}}
		o, err := fn(ctx, c, input)
		c.finish(err)
		if err != nil {
			return fail(err)
		}
		c.stamp(&o)
		data, err := json.Marshal(o)
		if err != nil {
			return fail(err)
		}
		return &mcp.CallToolResult{StructuredContent: json.RawMessage(data), Content: []mcp.Content{&mcp.TextContent{Text: string(data)}}}, nil
	})
}

// --- guards -------------------------------------------------------------------

var (
	// mongoWriteStages write from an aggregation.
	mongoWriteStages = []string{"$out", "$merge"}
	// mongoAdminStages report on the whole server (every database, which would
	// get around databases set to Off), or never end.
	mongoAdminStages = []string{"$currentOp", "$listSessions", "$listLocalSessions", "$changeStream", "$listSampledQueries",
		"$listCatalog", "$listClusterCatalog", "$shardedDataDistribution"}
	// mongoSourceStages read another collection, which must be in the same database.
	mongoSourceStages = []string{"$lookup", "$graphLookup", "$unionWith"}
	// mongoUpdateOps are the update operators mongo_update takes.
	mongoUpdateOps = []string{"$set", "$unset", "$inc", "$mul", "$rename", "$min", "$max", "$currentDate",
		"$push", "$pull", "$pullAll", "$addToSet", "$pop", "$setOnInsert", "$bit"}
)

// checkMongo walks a filter, projection, update or pipeline and refuses
// writing stages (a read must not write) and stages that reach other databases
// (which may be Off for this endpoint). Stages are refused at any depth, so
// nested pipelines ($facet, $lookup, $unionWith) are covered too.
func checkMongo(v any) error {
	switch x := v.(type) {
	case bson.D:
		for _, e := range x {
			switch {
			case slices.Contains(mongoWriteStages, e.Key):
				return fmt.Errorf("%s writes to a collection; the read tools never write (use mongo_insert, mongo_update or mongo_delete)", e.Key)
			case slices.Contains(mongoAdminStages, e.Key):
				return fmt.Errorf("%s isn't allowed over MCP", e.Key)
			case slices.Contains(mongoSourceStages, e.Key):
				if err := checkSource(e.Key, e.Value); err != nil {
					return err
				}
			}
			if err := checkMongo(e.Value); err != nil {
				return err
			}
		}
	case bson.A:
		for _, e := range x {
			if err := checkMongo(e); err != nil {
				return err
			}
		}
	}
	return nil
}

// checkSource keeps $lookup, $graphLookup and $unionWith in the current database.
func checkSource(stage string, spec any) error {
	d, ok := spec.(bson.D)
	if !ok {
		return nil // $unionWith: "coll"
	}
	for _, e := range d {
		switch e.Key {
		case "db":
			return fmt.Errorf("%s can only read collections of the same database", stage)
		case "from", "coll":
			if _, ok := e.Value.(string); !ok {
				return fmt.Errorf("%s can only read collections of the same database (%s must be a collection name)", stage, e.Key)
			}
		}
	}
	return nil
}

// checkUpdate accepts an operator document ($set, $inc…) only.
func checkUpdate(u bson.D) error {
	if len(u) == 0 {
		return errors.New("update needs at least one operator, e.g. {\"$set\": {\"status\": \"shipped\"}}")
	}
	for _, e := range u {
		if !slices.Contains(mongoUpdateOps, e.Key) {
			if !strings.HasPrefix(e.Key, "$") {
				return fmt.Errorf("update takes operators such as $set, not a replacement document (got field %q)", e.Key)
			}
			return fmt.Errorf("update operator %s isn't supported (use %s)", e.Key, strings.Join(mongoUpdateOps, ", "))
		}
		fields, ok := e.Value.(bson.D)
		if !ok {
			return fmt.Errorf("%s takes a document of fields, e.g. {%q: {\"status\": \"shipped\"}}", e.Key, e.Key)
		}
		for _, f := range fields {
			if err := checkUpdateValue(e.Key, f.Key, f.Value); err != nil {
				return err
			}
		}
	}
	return checkMongo(u)
}

// updateModifiers are the $-keys an operator takes in a field's value.
var updateModifiers = map[string][]string{
	"$push":        {"$each", "$slice", "$sort", "$position"},
	"$addToSet":    {"$each"},
	"$currentDate": {"$type"},
}

// checkUpdateValue refuses $-keys in a value, which MongoDB would store as
// data: {"$set": {"n": {"$add": [1, 2]}}} saves the object, not 3, because
// operator updates don't evaluate expressions. $pull takes query conditions.
func checkUpdateValue(op, field string, v any) error {
	if op == "$pull" {
		return nil
	}
	d, ok := v.(bson.D)
	if !ok {
		return checkDataValue(v, op+" "+field)
	}
	for _, e := range d {
		if strings.HasPrefix(e.Key, "$") {
			if slices.Contains(updateModifiers[op], e.Key) {
				if err := checkDataValue(e.Value, op+" "+field); err != nil {
					return err
				}
				continue
			}
			return exprError(e.Key, op+" "+field)
		}
		if err := checkDataValue(e.Value, op+" "+field); err != nil {
			return err
		}
	}
	return nil
}

// checkDataValue refuses $-prefixed field names anywhere in a value meant to be stored.
func checkDataValue(v any, where string) error {
	switch x := v.(type) {
	case bson.D:
		for _, e := range x {
			if strings.HasPrefix(e.Key, "$") {
				return exprError(e.Key, where)
			}
			if err := checkDataValue(e.Value, where); err != nil {
				return err
			}
		}
	case bson.A:
		for _, e := range x {
			if err := checkDataValue(e, where); err != nil {
				return err
			}
		}
	}
	return nil
}

func exprError(key, where string) error {
	msg := fmt.Sprintf("%s in %s would be stored as a literal field named %q, not evaluated: ", key, where, key)
	if strings.HasPrefix(where, "document ") {
		return errors.New(msg + "inserts don't compute expressions. Store the computed value instead")
	}
	return errors.New(msg + "updates don't compute expressions. Read the current value, compute the result, and write it " +
		"(or use $inc, $mul, $min, $max for arithmetic)")
}

// --- targets --------------------------------------------------------------------

type mongoTarget struct {
	conn     model.Connection
	database string
	cl       *mdb.Client
}

func (t mongoTarget) coll(name string) *mongo.Collection {
	return t.cl.Mongo().Database(t.database).Collection(name)
}

// exists checks a collection or view is there, so a misspelt name isn't taken
// for an empty result.
func (t mongoTarget) exists(ctx context.Context, name string) error {
	typ, err := t.collType(ctx, name)
	if err == nil && typ == "" {
		err = fmt.Errorf("no collection %q in database %s (see mongo_collections)", name, t.database)
	}
	return err
}

// collType is a collection's type (collection, view, timeseries), or "" when it doesn't exist.
func (t mongoTarget) collType(ctx context.Context, name string) (string, error) {
	cur, err := t.cl.Mongo().Database(t.database).ListCollections(ctx, bson.D{{Key: "name", Value: name}}, options.ListCollections().SetNameOnly(true))
	if err != nil {
		return "", err
	}
	var specs []struct {
		Type string `bson:"type"`
	}
	if err := cur.All(ctx, &specs); err != nil || len(specs) == 0 {
		return "", err
	}
	return specs[0].Type, nil
}

// writable refuses writes to a view before the user is asked: they'd fail on the server.
func (t mongoTarget) writable(ctx context.Context, name string) error {
	typ, err := t.collType(ctx, name)
	if err != nil {
		return err
	}
	if typ == "view" {
		return fmt.Errorf("%s is a view; change the collection it's built on (see mongo_describe)", name)
	}
	return nil
}

// mongoColl is mongoDB for a tool on one collection.
func (c *call) mongoColl(ctx context.Context, in MongoCollArgs, need store.MCPLevel) (mongoTarget, error) {
	if in.Collection == "" {
		return mongoTarget{}, errors.New("collection is required (see mongo_collections)")
	}
	return c.mongoDB(ctx, in.Connection, in.Database, need)
}

// mongoDB finds a MongoDB connection and database the endpoint may use at
// need, opening the connection if necessary.
func (c *call) mongoDB(ctx context.Context, connection, database string, need store.MCPLevel) (mongoTarget, error) {
	conn, a, err := c.connectionOf(connection, mongoEngines)
	if err != nil {
		return mongoTarget{}, err
	}
	c.act.Connection, c.act.Database = conn.Name, database
	if database == "" {
		return mongoTarget{}, errors.New("database is required (see mongo_databases)")
	}
	have := a.db(database)
	if have == store.MCPOff {
		return mongoTarget{}, fmt.Errorf("database %q of %s isn't shared with this endpoint (see mongo_databases)", database, conn.Name)
	}
	if !atLeast(have, need) {
		return mongoTarget{}, fmt.Errorf("this endpoint can only read %s/%s; ask the user to allow writes in Chloe's MCP Server settings", conn.Name, database)
	}
	cl, err := c.mongoClient(ctx, conn)
	if err != nil {
		return mongoTarget{}, err
	}
	return mongoTarget{conn: conn, database: database, cl: cl}, nil
}

func (c *call) mongoClient(ctx context.Context, conn model.Connection) (*mdb.Client, error) {
	if err := c.s.m.Ensure(ctx, conn.ID); err != nil {
		return nil, fmt.Errorf("couldn't open %s: %w", conn.Name, err)
	}
	return c.s.m.Mongo(conn.ID)
}

// --- results ---------------------------------------------------------------------

// extJSON renders a value as relaxed Extended JSON, for results, approvals and
// the log; longs beyond 2^53 stay $numberLong.
func extJSON(v any) string {
	v = cutLongs(v)
	if d, ok := v.(bson.A); ok {
		parts := make([]string, len(d))
		for i, e := range d {
			parts[i] = extJSON(e)
		}
		return "[" + strings.Join(parts, ", ") + "]"
	}
	b, err := bson.MarshalExtJSON(v, false, false)
	if err != nil {
		// Scalars (an _id that isn't a document) need a wrapper.
		if w, err := bson.MarshalExtJSON(bson.D{{Key: "v", Value: v}}, false, false); err == nil {
			return strings.TrimSuffix(strings.TrimPrefix(string(w), `{"v":`), "}")
		}
		return fmt.Sprint(v)
	}
	return string(b)
}

// cutLongs keeps big longs exact for display, where values aren't shortened.
func cutLongs(v any) any {
	switch x := v.(type) {
	case bson.D:
		out := make(bson.D, len(x))
		for i, e := range x {
			out[i] = bson.E{Key: e.Key, Value: cutLongs(e.Value)}
		}
		return out
	case bson.A:
		out := make(bson.A, len(x))
		for i, e := range x {
			out[i] = cutLongs(e)
		}
		return out
	case int64:
		return bigLong(x)
	}
	return v
}

// cutDoc shortens long strings and binary values so one document can't fill a response.
func cutDoc(v any, cut *bool) any {
	switch x := v.(type) {
	case bson.D:
		out := make(bson.D, len(x))
		for i, e := range x {
			out[i] = bson.E{Key: e.Key, Value: cutDoc(e.Value, cut)}
		}
		return out
	case bson.A:
		out := make(bson.A, len(x))
		for i, e := range x {
			out[i] = cutDoc(e, cut)
		}
		return out
	case string:
		if len(x) > maxCellBytes {
			*cut = true
			return x[:maxCellBytes] + fmt.Sprintf("…[%d more bytes]", len(x)-maxCellBytes)
		}
	case bson.Binary:
		if len(x.Data) > maxCellBytes {
			*cut = true
			return fmt.Sprintf("[binary, %d bytes]", len(x.Data))
		}
	case int64:
		return bigLong(x)
	}
	return v
}

// bigLong keeps a long that JSON numbers can't hold exactly (beyond 2^53) as
// {"$numberLong": "…"}, which agents and the approval dialog read correctly.
func bigLong(n int64) any {
	if n > 1<<53 || n < -(1<<53) {
		return bson.D{{Key: "$numberLong", Value: strconv.FormatInt(n, 10)}}
	}
	return n
}

type DocsOut struct {
	Documents []json.RawMessage `json:"documents" jsonschema:"documents as relaxed Extended JSON"`
	Count     int               `json:"count"`
	Total     *int64            `json:"total,omitempty" jsonschema:"documents matching the filter, when count was asked for"`
	HasMore   bool              `json:"has_more" jsonschema:"more documents follow; page with skip (mongo_find) or narrow the query"`
	Truncated bool              `json:"truncated" jsonschema:"long values were cut (strings end in …[N more bytes], binary shows its size); never write a cut value back"`
}

// readDocs reads up to limit documents from a cursor, cutting long values and
// stopping at the response size cap.
func readDocs(ctx context.Context, cur *mongo.Cursor, limit int) (DocsOut, error) {
	defer cur.Close(context.Background())
	out := DocsOut{Documents: []json.RawMessage{}}
	size := 0
	for cur.Next(ctx) {
		if len(out.Documents) >= limit || size > maxResultBytes {
			out.HasMore = true
			break
		}
		var d bson.D
		if err := cur.Decode(&d); err != nil {
			return out, err
		}
		cut := false
		b, err := bson.MarshalExtJSON(cutDoc(d, &cut), false, false)
		if err != nil {
			return out, err
		}
		out.Truncated = out.Truncated || cut
		out.Documents = append(out.Documents, b)
		size += len(b)
	}
	out.Count = len(out.Documents)
	return out, cur.Err()
}

// --- tool inputs and outputs ---------------------------------------------------------

type MongoDBArgs struct {
	Connection string `json:"connection" jsonschema:"MongoDB connection name or id from list_connections"`
	Database   string `json:"database"`
}

type MongoCollArgs struct {
	MongoDBArgs
	Collection string `json:"collection"`
}

type MongoDatabaseOut struct {
	Name   string `json:"name"`
	Size   int64  `json:"size_bytes"`
	Access string `json:"access" jsonschema:"read or write"`
}

type MongoDatabasesOut struct {
	Databases []MongoDatabaseOut `json:"databases"`
}

type MongoCollectionOut struct {
	Name   string `json:"name"`
	Type   string `json:"type" jsonschema:"collection, view or timeseries"`
	Count  *int64 `json:"count,omitempty" jsonschema:"documents; absent for views and time-series collections (count them with mongo_find count: true)"`
	Size   int64  `json:"size_bytes,omitempty"`
	ViewOn string `json:"view_on,omitempty"`
}

type MongoCollectionsOut struct {
	Collections []MongoCollectionOut `json:"collections"`
}

type MongoFieldOut struct {
	Path   string         `json:"path"`
	Type   string         `json:"type" jsonschema:"BSON type seen most: string, int, long, double, decimal, bool, date, oid, object, array…; mixed when no type is in the majority (see types)"`
	SeenIn int            `json:"seen_in" jsonschema:"sampled documents that have the field"`
	Types  map[string]int `json:"types,omitempty" jsonschema:"when the field has more than one type: how often each was seen"`
}

type MongoIndexOut struct {
	Name string          `json:"name"`
	Spec json.RawMessage `json:"spec" jsonschema:"the index as listIndexes reports it: key, unique, partialFilterExpression, weights, expireAfterSeconds…"`
}

type MongoDescribeOut struct {
	Type      string          `json:"type"`
	Count     int64           `json:"count"`
	Sampled   int             `json:"sampled" jsonschema:"documents sampled for fields (up to 100)"`
	Fields    []MongoFieldOut `json:"fields" jsonschema:"field paths seen in the sample, nested documents included"`
	Options   json.RawMessage `json:"options,omitempty" jsonschema:"collection options: capped, size, max, timeseries, validationLevel, validationAction, collation…"`
	Indexes   []MongoIndexOut `json:"indexes"`
	Validator json.RawMessage `json:"validator,omitempty"`
	ViewOn    string          `json:"view_on,omitempty"`
	Pipeline  json.RawMessage `json:"pipeline,omitempty"`
}

type MongoSort struct {
	Field string `json:"field"`
	Desc  bool   `json:"desc,omitempty"`
}

type MongoFindArgs struct {
	MongoCollArgs
	Filter     Doc         `json:"filter,omitzero" jsonschema:"query filter, e.g. {\"status\": \"open\", \"total\": {\"$gt\": 100}}; Extended JSON for ObjectId and dates: {\"$oid\": \"…\"}, {\"$date\": \"2026-01-01T00:00:00Z\"}"`
	Projection Doc         `json:"projection,omitzero" jsonschema:"fields to include or exclude, e.g. {\"name\": 1, \"_id\": 0}"`
	Sort       []MongoSort `json:"sort,omitempty"`
	Skip       int64       `json:"skip,omitempty"`
	Limit      int         `json:"limit,omitempty" jsonschema:"documents to return (default 100, at most 1000)"`
	Count      bool        `json:"count,omitempty" jsonschema:"also count every document matching the filter"`
}

type MongoAggregateArgs struct {
	MongoCollArgs
	Pipeline []Doc `json:"pipeline" jsonschema:"aggregation stages in order, e.g. [{\"$match\": {…}}, {\"$group\": {…}}, {\"$sort\": {\"total\": -1}}]"`
	Limit    int   `json:"limit,omitempty" jsonschema:"documents to return (default 100, at most 1000)"`
}

type MongoInsertArgs struct {
	MongoCollArgs
	Documents []Doc `json:"documents" jsonschema:"documents to insert (at most 1000); _id is generated when left out"`
}

type MongoInsertFailure struct {
	Index int             `json:"index" jsonschema:"position in documents, from 0"`
	ID    json.RawMessage `json:"_id"`
	Error string          `json:"error"`
}

type MongoInsertOut struct {
	Approval string               `json:"approval,omitempty" jsonschema:"how the change was cleared: approved by the user in Chloe, or ran without asking because danger mode is on"`
	Inserted int                  `json:"inserted"`
	IDs      []json.RawMessage    `json:"ids" jsonschema:"_id of each inserted document"`
	Failed   []MongoInsertFailure `json:"failed,omitempty" jsonschema:"documents that weren't inserted and why; the others were"`
}

type MongoUpdateArgs struct {
	MongoCollArgs
	Filter Doc  `json:"filter" jsonschema:"documents to change; required and not empty (to match every document use {\"_id\": {\"$exists\": true}})"`
	Update Doc  `json:"update" jsonschema:"update operators, e.g. {\"$set\": {\"status\": \"shipped\"}, \"$inc\": {\"version\": 1}}"`
	Many   bool `json:"many,omitempty" jsonschema:"change every matching document (at most 1000) instead of the first"`
	Upsert bool `json:"upsert,omitempty" jsonschema:"insert a document built from the filter and update when nothing matches"`
}

type MongoUpdateOut struct {
	Approval string          `json:"approval,omitempty" jsonschema:"how the change was cleared: approved by the user in Chloe, or ran without asking because danger mode is on"`
	Matched  int64           `json:"matched"`
	Modified int64           `json:"modified"`
	Upserted json.RawMessage `json:"upserted_id,omitempty"`
}

type MongoDeleteArgs struct {
	MongoCollArgs
	Filter Doc  `json:"filter" jsonschema:"documents to delete; required and not empty"`
	Many   bool `json:"many,omitempty" jsonschema:"delete every matching document (at most 1000) instead of the first"`
}

type MongoDeletedOut struct {
	Approval string `json:"approval,omitempty" jsonschema:"how the change was cleared: approved by the user in Chloe, or ran without asking because danger mode is on"`
	Deleted  int64  `json:"deleted"`
}

// --- tools --------------------------------------------------------------------------

func (s *Server) addMongoTools(srv *mcp.Server, ep string, writes bool) {
	add(s, srv, ep, readOnlyTool("mongo_databases", "List MongoDB databases",
		"Lists the databases of a MongoDB connection that this endpoint can see."),
		func(ctx context.Context, c *call, in struct {
			Connection string `json:"connection" jsonschema:"MongoDB connection name or id from list_connections"`
		}) (MongoDatabasesOut, error) {
			conn, a, err := c.connectionOf(in.Connection, mongoEngines)
			if err != nil {
				return MongoDatabasesOut{}, err
			}
			c.act.Connection = conn.Name
			if err := c.s.m.Ensure(ctx, conn.ID); err != nil {
				return MongoDatabasesOut{}, fmt.Errorf("couldn't open %s: %w", conn.Name, err)
			}
			ctx, cancel := context.WithTimeout(ctx, statementTimeout)
			defer cancel()
			dbs, err := c.s.m.MongoDatabases(ctx, conn.ID)
			if err != nil {
				return MongoDatabasesOut{}, err
			}
			out := MongoDatabasesOut{Databases: []MongoDatabaseOut{}}
			for _, d := range dbs {
				l := a.db(d.Name)
				if l == store.MCPOff {
					continue
				}
				out.Databases = append(out.Databases, MongoDatabaseOut{Name: d.Name, Size: d.Size, Access: levelName(l)})
			}
			return out, nil
		})

	add(s, srv, ep, readOnlyTool("mongo_collections", "List MongoDB collections",
		"Lists the collections and views of a database with their document counts and sizes."),
		func(ctx context.Context, c *call, in MongoDBArgs) (MongoCollectionsOut, error) {
			t, err := c.mongoDB(ctx, in.Connection, in.Database, store.MCPRead)
			if err != nil {
				return MongoCollectionsOut{}, err
			}
			ctx, cancel := context.WithTimeout(ctx, statementTimeout)
			defer cancel()
			cs, err := t.cl.Collections(ctx, t.database, true)
			if err != nil {
				return MongoCollectionsOut{}, err
			}
			out := MongoCollectionsOut{Collections: []MongoCollectionOut{}}
			for _, ci := range cs {
				o := MongoCollectionOut{Name: ci.Name, Type: ci.Type, Size: ci.Size, ViewOn: ci.ViewOn}
				if ci.Type == "collection" {
					o.Count = &ci.Count
				}
				out.Collections = append(out.Collections, o)
			}
			return out, nil
		})

	addOrdered(s, srv, ep, readOnlyTool("mongo_describe", "Describe MongoDB collection",
		"Shows a collection's fields (from a sample of documents) with their types, its indexes and validator, or a view's pipeline."),
		func(ctx context.Context, c *call, in MongoCollArgs) (MongoDescribeOut, error) {
			t, err := c.mongoColl(ctx, in, store.MCPRead)
			if err != nil {
				return MongoDescribeOut{}, err
			}
			ctx, cancel := context.WithTimeout(ctx, statementTimeout)
			defer cancel()
			return describeCollection(ctx, t, in.Collection)
		})

	addOrdered(s, srv, ep, readOnlyTool("mongo_find", "Find MongoDB documents",
		"Reads documents of a collection or view matching a filter, with optional projection, sort and paging. "+
			"Documents come back as relaxed Extended JSON."),
		func(ctx context.Context, c *call, in MongoFindArgs) (DocsOut, error) {
			t, err := c.mongoColl(ctx, in.MongoCollArgs, store.MCPRead)
			if err != nil {
				return DocsOut{}, err
			}
			filter, err := guarded("filter", in.Filter)
			if err != nil {
				return DocsOut{}, err
			}
			proj, err := guarded("projection", in.Projection)
			if err != nil {
				return DocsOut{}, err
			}
			if in.Limit < 0 || in.Skip < 0 {
				return DocsOut{}, errors.New("limit and skip can't be negative (limit 0 means the default of 100)")
			}
			if err := t.exists(ctx, in.Collection); err != nil {
				return DocsOut{}, err
			}
			sort := bson.D{}
			for _, s := range in.Sort {
				dir := 1
				if s.Desc {
					dir = -1
				}
				sort = append(sort, bson.E{Key: s.Field, Value: dir})
			}
			limit := clampLimit(in.Limit)
			c.act.SQL = fmt.Sprintf("db.%s.find(%s, %s).sort(%s).skip(%d).limit(%d)", in.Collection, extJSON(filter), extJSON(proj), extJSON(sort), in.Skip, limit)
			ctx, cancel := context.WithTimeout(ctx, statementTimeout)
			defer cancel()
			o := options.Find().SetSkip(in.Skip).SetLimit(int64(limit) + 1)
			if len(proj) > 0 {
				o.SetProjection(proj)
			}
			if len(sort) > 0 {
				o.SetSort(sort)
			}
			cur, err := t.coll(in.Collection).Find(ctx, filter, o)
			if err != nil {
				return DocsOut{}, err
			}
			out, err := readDocs(ctx, cur, limit)
			if err != nil {
				return out, err
			}
			if in.Count {
				n, err := t.coll(in.Collection).CountDocuments(ctx, filter)
				if err != nil {
					return out, err
				}
				out.Total = &n
			}
			c.act.Rows = int64(out.Count)
			return out, nil
		})

	addOrdered(s, srv, ep, readOnlyTool("mongo_aggregate", "Run MongoDB aggregation",
		"Runs a read-only aggregation pipeline on a collection or view and returns its documents. "+
			"$out and $merge, server-wide stages ($currentOp, $listCatalog…) and lookups into other databases are refused."),
		func(ctx context.Context, c *call, in MongoAggregateArgs) (DocsOut, error) {
			t, err := c.mongoColl(ctx, in.MongoCollArgs, store.MCPRead)
			if err != nil {
				return DocsOut{}, err
			}
			if in.Limit < 0 {
				return DocsOut{}, errors.New("limit can't be negative (0 means the default of 100)")
			}
			if err := t.exists(ctx, in.Collection); err != nil {
				return DocsOut{}, err
			}
			pipeline := bson.A{}
			for i, st := range in.Pipeline {
				d, err := st.bson()
				if err != nil {
					return DocsOut{}, fmt.Errorf("stage %d: %w", i+1, err)
				}
				if len(d) != 1 {
					return DocsOut{}, fmt.Errorf("stage %d must have exactly one stage name, such as $match", i+1)
				}
				pipeline = append(pipeline, d)
			}
			if err := checkMongo(pipeline); err != nil {
				return DocsOut{}, err
			}
			limit := clampLimit(in.Limit)
			c.act.SQL = fmt.Sprintf("db.%s.aggregate(%s)", in.Collection, extJSON(pipeline))
			// A final $limit stops the server producing more than is returned.
			pipeline = append(pipeline, bson.D{{Key: "$limit", Value: limit + 1}})
			ctx, cancel := context.WithTimeout(ctx, statementTimeout)
			defer cancel()
			cur, err := t.coll(in.Collection).Aggregate(ctx, pipeline)
			if err != nil {
				return DocsOut{}, err
			}
			out, err := readDocs(ctx, cur, limit)
			c.act.Rows = int64(out.Count)
			return out, err
		})

	if !writes {
		return
	}

	addOrdered(s, srv, ep, writeTool("mongo_insert", "Insert MongoDB documents",
		"Inserts documents into a collection (created if missing). The user approves it in Chloe unless they've turned on danger mode.", false),
		func(ctx context.Context, c *call, in MongoInsertArgs) (MongoInsertOut, error) {
			t, err := c.mongoColl(ctx, in.MongoCollArgs, store.MCPWrite)
			if err != nil {
				return MongoInsertOut{}, err
			}
			switch {
			case len(in.Documents) == 0:
				return MongoInsertOut{}, errors.New("documents is empty")
			case len(in.Documents) > maxMongoWrite:
				return MongoInsertOut{}, fmt.Errorf("insert at most %d documents per call", maxMongoWrite)
			}
			if err := t.writable(ctx, in.Collection); err != nil {
				return MongoInsertOut{}, err
			}
			docs := make([]any, len(in.Documents))
			ids := make([]any, len(in.Documents))
			shown := []string{}
			for i, d := range in.Documents {
				doc, err := d.bson()
				if err != nil {
					return MongoInsertOut{}, fmt.Errorf("document %d: %w", i+1, err)
				}
				if err := checkDataValue(doc, fmt.Sprintf("document %d", i+1)); err != nil {
					return MongoInsertOut{}, err
				}
				// Chloe picks missing _ids itself, so it can say which documents were saved.
				doc, ids[i] = withID(doc)
				docs[i] = doc
				if i < 5 {
					shown = append(shown, extJSON(doc))
				}
			}
			if len(docs) > 5 {
				shown = append(shown, fmt.Sprintf("// … and %d more documents", len(docs)-5))
			}
			n := int64(len(docs))
			c.act.SQL = fmt.Sprintf("db.%s.insertMany([\n  %s\n])", in.Collection, strings.Join(shown, ",\n  "))
			if err := c.approve(ctx, ApprovalRequest{Connection: t.conn.Name, Database: t.database, Table: in.Collection,
				Summary: fmt.Sprintf("Insert %s into %s", plural(n, "document"), in.Collection), SQL: c.act.SQL, Rows: n}); err != nil {
				return MongoInsertOut{}, err
			}
			ctx, cancel := context.WithTimeout(ctx, statementTimeout)
			defer cancel()
			// Unordered: one bad document doesn't stop the rest, and each failure is reported.
			_, err = t.coll(in.Collection).InsertMany(ctx, docs, options.InsertMany().SetOrdered(false))
			failed := map[int]string{}
			if err != nil {
				var bwe mongo.BulkWriteException
				if !errors.As(err, &bwe) || len(bwe.WriteErrors) == 0 {
					return MongoInsertOut{}, mdb.WriteError(err)
				}
				for _, we := range bwe.WriteErrors {
					failed[we.Index] = mdb.WriteError(mongo.WriteException{WriteErrors: mongo.WriteErrors{we.WriteError}}).Error()
				}
			}
			out := MongoInsertOut{IDs: []json.RawMessage{}}
			for i, id := range ids {
				if msg, ok := failed[i]; ok {
					out.Failed = append(out.Failed, MongoInsertFailure{Index: i, ID: json.RawMessage(extJSON(id)), Error: msg})
					continue
				}
				out.IDs = append(out.IDs, json.RawMessage(extJSON(id)))
			}
			out.Inserted = len(out.IDs)
			c.act.Rows = int64(out.Inserted)
			if out.Inserted == 0 {
				return out, fmt.Errorf("nothing was inserted: %s", out.Failed[0].Error)
			}
			return out, nil
		})

	addOrdered(s, srv, ep, writeTool("mongo_update", "Update MongoDB documents",
		"Applies update operators ($set, $inc, $push…) to the first matching document, or with many to every match (at most 1000). "+
			"The user approves the number of documents in Chloe unless they've turned on danger mode; only those documents are changed.", true),
		func(ctx context.Context, c *call, in MongoUpdateArgs) (MongoUpdateOut, error) {
			t, err := c.mongoColl(ctx, in.MongoCollArgs, store.MCPWrite)
			if err != nil {
				return MongoUpdateOut{}, err
			}
			filter, err := writeFilter(in.Filter)
			if err != nil {
				return MongoUpdateOut{}, err
			}
			update, err := in.Update.bson()
			if err != nil {
				return MongoUpdateOut{}, fmt.Errorf("update: %w", err)
			}
			if err := checkUpdate(update); err != nil {
				return MongoUpdateOut{}, err
			}
			ctx, cancel := context.WithTimeout(ctx, statementTimeout)
			defer cancel()
			if err := t.writable(ctx, in.Collection); err != nil {
				return MongoUpdateOut{}, err
			}
			coll := t.coll(in.Collection)
			ids, err := matchingIDs(ctx, coll, filter, in.Many)
			if err != nil {
				return MongoUpdateOut{}, err
			}
			verb := "updateOne"
			if in.Many {
				verb = "updateMany"
			}
			c.act.SQL = fmt.Sprintf("db.%s.%s(%s, %s)", in.Collection, verb, extJSON(filter), extJSON(update))
			if len(ids) == 0 && !in.Upsert {
				return MongoUpdateOut{}, nil
			}
			n := int64(len(ids))
			summary := fmt.Sprintf("Update %s in %s", plural(n, "document"), in.Collection)
			shown := c.act.SQL + idList(ids)
			if n == 0 {
				summary = fmt.Sprintf("Insert a document into %s (upsert: nothing matched)", in.Collection)
				c.act.SQL = fmt.Sprintf("db.%s.updateOne(%s, %s, {\"upsert\": true})", in.Collection, extJSON(filter), extJSON(update))
				shown = c.act.SQL
			}
			if err := c.approve(ctx, ApprovalRequest{Connection: t.conn.Name, Database: t.database, Table: in.Collection, Summary: summary, SQL: shown, Rows: max(n, 1)}); err != nil {
				return MongoUpdateOut{}, err
			}
			var r *mongo.UpdateResult
			if n == 0 {
				r, err = coll.UpdateOne(ctx, filter, update, options.UpdateOne().SetUpsert(true))
			} else {
				r, err = coll.UpdateMany(ctx, onlyIDs(filter, ids), update)
			}
			if err != nil {
				return MongoUpdateOut{}, mdb.WriteError(err)
			}
			out := MongoUpdateOut{Matched: r.MatchedCount, Modified: r.ModifiedCount}
			if r.UpsertedID != nil {
				out.Upserted = json.RawMessage(extJSON(r.UpsertedID))
			}
			c.act.Rows = max(r.ModifiedCount, r.UpsertedCount)
			return out, nil
		})

	addOrdered(s, srv, ep, writeTool("mongo_delete", "Delete MongoDB documents",
		"Deletes the first document matching the filter, or with many every match (at most 1000). "+
			"The user approves the number of documents in Chloe unless they've turned on danger mode; only those documents are deleted.", true),
		func(ctx context.Context, c *call, in MongoDeleteArgs) (MongoDeletedOut, error) {
			t, err := c.mongoColl(ctx, in.MongoCollArgs, store.MCPWrite)
			if err != nil {
				return MongoDeletedOut{}, err
			}
			filter, err := writeFilter(in.Filter)
			if err != nil {
				return MongoDeletedOut{}, err
			}
			ctx, cancel := context.WithTimeout(ctx, statementTimeout)
			defer cancel()
			if err := t.writable(ctx, in.Collection); err != nil {
				return MongoDeletedOut{}, err
			}
			coll := t.coll(in.Collection)
			ids, err := matchingIDs(ctx, coll, filter, in.Many)
			if err != nil {
				return MongoDeletedOut{}, err
			}
			verb := "deleteOne"
			if in.Many {
				verb = "deleteMany"
			}
			c.act.SQL = fmt.Sprintf("db.%s.%s(%s)", in.Collection, verb, extJSON(filter))
			if len(ids) == 0 {
				return MongoDeletedOut{}, nil
			}
			n := int64(len(ids))
			if err := c.approve(ctx, ApprovalRequest{Connection: t.conn.Name, Database: t.database, Table: in.Collection,
				Summary: fmt.Sprintf("Delete %s from %s", plural(n, "document"), in.Collection), SQL: c.act.SQL + idList(ids), Rows: n}); err != nil {
				return MongoDeletedOut{}, err
			}
			r, err := coll.DeleteMany(ctx, onlyIDs(filter, ids))
			if err != nil {
				return MongoDeletedOut{}, mdb.WriteError(err)
			}
			c.act.Rows = r.DeletedCount
			return MongoDeletedOut{Deleted: r.DeletedCount}, nil
		})
}

func guarded(what string, d Doc) (bson.D, error) {
	v, err := d.bson()
	if err != nil {
		return nil, fmt.Errorf("%s: %w", what, err)
	}
	if err := checkMongo(v); err != nil {
		return nil, err
	}
	return v, nil
}

// withID returns doc with an _id (a new ObjectId first when it has none) and that _id.
func withID(doc bson.D) (bson.D, any) {
	for _, e := range doc {
		if e.Key == "_id" {
			return doc, e.Value
		}
	}
	id := bson.NewObjectID()
	return append(bson.D{{Key: "_id", Value: id}}, doc...), id
}

// writeFilter parses the filter of an update or delete, which can't be empty.
func writeFilter(d Doc) (bson.D, error) {
	f, err := guarded("filter", d)
	if err != nil {
		return nil, err
	}
	if len(f) == 0 {
		return nil, errors.New(`filter is required and can't be empty; to match every document use {"_id": {"$exists": true}}`)
	}
	return f, nil
}

// matchingIDs collects the _id of the documents a write would touch: the first
// match, or with many up to maxMongoWrite of them.
func matchingIDs(ctx context.Context, coll *mongo.Collection, filter bson.D, many bool) (bson.A, error) {
	limit := int64(1)
	if many {
		limit = maxMongoWrite + 1
	}
	cur, err := coll.Find(ctx, filter, options.Find().SetProjection(bson.D{{Key: "_id", Value: 1}}).SetLimit(limit))
	if err != nil {
		return nil, err
	}
	var docs []bson.D
	if err := cur.All(ctx, &docs); err != nil {
		return nil, err
	}
	if int64(len(docs)) > maxMongoWrite {
		return nil, fmt.Errorf("the filter matches more than %d documents; narrow it, or change them in batches", maxMongoWrite)
	}
	ids := bson.A{}
	for _, d := range docs {
		for _, e := range d {
			if e.Key == "_id" {
				ids = append(ids, e.Value)
			}
		}
	}
	return ids, nil
}

// onlyIDs limits a filter to the approved documents, so a write never touches
// one that started matching after the user approved.
func onlyIDs(filter bson.D, ids bson.A) bson.D {
	return bson.D{{Key: "$and", Value: bson.A{filter, bson.D{{Key: "_id", Value: bson.D{{Key: "$in", Value: ids}}}}}}}
}

// idList shows the approved _ids under the statement.
func idList(ids bson.A) string {
	shown := ids
	if len(shown) > 5 {
		shown = shown[:5]
	}
	s := "\n// _id: " + extJSON(shown)
	if len(ids) > 5 {
		s += fmt.Sprintf(" and %d more", len(ids)-5)
	}
	return s
}

func describeCollection(ctx context.Context, t mongoTarget, name string) (MongoDescribeOut, error) {
	cs, err := t.cl.Collections(ctx, t.database, true)
	if err != nil {
		return MongoDescribeOut{}, err
	}
	var ci *mdb.CollInfo
	for i := range cs {
		if cs[i].Name == name {
			ci = &cs[i]
		}
	}
	if ci == nil {
		return MongoDescribeOut{}, fmt.Errorf("no collection %q in %s (see mongo_collections)", name, t.database)
	}
	out := MongoDescribeOut{Type: ci.Type, Count: ci.Count, ViewOn: ci.ViewOn, Fields: []MongoFieldOut{}, Indexes: []MongoIndexOut{}}
	if ci.Type != "collection" {
		// Collection stats don't count views or time-series buckets' documents.
		n, err := t.coll(name).CountDocuments(ctx, bson.D{})
		if err != nil {
			return out, err
		}
		out.Count = n
	}
	opts, _ := mdb.FromVal(ci.Options)
	if d, ok := opts.(bson.D); ok {
		rest := bson.D{}
		for _, e := range d {
			switch e.Key {
			case "validator":
				out.Validator = json.RawMessage(extJSON(e.Value))
			case "pipeline":
				out.Pipeline = json.RawMessage(extJSON(e.Value))
			case "viewOn":
			default:
				rest = append(rest, e)
			}
		}
		if len(rest) > 0 {
			out.Options = json.RawMessage(extJSON(rest))
		}
	}
	sampled, fields, err := sampleFields(ctx, t.coll(name), 100)
	if err != nil {
		return out, err
	}
	out.Sampled, out.Fields = sampled, fields
	if ci.Type == "view" {
		return out, nil
	}
	idx, err := t.cl.Indexes(ctx, t.database, name)
	if err != nil {
		return out, err
	}
	for _, ix := range idx {
		spec, _ := mdb.FromVal(ix.Spec)
		if d, ok := spec.(bson.D); ok {
			d = slices.DeleteFunc(slices.Clone(d), func(e bson.E) bool { return e.Key == "v" || e.Key == "name" || e.Key == "ns" })
			spec = d
		}
		out.Indexes = append(out.Indexes, MongoIndexOut{Name: ix.Name, Spec: json.RawMessage(extJSON(spec))})
	}
	return out, nil
}

// sampleFields lists the field paths of up to n random documents, with the
// type seen most and how many documents have each.
func sampleFields(ctx context.Context, coll *mongo.Collection, n int) (int, []MongoFieldOut, error) {
	cur, err := coll.Aggregate(ctx, bson.A{bson.D{{Key: "$sample", Value: bson.D{{Key: "size", Value: n}}}}})
	if err != nil {
		return 0, nil, err
	}
	var docs []bson.D
	if err := cur.All(ctx, &docs); err != nil {
		return 0, nil, err
	}
	type field struct {
		seen  int
		types map[string]int
	}
	byPath := map[string]*field{}
	var order []string
	var walk func(prefix string, d bson.D, depth int, here map[string]bool)
	walk = func(prefix string, d bson.D, depth int, here map[string]bool) {
		for _, e := range d {
			p := prefix + e.Key
			f := byPath[p]
			if f == nil {
				f = &field{types: map[string]int{}}
				byPath[p] = f
				order = append(order, p)
			}
			if !here[p] {
				here[p] = true
				f.seen++
			}
			f.types[mdb.ToVal(e.Value).T]++
			switch sub := e.Value.(type) {
			case bson.D:
				if depth < 4 {
					walk(p+".", sub, depth+1, here)
				}
			case bson.A:
				// Fields of documents in arrays, e.g. items.sku.
				for _, el := range sub {
					if d, ok := el.(bson.D); ok && depth < 4 {
						walk(p+".", d, depth+1, here)
					}
				}
			}
		}
	}
	for _, d := range docs {
		walk("", d, 0, map[string]bool{})
	}
	out := make([]MongoFieldOut, 0, len(order))
	for _, p := range order {
		f := byPath[p]
		best, most, total := "", 0, 0
		for t, c := range f.types {
			total += c
			if c > most || c == most && t < best {
				best, most = t, c
			}
		}
		if most*2 <= total {
			best = "mixed" // no type is in the majority; types has the counts
		}
		o := MongoFieldOut{Path: p, Type: best, SeenIn: f.seen}
		if len(f.types) > 1 {
			o.Types = f.types
		}
		out = append(out, o)
	}
	return len(docs), out, nil
}
