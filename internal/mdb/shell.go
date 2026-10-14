package mdb

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/dop251/goja"
	"github.com/dop251/goja/ast"
	"github.com/dop251/goja/parser"
	"github.com/google/uuid"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

var preludeProgram = goja.MustCompile("prelude.js", prelude, false)

func newUUID() []byte {
	u := uuid.New()
	return u[:]
}

// Shell runs mongo shell scripts: JavaScript with a db object, collection
// methods, cursors and the BSON types. One goroutine uses a Shell at a time.
type Shell struct {
	c  *Client // nil for literal-only evaluation
	vm *goja.Runtime
	// DB is the current database (use <db> changes it).
	DB  string
	ctx context.Context
	out strings.Builder

	mu      sync.Mutex
	cursors map[string]*cursor
	results []Result
	stmts   []string
	last    time.Time
}

func newRuntime() *goja.Runtime {
	vm := goja.New()
	vm.SetMaxCallStackSize(2000)
	return vm
}

// NewShell starts a shell on database db.
func (c *Client) NewShell(db string) *Shell {
	if db == "" {
		db = "test"
	}
	s := &Shell{c: c, vm: newRuntime(), DB: db, ctx: context.Background(), cursors: map[string]*cursor{}}
	s.installHelpers()
	if _, err := s.vm.RunProgram(preludeProgram); err != nil {
		panic(err)
	}
	s.installShell()
	return s
}

// ---- literal evaluation --------------------------------------------------------------

var evalPool = sync.Pool{New: func() any {
	s := &Shell{vm: newRuntime(), ctx: context.Background()}
	s.installHelpers()
	if _, err := s.vm.RunProgram(preludeProgram); err != nil {
		panic(err)
	}
	return s
}}

// evalTimeout bounds literal evaluation (a filter can't loop forever).
var evalTimeout = 5 * time.Second

// EvalShell evaluates one shell expression, such as a document literal with
// ObjectId(...) or ISODate(...) values, and returns it as BSON.
func EvalShell(text string) (v any, err error) {
	s := evalPool.Get().(*Shell)
	defer evalPool.Put(s)
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("%v", r)
		}
	}()
	t := time.AfterFunc(evalTimeout, func() { s.vm.Interrupt("timed out") })
	defer func() { t.Stop(); s.vm.ClearInterrupt() }()
	jv, err := s.vm.RunString("(" + text + "\n)")
	if err != nil {
		return nil, jsError(err)
	}
	return s.toBSON(jv)
}

// EvalDoc evaluates a document literal in shell syntax.
func EvalDoc(text string) (bson.D, error) {
	v, err := EvalShell(text)
	if err != nil {
		return nil, err
	}
	switch x := v.(type) {
	case bson.D:
		return x, nil
	case nil:
		return bson.D{}, nil
	}
	return nil, fmt.Errorf("expected a document such as { field: value }")
}

func jsError(err error) error {
	var ex *goja.Exception
	if errors.As(err, &ex) {
		msg := ex.Value().String()
		if o, ok := ex.Value().(*goja.Object); ok {
			if m := o.Get("message"); m != nil && !goja.IsUndefined(m) {
				msg = m.String()
				if n := o.Get("name"); n != nil && !goja.IsUndefined(n) && n.String() != "Error" && n.String() != "GoError" {
					msg = n.String() + ": " + msg
				}
			}
		}
		if f := ex.Stack(); len(f) > 0 {
			if p := f[0].Position(); p.Line > 0 {
				return fmt.Errorf("%s (line %d)", msg, p.Line)
			}
		}
		return errors.New(msg)
	}
	var ie *goja.InterruptedError
	if errors.As(err, &ie) {
		return fmt.Errorf("%v", ie.Value())
	}
	return err
}

// ---- running scripts ---------------------------------------------------------------

// Result is the value of one top-level expression statement.
type Result struct {
	// Statement is the source text of the statement.
	Statement string `json:"statement"`
	// Kind: documents (a cursor or documents), value (anything else) or text
	// (output of show and use commands).
	Kind  string `json:"kind"`
	Page  *Page  `json:"page,omitempty"`
	Value *Val   `json:"value,omitempty"`
	Text  string `json:"text,omitempty"`
	// CursorID fetches further pages of a cursor result; HasMore says there are some.
	CursorID string `json:"cursorId,omitempty"`
	HasMore  bool   `json:"hasMore"`
	// Namespace is db.collection for cursors over a collection (lets the grid edit documents).
	DB         string  `json:"db,omitempty"`
	Collection string  `json:"collection,omitempty"`
	ElapsedMS  float64 `json:"elapsedMs"`
}

// RunOutput is everything one Run produced.
type RunOutput struct {
	Results []Result `json:"results"`
	// Output collects print() and printjson() output.
	Output string `json:"output"`
	Error  string `json:"error,omitempty"`
	// DB is the current database after the script.
	DB        string  `json:"db"`
	ElapsedMS float64 `json:"elapsedMs"`
}

var cmdLine = regexp.MustCompile(`(?m)^([ \t]*)(use|show)[ \t]+([^\s;]+)[ \t]*;?[ \t]*$`)

// rewrite turns shell commands into calls and wraps every top-level expression
// statement in __result(expr, n) so its value is reported.
func rewrite(src string) (string, []string, error) {
	src = cmdLine.ReplaceAllStringFunc(src, func(m string) string {
		g := cmdLine.FindStringSubmatch(m)
		return g[1] + "__" + g[2] + "(" + strconv.Quote(g[3]) + ");"
	})
	prog, err := parser.ParseFile(nil, "", src, 0)
	if err != nil {
		return "", nil, err
	}
	type span struct{ a, b int }
	var spans []span
	var stmts []string
	for _, st := range prog.Body {
		es, ok := st.(*ast.ExpressionStatement)
		if !ok {
			continue
		}
		a, b := int(es.Expression.Idx0())-1, int(es.Expression.Idx1())-1
		if a < 0 || b > len(src) || a >= b {
			continue
		}
		spans = append(spans, span{a, b})
		stmts = append(stmts, statementText(src[a:b]))
	}
	var out strings.Builder
	pos := 0
	for i, sp := range spans {
		out.WriteString(src[pos:sp.a])
		fmt.Fprintf(&out, "__result((%s), %d)", src[sp.a:sp.b], i)
		pos = sp.b
	}
	out.WriteString(src[pos:])
	return out.String(), stmts, nil
}

var shellCall = regexp.MustCompile(`^__(use|show)\("(.*)"\)$`)

func statementText(s string) string {
	s = strings.TrimSpace(s)
	if m := shellCall.FindStringSubmatch(s); m != nil {
		return m[1] + " " + m[2]
	}
	return s
}

// Run executes a script. Cursor results return their first pageSize documents.
func (s *Shell) Run(ctx context.Context, script string, pageSize int) RunOutput {
	s.mu.Lock()
	defer s.mu.Unlock()
	start := time.Now()
	s.ctx, s.results, s.last = ctx, nil, start
	s.out.Reset()
	s.cursors = map[string]*cursor{}
	if pageSize <= 0 {
		pageSize = 1000
	}
	_ = s.vm.Set("__pageSize", pageSize)
	stop := context.AfterFunc(ctx, func() { s.vm.Interrupt("cancelled") })
	defer func() {
		stop()
		s.vm.ClearInterrupt()
		s.ctx = context.Background()
	}()
	src, stmts, err := rewrite(script)
	out := RunOutput{}
	if err == nil {
		s.stmts = stmts
		_, err = s.vm.RunString(src)
	}
	if err != nil {
		out.Error = jsError(err).Error()
	}
	out.Results, out.Output, out.DB = s.results, s.out.String(), s.DB
	out.ElapsedMS = ms(time.Since(start))
	if out.Results == nil {
		out.Results = []Result{}
	}
	return out
}

func ms(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 }

// Fetch returns documents of a cursor result from offset skip.
func (s *Shell) Fetch(ctx context.Context, cursorID string, skip, limit int) (Page, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.cursors[cursorID]
	if !ok {
		return Page{}, false, fmt.Errorf("the result is no longer available; run the query again")
	}
	s.ctx = ctx
	defer func() { s.ctx = context.Background() }()
	var docs []bson.D
	var more bool
	err := s.catch(func() { docs, more = c.page(skip, limit) })
	if err != nil {
		return Page{}, false, err
	}
	return PageOf(docs), more, nil
}

// catch runs f, turning a thrown shell error into an error.
func (s *Shell) catch(f func()) (err error) {
	defer func() {
		if r := recover(); r != nil {
			if v, ok := r.(goja.Value); ok {
				if o, ok := v.(*goja.Object); ok {
					if m := o.Get("message"); m != nil {
						err = errors.New(m.String())
						return
					}
				}
				err = errors.New(v.String())
				return
			}
			if e, ok := r.(error); ok {
				err = e
				return
			}
			panic(r)
		}
	}()
	f()
	return nil
}

func (s *Shell) addResult(v goja.Value, n int) {
	now := time.Now()
	r := Result{ElapsedMS: ms(now.Sub(s.last))}
	s.last = now
	if n >= 0 && n < len(s.stmts) {
		r.Statement = s.stmts[n]
	}
	if v == nil || goja.IsUndefined(v) {
		return
	}
	pageSize := int(s.vm.Get("__pageSize").ToInteger())
	if o, ok := v.(*goja.Object); ok {
		switch x := o.Export().(type) {
		case *cursor:
			id := uuid.NewString()
			s.cursors[id] = x
			docs, more := x.page(0, pageSize)
			p := PageOf(docs)
			r.Kind, r.Page, r.CursorID, r.HasMore = "documents", &p, id, more
			r.DB, r.Collection = x.db, x.coll
			r.ElapsedMS = ms(time.Since(s.last) + now.Sub(now))
			s.last = time.Now()
			s.results = append(s.results, r)
			return
		case *dbObj:
			r.Kind, r.Text = "text", x.name
			s.results = append(s.results, r)
			return
		case *collObj:
			r.Kind, r.Text = "text", x.db+"."+x.name
			s.results = append(s.results, r)
			return
		}
	}
	if str, ok := v.Export().(string); ok && strings.HasPrefix(r.Statement, "use ") || ok && strings.HasPrefix(r.Statement, "show ") {
		r.Kind, r.Text = "text", str
		s.results = append(s.results, r)
		return
	}
	b, err := s.toBSON(v)
	if err != nil {
		panic(s.vm.NewGoError(err))
	}
	val := ToVal(b)
	switch {
	case val.T == "object":
		p := PageOf([]bson.D{b.(bson.D)})
		r.Kind, r.Page = "documents", &p
	case val.T == "array" && allDocs(b.(bson.A)):
		docs := make([]bson.D, len(b.(bson.A)))
		for i, e := range b.(bson.A) {
			docs[i] = e.(bson.D)
		}
		p := PageOf(docs)
		r.Kind, r.Page = "documents", &p
	default:
		r.Kind, r.Value = "value", &val
	}
	s.results = append(s.results, r)
}

func allDocs(a bson.A) bool {
	if len(a) == 0 {
		return false
	}
	for _, e := range a {
		if _, ok := e.(bson.D); !ok {
			return false
		}
	}
	return true
}

// ---- globals ---------------------------------------------------------------------------

func (s *Shell) throw(err error) { panic(s.vm.NewGoError(err)) }

func (s *Shell) check(err error) {
	if err != nil {
		s.throw(writeError(err))
	}
}

func (s *Shell) arg(c goja.FunctionCall, i int) any {
	v, err := s.toBSON(c.Argument(i))
	s.check(err)
	return v
}

// docArg returns argument i as a document (an empty one when missing).
func (s *Shell) docArg(c goja.FunctionCall, i int, what string) bson.D {
	v := s.arg(c, i)
	switch x := v.(type) {
	case nil:
		return bson.D{}
	case bson.D:
		return x
	}
	s.throw(fmt.Errorf("%s must be a document", what))
	return nil
}

func (s *Shell) optDoc(c goja.FunctionCall, i int) bson.D {
	if goja.IsUndefined(c.Argument(i)) || goja.IsNull(c.Argument(i)) {
		return nil
	}
	return s.docArg(c, i, "options")
}

func (s *Shell) installShell() {
	vm := s.vm
	set := func(name string, f func(goja.FunctionCall) goja.Value) { _ = vm.Set(name, f) }
	_ = vm.Set("db", vm.NewDynamicObject(&dbObj{s: s, current: true}))
	set("__result", func(c goja.FunctionCall) goja.Value {
		s.addResult(c.Argument(0), int(c.Argument(1).ToInteger()))
		return goja.Undefined()
	})
	set("__use", func(c goja.FunctionCall) goja.Value {
		name := c.Argument(0).String()
		s.DB = name
		return vm.ToValue("switched to db " + name)
	})
	set("__show", func(c goja.FunctionCall) goja.Value { return vm.ToValue(s.show(c.Argument(0).String())) })
	printFn := func(json bool) func(goja.FunctionCall) goja.Value {
		return func(c goja.FunctionCall) goja.Value {
			parts := make([]string, len(c.Arguments))
			for i, a := range c.Arguments {
				parts[i] = s.display(a, json)
			}
			s.out.WriteString(strings.Join(parts, " "))
			s.out.WriteByte('\n')
			return goja.Undefined()
		}
	}
	set("print", printFn(false))
	set("printjson", printFn(true))
	_ = vm.Set("console", map[string]any{"log": printFn(false), "info": printFn(false), "warn": printFn(false), "error": printFn(false)})
	set("tojson", func(c goja.FunctionCall) goja.Value { return vm.ToValue(s.display(c.Argument(0), true)) })
	set("sleep", func(c goja.FunctionCall) goja.Value {
		select {
		case <-time.After(time.Duration(c.Argument(0).ToInteger()) * time.Millisecond):
		case <-s.ctx.Done():
		}
		return goja.Undefined()
	})
}

// display renders a value for print: strings as they are, everything else in shell syntax.
func (s *Shell) display(v goja.Value, json bool) string {
	if !json {
		if _, ok := v.Export().(string); ok {
			return v.String()
		}
	}
	if o, ok := v.(*goja.Object); ok {
		switch x := o.Export().(type) {
		case *cursor:
			return "Cursor(" + x.ns() + ")"
		case *dbObj:
			return x.dbName()
		case *collObj:
			return x.db + "." + x.name
		}
	}
	b, err := s.toBSON(v)
	if err != nil {
		return v.String()
	}
	return Format(ToVal(b), "  ", false)
}

func (s *Shell) show(what string) string {
	ctx := s.ctx
	switch what {
	case "dbs", "databases":
		dbs, err := s.c.Databases(ctx)
		s.check(err)
		w := 0
		for _, d := range dbs {
			w = max(w, len(d.Name))
		}
		var b strings.Builder
		for _, d := range dbs {
			fmt.Fprintf(&b, "%-*s  %s\n", w, d.Name, humanSize(d.Size))
		}
		return strings.TrimRight(b.String(), "\n")
	case "collections", "tables":
		cs, err := s.c.Collections(ctx, s.DB, false)
		s.check(err)
		names := make([]string, len(cs))
		for i, c := range cs {
			names[i] = c.Name
		}
		return strings.Join(names, "\n")
	case "users", "roles":
		cmd := bson.D{{Key: "usersInfo", Value: 1}}
		key := "users"
		if what == "roles" {
			cmd, key = bson.D{{Key: "rolesInfo", Value: 1}, {Key: "showBuiltinRoles", Value: true}}, "roles"
		}
		r, err := s.c.RunCommand(ctx, s.DB, cmd)
		s.check(err)
		for _, e := range r {
			if e.Key == key {
				return Format(ToVal(e.Value), "  ", false)
			}
		}
		return "[]"
	}
	s.throw(fmt.Errorf("don't know how to show %q", what))
	return ""
}

func humanSize(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.2f GiB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.2f MiB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.2f KiB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}

// ---- db -------------------------------------------------------------------------------

type dbObj struct {
	s *Shell
	// current follows the shell's database; otherwise name is fixed.
	current bool
	name    string
	methods map[string]goja.Value
}

func (d *dbObj) dbName() string {
	if d.current {
		return d.s.DB
	}
	return d.name
}

func (d *dbObj) mdb() *mongo.Database { return d.s.c.cl.Database(d.dbName()) }

func (d *dbObj) Get(key string) goja.Value {
	if d.methods == nil {
		d.methods = d.makeMethods()
	}
	if m, ok := d.methods[key]; ok {
		return m
	}
	if key == "_name" {
		return d.s.vm.ToValue(d.dbName())
	}
	if strings.HasPrefix(key, "__") || key == "then" || key == "constructor" || key == "inspect" {
		return nil
	}
	return d.s.collection(d.dbName(), key)
}
func (d *dbObj) Set(string, goja.Value) bool { return false }
func (d *dbObj) Has(key string) bool         { return true }
func (d *dbObj) Delete(string) bool          { return false }
func (d *dbObj) Keys() []string {
	cs, err := d.s.c.Collections(d.s.ctx, d.dbName(), false)
	if err != nil {
		return nil
	}
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.Name
	}
	return out
}

func (s *Shell) collection(db, name string) goja.Value {
	return s.vm.NewDynamicObject(&collObj{s: s, db: db, name: name})
}

func (d *dbObj) makeMethods() map[string]goja.Value {
	s, vm := d.s, d.s.vm
	fn := func(f func(c goja.FunctionCall) goja.Value) goja.Value { return vm.ToValue(f) }
	run := func(db string, cmd bson.D) goja.Value {
		r, err := s.c.RunCommand(s.ctx, db, cmd)
		s.check(err)
		return s.toJS(r)
	}
	cmdArg := func(c goja.FunctionCall) bson.D {
		if str, ok := c.Argument(0).Export().(string); ok {
			return bson.D{{Key: str, Value: int32(1)}}
		}
		return s.docArg(c, 0, "command")
	}
	return map[string]goja.Value{
		"getName":  fn(func(goja.FunctionCall) goja.Value { return vm.ToValue(d.dbName()) }),
		"toString": fn(func(goja.FunctionCall) goja.Value { return vm.ToValue(d.dbName()) }),
		"getCollection": fn(func(c goja.FunctionCall) goja.Value {
			return s.collection(d.dbName(), c.Argument(0).String())
		}),
		"getSiblingDB": fn(func(c goja.FunctionCall) goja.Value {
			return vm.NewDynamicObject(&dbObj{s: s, name: c.Argument(0).String()})
		}),
		"getCollectionNames": fn(func(goja.FunctionCall) goja.Value {
			names := d.Keys()
			items := make([]any, len(names))
			for i, n := range names {
				items[i] = n
			}
			return vm.NewArray(items...)
		}),
		"getCollectionInfos": fn(func(c goja.FunctionCall) goja.Value {
			cur, err := d.mdb().ListCollections(s.ctx, s.docArg(c, 0, "filter"))
			s.check(err)
			var docs []bson.D
			s.check(cur.All(s.ctx, &docs))
			a := make(bson.A, len(docs))
			for i, x := range docs {
				a[i] = x
			}
			return s.toJS(a)
		}),
		"createCollection": fn(func(c goja.FunctionCall) goja.Value {
			cmd := append(bson.D{{Key: "create", Value: c.Argument(0).String()}}, s.optDoc(c, 1)...)
			return run(d.dbName(), cmd)
		}),
		"createView": fn(func(c goja.FunctionCall) goja.Value {
			p, _ := s.arg(c, 2).(bson.A)
			if p == nil {
				p = bson.A{}
			}
			cmd := append(bson.D{{Key: "create", Value: c.Argument(0).String()}, {Key: "viewOn", Value: c.Argument(1).String()},
				{Key: "pipeline", Value: p}}, s.optDoc(c, 3)...)
			return run(d.dbName(), cmd)
		}),
		"dropDatabase": fn(func(goja.FunctionCall) goja.Value {
			return run(d.dbName(), bson.D{{Key: "dropDatabase", Value: int32(1)}})
		}),
		"runCommand":   fn(func(c goja.FunctionCall) goja.Value { return run(d.dbName(), cmdArg(c)) }),
		"adminCommand": fn(func(c goja.FunctionCall) goja.Value { return run("admin", cmdArg(c)) }),
		"stats": fn(func(c goja.FunctionCall) goja.Value {
			return run(d.dbName(), bson.D{{Key: "dbStats", Value: int32(1)}})
		}),
		"version": fn(func(goja.FunctionCall) goja.Value {
			return vm.ToValue(strings.TrimPrefix(s.c.version, "MongoDB "))
		}),
		"serverStatus": fn(func(goja.FunctionCall) goja.Value {
			return run("admin", bson.D{{Key: "serverStatus", Value: int32(1)}})
		}),
		"hostInfo": fn(func(goja.FunctionCall) goja.Value {
			return run("admin", bson.D{{Key: "hostInfo", Value: int32(1)}})
		}),
		"serverBuildInfo": fn(func(goja.FunctionCall) goja.Value {
			return run("admin", bson.D{{Key: "buildInfo", Value: int32(1)}})
		}),
		"currentOp": fn(func(c goja.FunctionCall) goja.Value {
			return run("admin", append(bson.D{{Key: "currentOp", Value: int32(1)}}, s.optDoc(c, 0)...))
		}),
		"killOp": fn(func(c goja.FunctionCall) goja.Value {
			return run("admin", bson.D{{Key: "killOp", Value: int32(1)}, {Key: "op", Value: s.arg(c, 0)}})
		}),
		"getUsers": fn(func(goja.FunctionCall) goja.Value {
			return run(d.dbName(), bson.D{{Key: "usersInfo", Value: int32(1)}})
		}),
		"getRoles": fn(func(goja.FunctionCall) goja.Value {
			return run(d.dbName(), bson.D{{Key: "rolesInfo", Value: int32(1)}})
		}),
		"createUser": fn(func(c goja.FunctionCall) goja.Value {
			u := s.docArg(c, 0, "user")
			cmd := bson.D{}
			for _, e := range u {
				if e.Key == "user" {
					cmd = append(bson.D{{Key: "createUser", Value: e.Value}}, cmd...)
				} else {
					cmd = append(cmd, e)
				}
			}
			return run(d.dbName(), cmd)
		}),
		"dropUser": fn(func(c goja.FunctionCall) goja.Value {
			return run(d.dbName(), bson.D{{Key: "dropUser", Value: c.Argument(0).String()}})
		}),
		"aggregate": fn(func(c goja.FunctionCall) goja.Value {
			p, ok := s.arg(c, 0).(bson.A)
			if !ok {
				s.throw(fmt.Errorf("aggregate needs an array of stages"))
			}
			return vm.NewDynamicObject(&cursor{s: s, db: d.dbName(), agg: true, pipeline: p, opts: s.optDoc(c, 1), limit: -1})
		}),
	}
}

// ---- collections ------------------------------------------------------------------------

type collObj struct {
	s        *Shell
	db, name string
	methods  map[string]goja.Value
}

func (c *collObj) mc() *mongo.Collection { return c.s.c.cl.Database(c.db).Collection(c.name) }

func (c *collObj) Get(key string) goja.Value {
	if c.methods == nil {
		c.methods = c.makeMethods()
	}
	if m, ok := c.methods[key]; ok {
		return m
	}
	if strings.HasPrefix(key, "__") || key == "then" || key == "constructor" || key == "inspect" {
		return nil
	}
	return c.s.collection(c.db, c.name+"."+key)
}
func (c *collObj) Set(string, goja.Value) bool { return false }
func (c *collObj) Has(string) bool             { return true }
func (c *collObj) Delete(string) bool          { return false }
func (c *collObj) Keys() []string              { return nil }

func optInt(o bson.D, key string) (int64, bool) {
	for _, e := range o {
		if e.Key == key {
			return toInt64(e.Value), true
		}
	}
	return 0, false
}

func optVal(o bson.D, key string) (any, bool) {
	for _, e := range o {
		if e.Key == key {
			return e.Value, true
		}
	}
	return nil, false
}

func optBool(o bson.D, key string) bool {
	v, _ := optVal(o, key)
	b, _ := v.(bool)
	return b
}

func (c *collObj) makeMethods() map[string]goja.Value {
	s, vm := c.s, c.s.vm
	fn := func(f func(a goja.FunctionCall) goja.Value) goja.Value { return vm.ToValue(f) }
	// Result documents; counts are plain numbers, as in mongosh.
	obj := func(kv ...any) goja.Value {
		d := bson.D{}
		for i := 0; i < len(kv); i += 2 {
			v := kv[i+1]
			if n, ok := v.(int64); ok {
				v = float64(n)
			}
			d = append(d, bson.E{Key: kv[i].(string), Value: v})
		}
		return s.toJS(d)
	}
	find := func(a goja.FunctionCall) *cursor {
		cur := &cursor{s: s, db: c.db, coll: c.name, filter: s.docArg(a, 0, "filter"), limit: -1}
		if p := s.arg(a, 1); p != nil {
			d, ok := p.(bson.D)
			if !ok {
				s.throw(fmt.Errorf("projection must be a document"))
			}
			cur.projection = d
		}
		if o := s.optDoc(a, 2); o != nil {
			for _, e := range o {
				switch e.Key {
				case "sort":
					cur.sort, _ = e.Value.(bson.D)
				case "limit":
					cur.limit = toInt64(e.Value)
				case "skip":
					cur.skip = toInt64(e.Value)
				case "projection":
					cur.projection, _ = e.Value.(bson.D)
				default:
					cur.opts = append(cur.opts, e)
				}
			}
		}
		return cur
	}
	updateOpts := func(o bson.D) *options.UpdateOneOptionsBuilder {
		u := options.UpdateOne()
		if optBool(o, "upsert") {
			u.SetUpsert(true)
		}
		if v, ok := optVal(o, "arrayFilters"); ok {
			if a, ok := v.(bson.A); ok {
				u.SetArrayFilters([]any(a))
			}
		}
		if v, ok := optVal(o, "collation"); ok {
			u.SetCollation(collation(v))
		}
		if v, ok := optVal(o, "hint"); ok {
			u.SetHint(v)
		}
		return u
	}
	updateManyOpts := func(o bson.D) *options.UpdateManyOptionsBuilder {
		u := options.UpdateMany()
		if optBool(o, "upsert") {
			u.SetUpsert(true)
		}
		if v, ok := optVal(o, "arrayFilters"); ok {
			if a, ok := v.(bson.A); ok {
				u.SetArrayFilters([]any(a))
			}
		}
		if v, ok := optVal(o, "collation"); ok {
			u.SetCollation(collation(v))
		}
		if v, ok := optVal(o, "hint"); ok {
			u.SetHint(v)
		}
		return u
	}
	updResult := func(r *mongo.UpdateResult) goja.Value {
		return obj("acknowledged", r.Acknowledged, "insertedId", r.UpsertedID, "matchedCount", r.MatchedCount,
			"modifiedCount", r.ModifiedCount, "upsertedCount", r.UpsertedCount)
	}
	update := func(a goja.FunctionCall) any {
		u := s.arg(a, 1)
		switch u.(type) {
		case bson.D, bson.A:
			return u
		}
		s.throw(fmt.Errorf("update must be a document or a pipeline"))
		return nil
	}
	insertMany := func(docs bson.A, o bson.D) goja.Value {
		if len(docs) == 0 {
			s.throw(fmt.Errorf("insertMany needs at least one document"))
		}
		for i, d := range docs {
			if _, ok := d.(bson.D); !ok {
				s.throw(fmt.Errorf("document %d is not a document", i))
			}
		}
		io := options.InsertMany()
		if v, ok := optVal(o, "ordered"); ok {
			b, _ := v.(bool)
			io.SetOrdered(b)
		}
		r, err := c.mc().InsertMany(s.ctx, []any(docs), io)
		s.check(err)
		ids := bson.D{}
		for i, id := range r.InsertedIDs {
			ids = append(ids, bson.E{Key: strconv.Itoa(i), Value: id})
		}
		return obj("acknowledged", r.Acknowledged, "insertedIds", ids)
	}
	insertOne := func(doc any) goja.Value {
		d, ok := doc.(bson.D)
		if !ok {
			s.throw(fmt.Errorf("insertOne needs a document"))
		}
		r, err := c.mc().InsertOne(s.ctx, d)
		s.check(err)
		return obj("acknowledged", r.Acknowledged, "insertedId", r.InsertedID)
	}
	delResult := func(r *mongo.DeleteResult, err error) goja.Value {
		s.check(err)
		return obj("acknowledged", r.Acknowledged, "deletedCount", r.DeletedCount)
	}
	findOneAnd := func(kind string) func(a goja.FunctionCall) goja.Value {
		return func(a goja.FunctionCall) goja.Value {
			filter := s.docArg(a, 0, "filter")
			var o bson.D
			if kind == "delete" {
				o = s.optDoc(a, 1)
			} else {
				o = s.optDoc(a, 2)
			}
			after := optBool(o, "returnNewDocument")
			if v, ok := optVal(o, "returnDocument"); ok {
				after = v == "after"
			}
			sortV, hasSort := optVal(o, "sort")
			projV, hasProj := optVal(o, "projection")
			var res *mongo.SingleResult
			switch kind {
			case "update":
				fo := options.FindOneAndUpdate()
				if after {
					fo.SetReturnDocument(options.After)
				}
				if optBool(o, "upsert") {
					fo.SetUpsert(true)
				}
				if hasSort {
					fo.SetSort(sortV)
				}
				if hasProj {
					fo.SetProjection(projV)
				}
				if v, ok := optVal(o, "arrayFilters"); ok {
					if arr, ok := v.(bson.A); ok {
						fo.SetArrayFilters([]any(arr))
					}
				}
				res = c.mc().FindOneAndUpdate(s.ctx, filter, update(a), fo)
			case "replace":
				fo := options.FindOneAndReplace()
				if after {
					fo.SetReturnDocument(options.After)
				}
				if optBool(o, "upsert") {
					fo.SetUpsert(true)
				}
				if hasSort {
					fo.SetSort(sortV)
				}
				if hasProj {
					fo.SetProjection(projV)
				}
				res = c.mc().FindOneAndReplace(s.ctx, filter, s.docArg(a, 1, "replacement"), fo)
			default:
				fo := options.FindOneAndDelete()
				if hasSort {
					fo.SetSort(sortV)
				}
				if hasProj {
					fo.SetProjection(projV)
				}
				res = c.mc().FindOneAndDelete(s.ctx, filter, fo)
			}
			var d bson.D
			if err := res.Decode(&d); err != nil {
				if errors.Is(err, mongo.ErrNoDocuments) {
					return goja.Null()
				}
				s.throw(err)
			}
			return s.toJS(d)
		}
	}
	run := func(cmd bson.D) goja.Value {
		r, err := s.c.RunCommand(s.ctx, c.db, cmd)
		s.check(err)
		return s.toJS(r)
	}
	return map[string]goja.Value{
		"getName":     fn(func(goja.FunctionCall) goja.Value { return vm.ToValue(c.name) }),
		"getFullName": fn(func(goja.FunctionCall) goja.Value { return vm.ToValue(c.db + "." + c.name) }),
		"toString":    fn(func(goja.FunctionCall) goja.Value { return vm.ToValue(c.db + "." + c.name) }),
		"getDB": fn(func(goja.FunctionCall) goja.Value {
			return vm.NewDynamicObject(&dbObj{s: s, name: c.db})
		}),
		"find": fn(func(a goja.FunctionCall) goja.Value { return vm.NewDynamicObject(find(a)) }),
		"findOne": fn(func(a goja.FunctionCall) goja.Value {
			cur := find(a)
			cur.limit = 1
			docs, _ := cur.page(0, 1)
			if len(docs) == 0 {
				return goja.Null()
			}
			return s.toJS(docs[0])
		}),
		"aggregate": fn(func(a goja.FunctionCall) goja.Value {
			var p bson.A
			var o bson.D
			if arr, ok := s.arg(a, 0).(bson.A); ok {
				p, o = arr, s.optDoc(a, 1)
			} else {
				for i := range a.Arguments {
					p = append(p, s.docArg(a, i, "stage"))
				}
			}
			return vm.NewDynamicObject(&cursor{s: s, db: c.db, coll: c.name, agg: true, pipeline: p, opts: o, limit: -1})
		}),
		"insertOne": fn(func(a goja.FunctionCall) goja.Value { return insertOne(s.arg(a, 0)) }),
		"insertMany": fn(func(a goja.FunctionCall) goja.Value {
			arr, _ := s.arg(a, 0).(bson.A)
			return insertMany(arr, s.optDoc(a, 1))
		}),
		"insert": fn(func(a goja.FunctionCall) goja.Value {
			v := s.arg(a, 0)
			if arr, ok := v.(bson.A); ok {
				return insertMany(arr, s.optDoc(a, 1))
			}
			return insertOne(v)
		}),
		"updateOne": fn(func(a goja.FunctionCall) goja.Value {
			r, err := c.mc().UpdateOne(s.ctx, s.docArg(a, 0, "filter"), update(a), updateOpts(s.optDoc(a, 2)))
			s.check(err)
			return updResult(r)
		}),
		"updateMany": fn(func(a goja.FunctionCall) goja.Value {
			r, err := c.mc().UpdateMany(s.ctx, s.docArg(a, 0, "filter"), update(a), updateManyOpts(s.optDoc(a, 2)))
			s.check(err)
			return updResult(r)
		}),
		"update": fn(func(a goja.FunctionCall) goja.Value {
			o := s.optDoc(a, 2)
			var r *mongo.UpdateResult
			var err error
			if optBool(o, "multi") {
				r, err = c.mc().UpdateMany(s.ctx, s.docArg(a, 0, "filter"), update(a), updateManyOpts(o))
			} else {
				r, err = c.mc().UpdateOne(s.ctx, s.docArg(a, 0, "filter"), update(a), updateOpts(o))
			}
			s.check(err)
			return updResult(r)
		}),
		"replaceOne": fn(func(a goja.FunctionCall) goja.Value {
			ro := options.Replace()
			o := s.optDoc(a, 2)
			if optBool(o, "upsert") {
				ro.SetUpsert(true)
			}
			r, err := c.mc().ReplaceOne(s.ctx, s.docArg(a, 0, "filter"), s.docArg(a, 1, "replacement"), ro)
			s.check(err)
			return updResult(r)
		}),
		"deleteOne": fn(func(a goja.FunctionCall) goja.Value {
			return delResult(c.mc().DeleteOne(s.ctx, s.docArg(a, 0, "filter")))
		}),
		"deleteMany": fn(func(a goja.FunctionCall) goja.Value {
			return delResult(c.mc().DeleteMany(s.ctx, s.docArg(a, 0, "filter")))
		}),
		"remove": fn(func(a goja.FunctionCall) goja.Value {
			justOne := a.Argument(1).ToBoolean()
			if o, ok := a.Argument(1).(*goja.Object); ok {
				justOne = o.Get("justOne") != nil && o.Get("justOne").ToBoolean()
			}
			if justOne {
				return delResult(c.mc().DeleteOne(s.ctx, s.docArg(a, 0, "filter")))
			}
			return delResult(c.mc().DeleteMany(s.ctx, s.docArg(a, 0, "filter")))
		}),
		"findOneAndUpdate":  fn(findOneAnd("update")),
		"findOneAndReplace": fn(findOneAnd("replace")),
		"findOneAndDelete":  fn(findOneAnd("delete")),
		"countDocuments": fn(func(a goja.FunctionCall) goja.Value {
			o := options.Count()
			opt := s.optDoc(a, 1)
			if n, ok := optInt(opt, "limit"); ok {
				o.SetLimit(n)
			}
			if n, ok := optInt(opt, "skip"); ok {
				o.SetSkip(n)
			}
			n, err := c.mc().CountDocuments(s.ctx, s.docArg(a, 0, "filter"), o)
			s.check(err)
			return vm.ToValue(n)
		}),
		"estimatedDocumentCount": fn(func(goja.FunctionCall) goja.Value {
			n, err := c.mc().EstimatedDocumentCount(s.ctx)
			s.check(err)
			return vm.ToValue(n)
		}),
		"count": fn(func(a goja.FunctionCall) goja.Value {
			f := s.docArg(a, 0, "filter")
			var n int64
			var err error
			if len(f) == 0 {
				n, err = c.mc().EstimatedDocumentCount(s.ctx)
			} else {
				n, err = c.mc().CountDocuments(s.ctx, f)
			}
			s.check(err)
			return vm.ToValue(n)
		}),
		"distinct": fn(func(a goja.FunctionCall) goja.Value {
			r := c.mc().Distinct(s.ctx, a.Argument(0).String(), s.docArg(a, 1, "filter"))
			var vals bson.A
			s.check(r.Decode(&vals))
			return s.toJS(vals)
		}),
		"createIndex": fn(func(a goja.FunctionCall) goja.Value {
			spec := append(bson.D{{Key: "key", Value: s.docArg(a, 0, "keys")}}, s.optDoc(a, 1)...)
			names := c.createIndexes([]bson.D{spec})
			return vm.ToValue(names[0])
		}),
		"createIndexes": fn(func(a goja.FunctionCall) goja.Value {
			arr, _ := s.arg(a, 0).(bson.A)
			o := s.optDoc(a, 1)
			specs := make([]bson.D, 0, len(arr))
			for _, k := range arr {
				kd, ok := k.(bson.D)
				if !ok {
					s.throw(fmt.Errorf("index keys must be documents"))
				}
				specs = append(specs, append(bson.D{{Key: "key", Value: kd}}, o...))
			}
			names := c.createIndexes(specs)
			items := make([]any, len(names))
			for i, n := range names {
				items[i] = n
			}
			return vm.NewArray(items...)
		}),
		"getIndexes": fn(func(goja.FunctionCall) goja.Value {
			ix, err := s.c.Indexes(s.ctx, c.db, c.name)
			s.check(err)
			a := make(bson.A, len(ix))
			for i, x := range ix {
				v, _ := FromVal(x.Spec)
				a[i] = v
			}
			return s.toJS(a)
		}),
		"dropIndex": fn(func(a goja.FunctionCall) goja.Value {
			return run(bson.D{{Key: "dropIndexes", Value: c.name}, {Key: "index", Value: s.arg(a, 0)}})
		}),
		"dropIndexes": fn(func(a goja.FunctionCall) goja.Value {
			ix := s.arg(a, 0)
			if ix == nil {
				ix = "*"
			}
			return run(bson.D{{Key: "dropIndexes", Value: c.name}, {Key: "index", Value: ix}})
		}),
		"drop": fn(func(goja.FunctionCall) goja.Value {
			s.check(c.mc().Drop(s.ctx))
			return vm.ToValue(true)
		}),
		"renameCollection": fn(func(a goja.FunctionCall) goja.Value {
			r, err := s.c.RunCommand(s.ctx, "admin", bson.D{{Key: "renameCollection", Value: c.db + "." + c.name},
				{Key: "to", Value: c.db + "." + a.Argument(0).String()}, {Key: "dropTarget", Value: a.Argument(1).ToBoolean()}})
			s.check(err)
			return s.toJS(r)
		}),
		"stats": fn(func(goja.FunctionCall) goja.Value {
			return run(bson.D{{Key: "collStats", Value: c.name}})
		}),
		"runCommand": fn(func(a goja.FunctionCall) goja.Value {
			return run(append(bson.D{{Key: a.Argument(0).String(), Value: c.name}}, s.optDoc(a, 1)...))
		}),
		"bulkWrite": fn(func(a goja.FunctionCall) goja.Value { return c.bulkWrite(a) }),
		"exists": fn(func(goja.FunctionCall) goja.Value {
			cur, err := s.c.cl.Database(c.db).ListCollections(s.ctx, bson.D{{Key: "name", Value: c.name}})
			s.check(err)
			var docs []bson.D
			s.check(cur.All(s.ctx, &docs))
			if len(docs) == 0 {
				return goja.Null()
			}
			return s.toJS(docs[0])
		}),
	}
}

func collation(v any) *options.Collation {
	d, _ := v.(bson.D)
	raw, err := bson.Marshal(d)
	if err != nil {
		return nil
	}
	var c options.Collation
	if bson.Unmarshal(raw, &c) != nil {
		return nil
	}
	return &c
}

func (c *collObj) createIndexes(specs []bson.D) []string {
	s := c.s
	names := make([]string, len(specs))
	for i, sp := range specs {
		if _, ok := optVal(sp, "name"); !ok {
			keys, _ := sp[0].Value.(bson.D)
			parts := make([]string, len(keys))
			for j, k := range keys {
				parts[j] = k.Key + "_" + fmt.Sprint(k.Value)
			}
			sp = append(sp, bson.E{Key: "name", Value: strings.Join(parts, "_")})
			specs[i] = sp
		}
		n, _ := optVal(sp, "name")
		names[i] = fmt.Sprint(n)
	}
	a := make(bson.A, len(specs))
	for i, sp := range specs {
		a[i] = sp
	}
	_, err := s.c.RunCommand(s.ctx, c.db, bson.D{{Key: "createIndexes", Value: c.name}, {Key: "indexes", Value: a}})
	s.check(err)
	return names
}

func (c *collObj) bulkWrite(a goja.FunctionCall) goja.Value {
	s := c.s
	ops, _ := s.arg(a, 0).(bson.A)
	var models []mongo.WriteModel
	for _, op := range ops {
		d, ok := op.(bson.D)
		if !ok || len(d) != 1 {
			s.throw(fmt.Errorf("each bulkWrite operation is a document with one key"))
		}
		body, _ := d[0].Value.(bson.D)
		get := func(k string) any { v, _ := optVal(body, k); return v }
		switch d[0].Key {
		case "insertOne":
			models = append(models, mongo.NewInsertOneModel().SetDocument(get("document")))
		case "updateOne":
			m := mongo.NewUpdateOneModel().SetFilter(get("filter")).SetUpdate(get("update"))
			if optBool(body, "upsert") {
				m.SetUpsert(true)
			}
			models = append(models, m)
		case "updateMany":
			m := mongo.NewUpdateManyModel().SetFilter(get("filter")).SetUpdate(get("update"))
			if optBool(body, "upsert") {
				m.SetUpsert(true)
			}
			models = append(models, m)
		case "replaceOne":
			m := mongo.NewReplaceOneModel().SetFilter(get("filter")).SetReplacement(get("replacement"))
			if optBool(body, "upsert") {
				m.SetUpsert(true)
			}
			models = append(models, m)
		case "deleteOne":
			models = append(models, mongo.NewDeleteOneModel().SetFilter(get("filter")))
		case "deleteMany":
			models = append(models, mongo.NewDeleteManyModel().SetFilter(get("filter")))
		default:
			s.throw(fmt.Errorf("unknown bulkWrite operation %q", d[0].Key))
		}
	}
	o := options.BulkWrite()
	if v, ok := optVal(s.optDoc(a, 1), "ordered"); ok {
		b, _ := v.(bool)
		o.SetOrdered(b)
	}
	r, err := c.mc().BulkWrite(s.ctx, models, o)
	s.check(err)
	return s.toJS(bson.D{{Key: "acknowledged", Value: r.Acknowledged}, {Key: "insertedCount", Value: float64(r.InsertedCount)},
		{Key: "matchedCount", Value: float64(r.MatchedCount)}, {Key: "modifiedCount", Value: float64(r.ModifiedCount)},
		{Key: "deletedCount", Value: float64(r.DeletedCount)}, {Key: "upsertedCount", Value: float64(r.UpsertedCount)}})
}

// ---- cursors ----------------------------------------------------------------------------

// cursor is a find or aggregate query. Results are fetched by page (re-running
// the query with skip and limit) or iterated from JavaScript.
type cursor struct {
	s        *Shell
	db, coll string
	agg      bool
	pipeline bson.A
	filter   bson.D
	// projection and sort for find
	projection, sort bson.D
	skip             int64
	limit            int64 // -1 none
	opts             bson.D
	transform        []goja.Callable
	// live iteration from JavaScript
	live    *mongo.Cursor
	peeked  bool
	done    bool
	methods map[string]goja.Value
	// written: an aggregate ending in $out or $merge has already run.
	written bool
}

func (c *cursor) ns() string {
	if c.coll == "" {
		return c.db
	}
	return c.db + "." + c.coll
}

func (c *cursor) writes() bool {
	if len(c.pipeline) == 0 {
		return false
	}
	last, _ := c.pipeline[len(c.pipeline)-1].(bson.D)
	return len(last) > 0 && (last[0].Key == "$out" || last[0].Key == "$merge")
}

// open starts the query, from offset skip with at most limit documents (-1: all).
func (c *cursor) open(skip, limit int64) (*mongo.Cursor, error) {
	s := c.s
	if c.agg {
		p := append(bson.A{}, c.pipeline...)
		o := options.Aggregate()
		for _, e := range c.opts {
			switch e.Key {
			case "allowDiskUse":
				b, _ := e.Value.(bool)
				o.SetAllowDiskUse(b)
			case "collation":
				o.SetCollation(collation(e.Value))
			case "hint":
				o.SetHint(e.Value)
			case "maxTimeMS":
			case "comment":
				o.SetComment(e.Value)
			case "batchSize":
				o.SetBatchSize(int32(toInt64(e.Value)))
			case "let":
				o.SetLet(e.Value)
			}
		}
		if c.writes() {
			if c.written {
				return nil, nil
			}
			c.written = true
		} else {
			if skip > 0 {
				p = append(p, bson.D{{Key: "$skip", Value: skip}})
			}
			if limit >= 0 {
				p = append(p, bson.D{{Key: "$limit", Value: max(limit, 1)}})
			}
		}
		if c.coll == "" {
			return s.c.cl.Database(c.db).Aggregate(s.ctx, p, o)
		}
		return s.c.cl.Database(c.db).Collection(c.coll).Aggregate(s.ctx, p, o)
	}
	o := options.Find()
	if c.projection != nil {
		o.SetProjection(c.projection)
	}
	if c.sort != nil {
		o.SetSort(c.sort)
	}
	o.SetSkip(c.skip + skip)
	lim := limit
	if c.limit >= 0 {
		rest := c.limit - skip
		if c.limit == 0 {
			rest = -1 // limit(0) means none
		}
		if rest == 0 || rest < 0 && c.limit != 0 {
			return nil, nil
		}
		if lim < 0 || rest > 0 && rest < lim {
			lim = rest
		}
	}
	if lim >= 0 {
		o.SetLimit(lim)
	}
	for _, e := range c.opts {
		switch e.Key {
		case "hint":
			o.SetHint(e.Value)
		case "collation":
			o.SetCollation(collation(e.Value))
		case "comment":
			o.SetComment(e.Value)
		case "batchSize":
			o.SetBatchSize(int32(toInt64(e.Value)))
		case "allowDiskUse":
			b, _ := e.Value.(bool)
			o.SetAllowDiskUse(b)
		case "noCursorTimeout":
			b, _ := e.Value.(bool)
			o.SetNoCursorTimeout(b)
		case "max":
			o.SetMax(e.Value)
		case "min":
			o.SetMin(e.Value)
		case "returnKey":
			b, _ := e.Value.(bool)
			o.SetReturnKey(b)
		case "showRecordId":
			b, _ := e.Value.(bool)
			o.SetShowRecordID(b)
		}
	}
	return s.c.cl.Database(c.db).Collection(c.coll).Find(s.ctx, c.filter, o)
}

// page fetches documents [skip, skip+limit) and reports whether more follow.
func (c *cursor) page(skip, limit int) ([]bson.D, bool) {
	s := c.s
	cur, err := c.open(int64(skip), int64(limit)+1)
	s.check(err)
	if cur == nil {
		return nil, false
	}
	defer cur.Close(s.ctx)
	var docs []bson.D
	for cur.Next(s.ctx) {
		var d bson.D
		s.check(bson.Unmarshal(cur.Current, &d))
		docs = append(docs, d)
	}
	s.check(cur.Err())
	more := len(docs) > limit
	if more {
		docs = docs[:limit]
	}
	if len(c.transform) > 0 {
		for i, d := range docs {
			docs[i] = c.apply(d)
		}
	}
	return docs, more
}

func (c *cursor) apply(d bson.D) bson.D {
	s := c.s
	v := s.toJS(d)
	for _, f := range c.transform {
		r, err := f(goja.Undefined(), v)
		if err != nil {
			panic(err)
		}
		v = r
	}
	b, err := s.toBSON(v)
	s.check(err)
	if dd, ok := b.(bson.D); ok {
		return dd
	}
	return bson.D{{Key: "value", Value: b}}
}

func (c *cursor) applyJS(d bson.D) goja.Value {
	v := c.s.toJS(d)
	for _, f := range c.transform {
		r, err := f(goja.Undefined(), v)
		if err != nil {
			panic(err)
		}
		v = r
	}
	return v
}

func (c *cursor) hasNext() bool {
	s := c.s
	if c.done {
		return false
	}
	if c.peeked {
		return true
	}
	if c.live == nil {
		cur, err := c.open(0, -1)
		s.check(err)
		if cur == nil {
			c.done = true
			return false
		}
		c.live = cur
	}
	if c.live.Next(s.ctx) {
		c.peeked = true
		return true
	}
	s.check(c.live.Err())
	c.live.Close(s.ctx)
	c.done = true
	return false
}

func (c *cursor) next() (bson.D, bool) {
	if !c.hasNext() {
		return nil, false
	}
	c.peeked = false
	var d bson.D
	c.s.check(bson.Unmarshal(c.live.Current, &d))
	return d, true
}

func (c *cursor) Get(key string) goja.Value {
	if c.methods == nil {
		c.methods = c.makeMethods()
	}
	return c.methods[key]
}
func (c *cursor) Set(string, goja.Value) bool { return false }
func (c *cursor) Has(key string) bool {
	if c.methods == nil {
		c.methods = c.makeMethods()
	}
	_, ok := c.methods[key]
	return ok
}
func (c *cursor) Delete(string) bool { return false }
func (c *cursor) Keys() []string     { return nil }

func (c *cursor) makeMethods() map[string]goja.Value {
	s, vm := c.s, c.s.vm
	var self goja.Value
	ret := func() goja.Value {
		if self == nil {
			self = vm.NewDynamicObject(c)
		}
		return self
	}
	// The object the methods were called on, so chaining returns the same cursor.
	chain := func(f func(a goja.FunctionCall)) goja.Value {
		return vm.ToValue(func(a goja.FunctionCall) goja.Value {
			if c.live != nil {
				s.throw(fmt.Errorf("the cursor has already been iterated"))
			}
			f(a)
			if o, ok := a.This.(*goja.Object); ok {
				return o
			}
			return ret()
		})
	}
	fn := func(f func(a goja.FunctionCall) goja.Value) goja.Value { return vm.ToValue(f) }
	setOpt := func(k string) goja.Value {
		return chain(func(a goja.FunctionCall) { c.opts = append(c.opts, bson.E{Key: k, Value: s.arg(a, 0)}) })
	}
	all := func() []goja.Value {
		var out []goja.Value
		for {
			d, ok := c.next()
			if !ok {
				return out
			}
			out = append(out, c.applyJS(d))
		}
	}
	m := map[string]goja.Value{
		"sort": chain(func(a goja.FunctionCall) {
			c.sort = s.docArg(a, 0, "sort")
			if c.agg {
				c.pipeline = append(c.pipeline, bson.D{{Key: "$sort", Value: c.sort}})
			}
		}),
		"limit": chain(func(a goja.FunctionCall) {
			c.limit = a.Argument(0).ToInteger()
			if c.limit < 0 {
				c.limit = -c.limit
			}
		}),
		"skip":       chain(func(a goja.FunctionCall) { c.skip = a.Argument(0).ToInteger() }),
		"projection": chain(func(a goja.FunctionCall) { c.projection = s.docArg(a, 0, "projection") }),
		"project":    chain(func(a goja.FunctionCall) { c.projection = s.docArg(a, 0, "projection") }),
		"hint":       setOpt("hint"),
		"collation":  setOpt("collation"),
		"comment":    setOpt("comment"),
		"batchSize":  setOpt("batchSize"),
		"maxTimeMS":  setOpt("maxTimeMS"),
		"max":        setOpt("max"),
		"min":        setOpt("min"),
		"returnKey":  setOpt("returnKey"),
		"allowDiskUse": chain(func(a goja.FunctionCall) {
			c.opts = append(c.opts, bson.E{Key: "allowDiskUse", Value: goja.IsUndefined(a.Argument(0)) || a.Argument(0).ToBoolean()})
		}),
		"noCursorTimeout": chain(func(goja.FunctionCall) { c.opts = append(c.opts, bson.E{Key: "noCursorTimeout", Value: true}) }),
		"showRecordId":    chain(func(goja.FunctionCall) { c.opts = append(c.opts, bson.E{Key: "showRecordId", Value: true}) }),
		"readPref":        chain(func(goja.FunctionCall) {}),
		"pretty":          chain(func(goja.FunctionCall) {}),
		"map": chain(func(a goja.FunctionCall) {
			f, ok := goja.AssertFunction(a.Argument(0))
			if !ok {
				s.throw(fmt.Errorf("map needs a function"))
			}
			c.transform = append(c.transform, f)
		}),
		"toArray": fn(func(goja.FunctionCall) goja.Value {
			items := all()
			anys := make([]any, len(items))
			for i, v := range items {
				anys[i] = v
			}
			return vm.NewArray(anys...)
		}),
		"forEach": fn(func(a goja.FunctionCall) goja.Value {
			f, ok := goja.AssertFunction(a.Argument(0))
			if !ok {
				s.throw(fmt.Errorf("forEach needs a function"))
			}
			for {
				d, ok := c.next()
				if !ok {
					return goja.Undefined()
				}
				r, err := f(goja.Undefined(), c.applyJS(d))
				if err != nil {
					panic(err)
				}
				if r != nil && r.Export() == false {
					return goja.Undefined()
				}
			}
		}),
		"hasNext": fn(func(goja.FunctionCall) goja.Value { return vm.ToValue(c.hasNext()) }),
		"next": fn(func(goja.FunctionCall) goja.Value {
			d, ok := c.next()
			if !ok {
				s.throw(fmt.Errorf("cursor is exhausted"))
			}
			return c.applyJS(d)
		}),
		"tryNext": fn(func(goja.FunctionCall) goja.Value {
			d, ok := c.next()
			if !ok {
				return goja.Null()
			}
			return c.applyJS(d)
		}),
		"isExhausted": fn(func(goja.FunctionCall) goja.Value { return vm.ToValue(!c.hasNext()) }),
		"itcount": fn(func(goja.FunctionCall) goja.Value {
			n := 0
			for {
				if _, ok := c.next(); !ok {
					return vm.ToValue(n)
				}
				n++
			}
		}),
		"count": fn(func(goja.FunctionCall) goja.Value {
			if c.agg {
				s.throw(fmt.Errorf("count is not available on an aggregation cursor; use itcount()"))
			}
			n, err := s.c.cl.Database(c.db).Collection(c.coll).CountDocuments(s.ctx, c.filter)
			s.check(err)
			return vm.ToValue(n)
		}),
		"size": fn(func(goja.FunctionCall) goja.Value {
			o := options.Count()
			if c.skip > 0 {
				o.SetSkip(c.skip)
			}
			if c.limit > 0 {
				o.SetLimit(c.limit)
			}
			n, err := s.c.cl.Database(c.db).Collection(c.coll).CountDocuments(s.ctx, c.filter, o)
			s.check(err)
			return vm.ToValue(n)
		}),
		"explain": fn(func(a goja.FunctionCall) goja.Value {
			verb := "queryPlanner"
			if v, ok := a.Argument(0).Export().(string); ok {
				verb = v
			} else if a.Argument(0).ToBoolean() {
				verb = "allPlansExecution"
			}
			var inner bson.D
			if c.agg {
				inner = bson.D{{Key: "aggregate", Value: c.coll}, {Key: "pipeline", Value: c.pipeline}, {Key: "cursor", Value: bson.D{}}}
				if c.coll == "" {
					inner[0].Value = int32(1)
				}
			} else {
				inner = bson.D{{Key: "find", Value: c.coll}, {Key: "filter", Value: c.filter}}
				if c.projection != nil {
					inner = append(inner, bson.E{Key: "projection", Value: c.projection})
				}
				if c.sort != nil {
					inner = append(inner, bson.E{Key: "sort", Value: c.sort})
				}
				if c.skip > 0 {
					inner = append(inner, bson.E{Key: "skip", Value: c.skip})
				}
				if c.limit > 0 {
					inner = append(inner, bson.E{Key: "limit", Value: c.limit})
				}
			}
			r, err := s.c.RunCommand(s.ctx, c.db, bson.D{{Key: "explain", Value: inner}, {Key: "verbosity", Value: verb}})
			s.check(err)
			return s.toJS(r)
		}),
		"close": fn(func(goja.FunctionCall) goja.Value {
			if c.live != nil {
				c.live.Close(s.ctx)
			}
			c.done = true
			return goja.Undefined()
		}),
		"toString": fn(func(goja.FunctionCall) goja.Value { return vm.ToValue("Cursor(" + c.ns() + ")") }),
	}
	return m
}

// CollectionNames lists collection names of the shell's current database, for completion.
func (s *Shell) CollectionNames(ctx context.Context) []string {
	cs, err := s.c.Collections(ctx, s.DB, false)
	if err != nil {
		return nil
	}
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.Name
	}
	sort.Strings(out)
	return out
}
