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
	// Pooled runtimes are shared: an expression must not be able to replace ObjectId
	// or the like, or add a getter to Object.prototype, for whoever evaluates next.
	if _, err := s.vm.RunProgram(lockdownProgram); err != nil {
		panic(err)
	}
	return s
}}

// lockdownProgram freezes everything reachable from the global object: the
// helpers, the built-in constructors and their prototypes.
var lockdownProgram = goja.MustCompile("lockdown.js", `(function (root) {
	const seen = new Set();
	const freeze = (o) => {
		if (o === null || (typeof o !== "object" && typeof o !== "function") || seen.has(o)) return;
		seen.add(o);
		Object.freeze(o);
		freeze(Object.getPrototypeOf(o));
		for (const k of Reflect.ownKeys(o)) {
			const d = Object.getOwnPropertyDescriptor(o, k);
			if (d) { freeze(d.value); freeze(d.get); freeze(d.set); }
		}
	};
	freeze(root);
})(this)`, false)

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
	s.vm.ClearInterrupt()
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
	// DB and Collection name the collection a cursor reads.
	DB         string  `json:"db,omitempty"`
	Collection string  `json:"collection,omitempty"`
	ElapsedMS  float64 `json:"elapsedMs"`
	// Editable: the documents are the collection's own (a find without map()), so
	// they can be edited in place. Aggregation results can't.
	Editable bool `json:"editable"`
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
	// Shell commands only count on lines that start in code, not inside a string,
	// template literal or comment.
	code := codeLines(src)
	lines := strings.SplitAfter(src, "\n")
	for i, l := range lines {
		if i < len(code) && code[i] {
			if g := cmdLine.FindStringSubmatch(strings.TrimRight(l, "\r\n")); g != nil {
				lines[i] = g[1] + "__" + g[2] + "(" + strconv.Quote(g[3]) + ");" + l[len(strings.TrimRight(l, "\r\n")):]
			}
		}
	}
	src = strings.Join(lines, "")
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
		// A parenthesised expression's span starts inside its parentheses: take in
		// opening ones until the text balances.
		for open := parenBalance(src[a:b]); open < 0 && a > 0; {
			j := a - 1
			for j > 0 && (src[j] == ' ' || src[j] == '\t' || src[j] == '\n' || src[j] == '\r') {
				j--
			}
			if src[j] != '(' {
				break
			}
			a = j
			open++
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

// parenBalance counts "(" minus ")" outside strings in s.
func parenBalance(s string) int {
	n := 0
	var quote byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case quote != 0:
			if c == '\\' {
				i++
			} else if c == quote {
				quote = 0
			}
		case c == '"' || c == '\'' || c == '`':
			quote = c
		case c == '(':
			n++
		case c == ')':
			n--
		}
	}
	return n
}

// codeLines reports for each line whether it starts outside strings, template
// literals and block comments.
func codeLines(src string) []bool {
	out := []bool{true}
	var quote byte // ', " or `
	block := false
	for i := 0; i < len(src); i++ {
		c := src[i]
		switch {
		case c == '\n':
			if quote == '\'' || quote == '"' {
				quote = 0 // unterminated: the parser will complain
			}
			out = append(out, quote == 0 && !block)
		case block:
			if c == '*' && i+1 < len(src) && src[i+1] == '/' {
				block = false
				i++
			}
		case quote != 0:
			if c == '\\' {
				i++
			} else if c == quote {
				quote = 0
			}
		case c == '/' && i+1 < len(src) && src[i+1] == '/':
			for i+1 < len(src) && src[i+1] != '\n' {
				i++
			}
		case c == '/' && i+1 < len(src) && src[i+1] == '*':
			block = true
			i++
		case c == '"' || c == '\'' || c == '`':
			quote = c
		}
	}
	return out
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
	// An interrupt that landed after the last run finished must not stop this one.
	s.vm.ClearInterrupt()
	for _, c := range s.cursors {
		c.close()
	}
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

// CurrentDB is the database "db" refers to (waits for a running script).
func (s *Shell) CurrentDB() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.DB
}

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
			r.Editable = !x.agg && x.coll != "" && len(x.transform) == 0
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
		s.throw(WriteError(err))
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
	fn := func(f func(c goja.FunctionCall) goja.Value) goja.Value { return d.s.vm.ToValue(f) }
	// cmd makes a method that runs a fixed command on this database or admin.
	cmd := func(admin bool, name string) goja.Value {
		return fn(func(goja.FunctionCall) goja.Value {
			db := d.dbName()
			if admin {
				db = "admin"
			}
			return d.command(db, bson.D{{Key: name, Value: int32(1)}})
		})
	}
	return map[string]goja.Value{
		"getName":            fn(d.jsGetName),
		"toString":           fn(d.jsGetName),
		"getCollection":      fn(d.jsGetCollection),
		"getSiblingDB":       fn(d.jsGetSiblingDB),
		"getCollectionNames": fn(d.jsGetCollectionNames),
		"getCollectionInfos": fn(d.jsGetCollectionInfos),
		"createCollection":   fn(d.jsCreateCollection),
		"createView":         fn(d.jsCreateView),
		"dropDatabase":       cmd(false, "dropDatabase"),
		"runCommand":         fn(func(c goja.FunctionCall) goja.Value { return d.command(d.dbName(), d.commandArg(c)) }),
		"adminCommand":       fn(func(c goja.FunctionCall) goja.Value { return d.command("admin", d.commandArg(c)) }),
		"stats":              cmd(false, "dbStats"),
		"version":            fn(d.jsVersion),
		"serverStatus":       cmd(true, "serverStatus"),
		"hostInfo":           cmd(true, "hostInfo"),
		"serverBuildInfo":    cmd(true, "buildInfo"),
		"currentOp":          fn(d.jsCurrentOp),
		"killOp":             fn(d.jsKillOp),
		"getUsers":           cmd(false, "usersInfo"),
		"getRoles":           cmd(false, "rolesInfo"),
		"createUser":         fn(d.jsCreateUser),
		"dropUser":           fn(d.jsDropUser),
		"aggregate":          fn(d.jsAggregate),
	}
}

// command runs a database command on db.
func (d *dbObj) command(db string, cmd bson.D) goja.Value {
	r, err := d.s.c.RunCommand(d.s.ctx, db, cmd)
	d.s.check(err)
	return d.s.toJS(r)
}

// commandArg is runCommand's argument: a command name or document.
func (d *dbObj) commandArg(c goja.FunctionCall) bson.D {
	if str, ok := c.Argument(0).Export().(string); ok {
		return bson.D{{Key: str, Value: int32(1)}}
	}
	return d.s.docArg(c, 0, "command")
}

func (d *dbObj) jsGetName(goja.FunctionCall) goja.Value { return d.s.vm.ToValue(d.dbName()) }

func (d *dbObj) jsGetCollection(c goja.FunctionCall) goja.Value {
	return d.s.collection(d.dbName(), c.Argument(0).String())
}

func (d *dbObj) jsGetSiblingDB(c goja.FunctionCall) goja.Value {
	return d.s.vm.NewDynamicObject(&dbObj{s: d.s, name: c.Argument(0).String()})
}

func (d *dbObj) jsGetCollectionNames(goja.FunctionCall) goja.Value {
	names := d.Keys()
	items := make([]any, len(names))
	for i, n := range names {
		items[i] = n
	}
	return d.s.vm.NewArray(items...)
}

func (d *dbObj) jsGetCollectionInfos(c goja.FunctionCall) goja.Value {
	s := d.s
	cur, err := d.mdb().ListCollections(s.ctx, s.docArg(c, 0, "filter"))
	s.check(err)
	var docs []bson.D
	s.check(cur.All(s.ctx, &docs))
	a := make(bson.A, len(docs))
	for i, x := range docs {
		a[i] = x
	}
	return s.toJS(a)
}

func (d *dbObj) jsCreateCollection(c goja.FunctionCall) goja.Value {
	cmd := append(bson.D{{Key: "create", Value: c.Argument(0).String()}}, d.s.optDoc(c, 1)...)
	return d.command(d.dbName(), cmd)
}

func (d *dbObj) jsCreateView(c goja.FunctionCall) goja.Value {
	p, _ := d.s.arg(c, 2).(bson.A)
	if p == nil {
		p = bson.A{}
	}
	cmd := append(bson.D{{Key: "create", Value: c.Argument(0).String()}, {Key: "viewOn", Value: c.Argument(1).String()},
		{Key: "pipeline", Value: p}}, d.s.optDoc(c, 3)...)
	return d.command(d.dbName(), cmd)
}

func (d *dbObj) jsVersion(goja.FunctionCall) goja.Value {
	return d.s.vm.ToValue(strings.TrimPrefix(d.s.c.version, "MongoDB "))
}

func (d *dbObj) jsCurrentOp(c goja.FunctionCall) goja.Value {
	return d.command("admin", append(bson.D{{Key: "currentOp", Value: int32(1)}}, d.s.optDoc(c, 0)...))
}

func (d *dbObj) jsKillOp(c goja.FunctionCall) goja.Value {
	return d.command("admin", bson.D{{Key: "killOp", Value: int32(1)}, {Key: "op", Value: d.s.arg(c, 0)}})
}

func (d *dbObj) jsCreateUser(c goja.FunctionCall) goja.Value {
	u := d.s.docArg(c, 0, "user")
	cmd := bson.D{}
	for _, e := range u {
		if e.Key == "user" {
			cmd = append(bson.D{{Key: "createUser", Value: e.Value}}, cmd...)
		} else {
			cmd = append(cmd, e)
		}
	}
	return d.command(d.dbName(), cmd)
}

func (d *dbObj) jsDropUser(c goja.FunctionCall) goja.Value {
	return d.command(d.dbName(), bson.D{{Key: "dropUser", Value: c.Argument(0).String()}})
}

func (d *dbObj) jsAggregate(c goja.FunctionCall) goja.Value {
	p, ok := d.s.arg(c, 0).(bson.A)
	if !ok {
		d.s.throw(fmt.Errorf("aggregate needs an array of stages"))
	}
	return d.s.aggregate(&cursor{s: d.s, db: d.dbName(), agg: true, pipeline: p, opts: d.s.optDoc(c, 1), limit: -1})
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
	fn := func(f func(a goja.FunctionCall) goja.Value) goja.Value { return c.s.vm.ToValue(f) }
	return map[string]goja.Value{
		"getName":                fn(c.jsGetName),
		"getFullName":            fn(c.jsGetFullName),
		"toString":               fn(c.jsGetFullName),
		"getDB":                  fn(c.jsGetDB),
		"find":                   fn(c.jsFind),
		"findOne":                fn(c.jsFindOne),
		"aggregate":              fn(c.jsAggregate),
		"insertOne":              fn(c.jsInsertOne),
		"insertMany":             fn(c.jsInsertMany),
		"insert":                 fn(c.jsInsert),
		"updateOne":              fn(c.jsUpdateOne),
		"updateMany":             fn(c.jsUpdateMany),
		"update":                 fn(c.jsUpdate),
		"replaceOne":             fn(c.jsReplaceOne),
		"deleteOne":              fn(c.jsDeleteOne),
		"deleteMany":             fn(c.jsDeleteMany),
		"remove":                 fn(c.jsRemove),
		"findOneAndUpdate":       fn(c.jsFindOneAndUpdate),
		"findOneAndReplace":      fn(c.jsFindOneAndReplace),
		"findOneAndDelete":       fn(c.jsFindOneAndDelete),
		"countDocuments":         fn(c.jsCountDocuments),
		"estimatedDocumentCount": fn(c.jsEstimatedDocumentCount),
		"count":                  fn(c.jsCount),
		"distinct":               fn(c.jsDistinct),
		"createIndex":            fn(c.jsCreateIndex),
		"createIndexes":          fn(c.jsCreateIndexes),
		"getIndexes":             fn(c.jsGetIndexes),
		"dropIndex":              fn(c.jsDropIndex),
		"dropIndexes":            fn(c.jsDropIndexes),
		"drop":                   fn(c.jsDrop),
		"renameCollection":       fn(c.jsRenameCollection),
		"stats":                  fn(c.jsStats),
		"runCommand":             fn(c.jsRunCommand),
		"bulkWrite":              fn(c.bulkWrite),
		"exists":                 fn(c.jsExists),
	}
}

// resultDoc is a write's result document; counts are plain numbers, as in mongosh.
func (s *Shell) resultDoc(kv ...any) goja.Value {
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

func (s *Shell) updateResult(r *mongo.UpdateResult) goja.Value {
	return s.resultDoc("acknowledged", r.Acknowledged, "insertedId", r.UpsertedID, "matchedCount", r.MatchedCount,
		"modifiedCount", r.ModifiedCount, "upsertedCount", r.UpsertedCount)
}

func (s *Shell) deleteResult(r *mongo.DeleteResult, err error) goja.Value {
	s.check(err)
	return s.resultDoc("acknowledged", r.Acknowledged, "deletedCount", r.DeletedCount)
}

// command runs a database command on the collection's database.
func (c *collObj) command(cmd bson.D) goja.Value {
	r, err := c.s.c.RunCommand(c.s.ctx, c.db, cmd)
	c.s.check(err)
	return c.s.toJS(r)
}

// findCursor reads find's arguments: filter, projection and options.
func (c *collObj) findCursor(a goja.FunctionCall) *cursor {
	s := c.s
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

// updateArg is an update's second argument: a document or a pipeline.
func (c *collObj) updateArg(a goja.FunctionCall) any {
	u := c.s.arg(a, 1)
	switch u.(type) {
	case bson.D, bson.A:
		return u
	}
	c.s.throw(fmt.Errorf("update must be a document or a pipeline"))
	return nil
}

func (c *collObj) insertDocs(docs bson.A, o bson.D) goja.Value {
	s := c.s
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
	return s.resultDoc("acknowledged", r.Acknowledged, "insertedIds", ids)
}

func (c *collObj) insertDoc(doc any) goja.Value {
	d, ok := doc.(bson.D)
	if !ok {
		c.s.throw(fmt.Errorf("insertOne needs a document"))
	}
	r, err := c.mc().InsertOne(c.s.ctx, d)
	c.s.check(err)
	return c.s.resultDoc("acknowledged", r.Acknowledged, "insertedId", r.InsertedID)
}

func (c *collObj) jsGetName(goja.FunctionCall) goja.Value { return c.s.vm.ToValue(c.name) }

func (c *collObj) jsGetFullName(goja.FunctionCall) goja.Value {
	return c.s.vm.ToValue(c.db + "." + c.name)
}

func (c *collObj) jsGetDB(goja.FunctionCall) goja.Value {
	return c.s.vm.NewDynamicObject(&dbObj{s: c.s, name: c.db})
}

func (c *collObj) jsFind(a goja.FunctionCall) goja.Value {
	return c.s.vm.NewDynamicObject(c.findCursor(a))
}

func (c *collObj) jsFindOne(a goja.FunctionCall) goja.Value {
	cur := c.findCursor(a)
	cur.limit = 1
	docs, _ := cur.page(0, 1)
	if len(docs) == 0 {
		return goja.Null()
	}
	return c.s.toJS(docs[0])
}

func (c *collObj) jsAggregate(a goja.FunctionCall) goja.Value {
	s := c.s
	var p bson.A
	var o bson.D
	if arr, ok := s.arg(a, 0).(bson.A); ok {
		p, o = arr, s.optDoc(a, 1)
	} else {
		for i := range a.Arguments {
			p = append(p, s.docArg(a, i, "stage"))
		}
	}
	return s.aggregate(&cursor{s: s, db: c.db, coll: c.name, agg: true, pipeline: p, opts: o, limit: -1})
}

func (c *collObj) jsInsertOne(a goja.FunctionCall) goja.Value { return c.insertDoc(c.s.arg(a, 0)) }

func (c *collObj) jsInsertMany(a goja.FunctionCall) goja.Value {
	arr, _ := c.s.arg(a, 0).(bson.A)
	return c.insertDocs(arr, c.s.optDoc(a, 1))
}

func (c *collObj) jsInsert(a goja.FunctionCall) goja.Value {
	v := c.s.arg(a, 0)
	if arr, ok := v.(bson.A); ok {
		return c.insertDocs(arr, c.s.optDoc(a, 1))
	}
	return c.insertDoc(v)
}

func (c *collObj) jsUpdateOne(a goja.FunctionCall) goja.Value {
	s := c.s
	r, err := c.mc().UpdateOne(s.ctx, s.docArg(a, 0, "filter"), c.updateArg(a), updateOptions(options.UpdateOne(), s.optDoc(a, 2)))
	s.check(err)
	return s.updateResult(r)
}

func (c *collObj) jsUpdateMany(a goja.FunctionCall) goja.Value {
	s := c.s
	r, err := c.mc().UpdateMany(s.ctx, s.docArg(a, 0, "filter"), c.updateArg(a), updateOptions(options.UpdateMany(), s.optDoc(a, 2)))
	s.check(err)
	return s.updateResult(r)
}

func (c *collObj) jsUpdate(a goja.FunctionCall) goja.Value {
	if optBool(c.s.optDoc(a, 2), "multi") {
		return c.jsUpdateMany(a)
	}
	return c.jsUpdateOne(a)
}

func (c *collObj) jsReplaceOne(a goja.FunctionCall) goja.Value {
	s := c.s
	ro := options.Replace()
	if optBool(s.optDoc(a, 2), "upsert") {
		ro.SetUpsert(true)
	}
	r, err := c.mc().ReplaceOne(s.ctx, s.docArg(a, 0, "filter"), s.docArg(a, 1, "replacement"), ro)
	s.check(err)
	return s.updateResult(r)
}

func (c *collObj) jsDeleteOne(a goja.FunctionCall) goja.Value {
	return c.s.deleteResult(c.mc().DeleteOne(c.s.ctx, c.s.docArg(a, 0, "filter")))
}

func (c *collObj) jsDeleteMany(a goja.FunctionCall) goja.Value {
	return c.s.deleteResult(c.mc().DeleteMany(c.s.ctx, c.s.docArg(a, 0, "filter")))
}

func (c *collObj) jsRemove(a goja.FunctionCall) goja.Value {
	justOne := a.Argument(1).ToBoolean()
	if o, ok := a.Argument(1).(*goja.Object); ok {
		justOne = o.Get("justOne") != nil && o.Get("justOne").ToBoolean()
	}
	if justOne {
		return c.jsDeleteOne(a)
	}
	return c.jsDeleteMany(a)
}

// findOneAndOpts is what findOneAndUpdate, findOneAndReplace and findOneAndDelete read from their options.
type findOneAndOpts struct {
	o                bson.D
	after            bool
	sort, projection any
	hasSort, hasProj bool
}

func readFindOneAnd(o bson.D) findOneAndOpts {
	f := findOneAndOpts{o: o, after: optBool(o, "returnNewDocument")}
	if v, ok := optVal(o, "returnDocument"); ok {
		f.after = v == "after"
	}
	f.sort, f.hasSort = optVal(o, "sort")
	f.projection, f.hasProj = optVal(o, "projection")
	return f
}

// oneResult is the document a findOneAnd… returns, or null.
func (s *Shell) oneResult(res *mongo.SingleResult) goja.Value {
	var d bson.D
	if err := res.Decode(&d); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return goja.Null()
		}
		s.throw(err)
	}
	return s.toJS(d)
}

func (c *collObj) jsFindOneAndUpdate(a goja.FunctionCall) goja.Value {
	s := c.s
	f := readFindOneAnd(s.optDoc(a, 2))
	fo := options.FindOneAndUpdate()
	if f.after {
		fo.SetReturnDocument(options.After)
	}
	if optBool(f.o, "upsert") {
		fo.SetUpsert(true)
	}
	if f.hasSort {
		fo.SetSort(f.sort)
	}
	if f.hasProj {
		fo.SetProjection(f.projection)
	}
	if v, ok := optVal(f.o, "arrayFilters"); ok {
		if arr, ok := v.(bson.A); ok {
			fo.SetArrayFilters([]any(arr))
		}
	}
	return s.oneResult(c.mc().FindOneAndUpdate(s.ctx, s.docArg(a, 0, "filter"), c.updateArg(a), fo))
}

func (c *collObj) jsFindOneAndReplace(a goja.FunctionCall) goja.Value {
	s := c.s
	f := readFindOneAnd(s.optDoc(a, 2))
	fo := options.FindOneAndReplace()
	if f.after {
		fo.SetReturnDocument(options.After)
	}
	if optBool(f.o, "upsert") {
		fo.SetUpsert(true)
	}
	if f.hasSort {
		fo.SetSort(f.sort)
	}
	if f.hasProj {
		fo.SetProjection(f.projection)
	}
	return s.oneResult(c.mc().FindOneAndReplace(s.ctx, s.docArg(a, 0, "filter"), s.docArg(a, 1, "replacement"), fo))
}

func (c *collObj) jsFindOneAndDelete(a goja.FunctionCall) goja.Value {
	s := c.s
	f := readFindOneAnd(s.optDoc(a, 1))
	fo := options.FindOneAndDelete()
	if f.hasSort {
		fo.SetSort(f.sort)
	}
	if f.hasProj {
		fo.SetProjection(f.projection)
	}
	return s.oneResult(c.mc().FindOneAndDelete(s.ctx, s.docArg(a, 0, "filter"), fo))
}

func (c *collObj) jsCountDocuments(a goja.FunctionCall) goja.Value {
	s := c.s
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
	return s.vm.ToValue(n)
}

func (c *collObj) jsEstimatedDocumentCount(goja.FunctionCall) goja.Value {
	n, err := c.mc().EstimatedDocumentCount(c.s.ctx)
	c.s.check(err)
	return c.s.vm.ToValue(n)
}

func (c *collObj) jsCount(a goja.FunctionCall) goja.Value {
	s := c.s
	f := s.docArg(a, 0, "filter")
	var n int64
	var err error
	if len(f) == 0 {
		n, err = c.mc().EstimatedDocumentCount(s.ctx)
	} else {
		n, err = c.mc().CountDocuments(s.ctx, f)
	}
	s.check(err)
	return s.vm.ToValue(n)
}

func (c *collObj) jsDistinct(a goja.FunctionCall) goja.Value {
	r := c.mc().Distinct(c.s.ctx, a.Argument(0).String(), c.s.docArg(a, 1, "filter"))
	var vals bson.A
	c.s.check(r.Decode(&vals))
	return c.s.toJS(vals)
}

func (c *collObj) jsCreateIndex(a goja.FunctionCall) goja.Value {
	spec := append(bson.D{{Key: "key", Value: c.s.docArg(a, 0, "keys")}}, c.s.optDoc(a, 1)...)
	names := c.createIndexes([]bson.D{spec})
	return c.s.vm.ToValue(names[0])
}

func (c *collObj) jsCreateIndexes(a goja.FunctionCall) goja.Value {
	s := c.s
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
	return s.vm.NewArray(items...)
}

func (c *collObj) jsGetIndexes(goja.FunctionCall) goja.Value {
	ix, err := c.s.c.Indexes(c.s.ctx, c.db, c.name)
	c.s.check(err)
	a := make(bson.A, len(ix))
	for i, x := range ix {
		v, _ := FromVal(x.Spec)
		a[i] = v
	}
	return c.s.toJS(a)
}

func (c *collObj) jsDropIndex(a goja.FunctionCall) goja.Value {
	return c.command(bson.D{{Key: "dropIndexes", Value: c.name}, {Key: "index", Value: c.s.arg(a, 0)}})
}

func (c *collObj) jsDropIndexes(a goja.FunctionCall) goja.Value {
	ix := c.s.arg(a, 0)
	if ix == nil {
		ix = "*"
	}
	return c.command(bson.D{{Key: "dropIndexes", Value: c.name}, {Key: "index", Value: ix}})
}

func (c *collObj) jsDrop(goja.FunctionCall) goja.Value {
	c.s.check(c.mc().Drop(c.s.ctx))
	return c.s.vm.ToValue(true)
}

func (c *collObj) jsRenameCollection(a goja.FunctionCall) goja.Value {
	r, err := c.s.c.RunCommand(c.s.ctx, "admin", bson.D{{Key: "renameCollection", Value: c.db + "." + c.name},
		{Key: "to", Value: c.db + "." + a.Argument(0).String()}, {Key: "dropTarget", Value: a.Argument(1).ToBoolean()}})
	c.s.check(err)
	return c.s.toJS(r)
}

func (c *collObj) jsStats(goja.FunctionCall) goja.Value {
	return c.command(bson.D{{Key: "collStats", Value: c.name}})
}

func (c *collObj) jsRunCommand(a goja.FunctionCall) goja.Value {
	return c.command(append(bson.D{{Key: a.Argument(0).String(), Value: c.name}}, c.s.optDoc(a, 1)...))
}

func (c *collObj) jsExists(goja.FunctionCall) goja.Value {
	s := c.s
	cur, err := s.c.cl.Database(c.db).ListCollections(s.ctx, bson.D{{Key: "name", Value: c.name}})
	s.check(err)
	var docs []bson.D
	s.check(cur.All(s.ctx, &docs))
	if len(docs) == 0 {
		return goja.Null()
	}
	return s.toJS(docs[0])
}

// updateSetter is what updateOne and updateMany options have in common.
type updateSetter[B any] interface {
	SetUpsert(bool) B
	SetArrayFilters([]any) B
	SetCollation(*options.Collation) B
	SetHint(any) B
}

// updateOptions applies the shell's update options to u.
func updateOptions[B updateSetter[B]](u B, o bson.D) B {
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

// aggregate returns the cursor of an aggregation. A pipeline ending in $out or $merge
// runs at once, as in mongosh, whether or not the cursor is ever read.
func (s *Shell) aggregate(c *cursor) goja.Value {
	if c.writes() {
		cur, err := c.open(0, -1)
		s.check(err)
		if cur != nil {
			for cur.Next(s.ctx) {
			}
			err := cur.Err()
			cur.Close(s.ctx)
			s.check(err)
		}
	}
	return s.vm.NewDynamicObject(c)
}

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
	self    goja.Value // the cursor as a JavaScript object, for chaining without a this
	// written: an aggregate ending in $out or $merge has already run.
	written bool
}

func (c *cursor) close() {
	if c.live != nil {
		c.live.Close(context.Background())
		c.live = nil
	}
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
	fn := func(f func(a goja.FunctionCall) goja.Value) goja.Value { return c.s.vm.ToValue(f) }
	return map[string]goja.Value{
		// On an aggregation these add pipeline stages, as the Node.js driver does.
		"sort":            c.chain(c.jsSort),
		"limit":           c.chain(c.jsLimit),
		"skip":            c.chain(c.jsSkip),
		"projection":      c.chain(c.jsProject),
		"project":         c.chain(c.jsProject),
		"hint":            c.setOpt("hint"),
		"collation":       c.setOpt("collation"),
		"comment":         c.setOpt("comment"),
		"batchSize":       c.setOpt("batchSize"),
		"maxTimeMS":       c.setOpt("maxTimeMS"),
		"max":             c.setOpt("max"),
		"min":             c.setOpt("min"),
		"returnKey":       c.setOpt("returnKey"),
		"allowDiskUse":    c.chain(c.jsAllowDiskUse),
		"noCursorTimeout": c.chain(func(goja.FunctionCall) { c.opts = append(c.opts, bson.E{Key: "noCursorTimeout", Value: true}) }),
		"showRecordId":    c.chain(func(goja.FunctionCall) { c.opts = append(c.opts, bson.E{Key: "showRecordId", Value: true}) }),
		"readPref":        c.chain(func(goja.FunctionCall) {}),
		"pretty":          c.chain(func(goja.FunctionCall) {}),
		"map":             c.chain(c.jsMap),
		"toArray":         fn(c.jsToArray),
		"forEach":         fn(c.jsForEach),
		"hasNext":         fn(func(goja.FunctionCall) goja.Value { return c.s.vm.ToValue(c.hasNext()) }),
		"next":            fn(c.jsNext),
		"tryNext":         fn(c.jsTryNext),
		"isExhausted":     fn(func(goja.FunctionCall) goja.Value { return c.s.vm.ToValue(!c.hasNext()) }),
		"itcount":         fn(c.jsItcount),
		"count":           fn(c.jsCount),
		"size":            fn(c.jsSize),
		"explain":         fn(c.jsExplain),
		"close":           fn(c.jsClose),
		"toString":        fn(func(goja.FunctionCall) goja.Value { return c.s.vm.ToValue("Cursor(" + c.ns() + ")") }),
	}
}

// chain makes a method that changes the cursor before it's read and returns
// the object it was called on, so calls chain on the same cursor.
func (c *cursor) chain(f func(a goja.FunctionCall)) goja.Value {
	return c.s.vm.ToValue(func(a goja.FunctionCall) goja.Value {
		if c.live != nil {
			c.s.throw(fmt.Errorf("the cursor has already been iterated"))
		}
		f(a)
		if o, ok := a.This.(*goja.Object); ok {
			return o
		}
		if c.self == nil {
			c.self = c.s.vm.NewDynamicObject(c)
		}
		return c.self
	})
}

// setOpt makes a chaining method that passes its argument on as option k.
func (c *cursor) setOpt(k string) goja.Value {
	return c.chain(func(a goja.FunctionCall) { c.opts = append(c.opts, bson.E{Key: k, Value: c.s.arg(a, 0)}) })
}

// addStage appends a stage to an aggregation; false for a find cursor.
func (c *cursor) addStage(name string, v any) bool {
	if !c.agg {
		return false
	}
	if c.writes() {
		c.s.throw(fmt.Errorf("%s can't follow a pipeline ending in $out or $merge", name))
	}
	c.pipeline = append(c.pipeline, bson.D{{Key: name, Value: v}})
	return true
}

func (c *cursor) jsSort(a goja.FunctionCall) {
	c.sort = c.s.docArg(a, 0, "sort")
	c.addStage("$sort", c.sort)
}

func (c *cursor) jsLimit(a goja.FunctionCall) {
	n := a.Argument(0).ToInteger()
	if n < 0 {
		n = -n
	}
	if !c.addStage("$limit", n) {
		c.limit = n
	}
}

func (c *cursor) jsSkip(a goja.FunctionCall) {
	if n := a.Argument(0).ToInteger(); !c.addStage("$skip", n) {
		c.skip = n
	}
}

func (c *cursor) jsProject(a goja.FunctionCall) {
	if p := c.s.docArg(a, 0, "projection"); !c.addStage("$project", p) {
		c.projection = p
	}
}

func (c *cursor) jsAllowDiskUse(a goja.FunctionCall) {
	c.opts = append(c.opts, bson.E{Key: "allowDiskUse", Value: goja.IsUndefined(a.Argument(0)) || a.Argument(0).ToBoolean()})
}

func (c *cursor) jsMap(a goja.FunctionCall) {
	f, ok := goja.AssertFunction(a.Argument(0))
	if !ok {
		c.s.throw(fmt.Errorf("map needs a function"))
	}
	c.transform = append(c.transform, f)
}

func (c *cursor) jsToArray(goja.FunctionCall) goja.Value {
	var items []any
	for {
		d, ok := c.next()
		if !ok {
			return c.s.vm.NewArray(items...)
		}
		items = append(items, c.applyJS(d))
	}
}

func (c *cursor) jsForEach(a goja.FunctionCall) goja.Value {
	f, ok := goja.AssertFunction(a.Argument(0))
	if !ok {
		c.s.throw(fmt.Errorf("forEach needs a function"))
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
}

func (c *cursor) jsNext(goja.FunctionCall) goja.Value {
	d, ok := c.next()
	if !ok {
		c.s.throw(fmt.Errorf("cursor is exhausted"))
	}
	return c.applyJS(d)
}

func (c *cursor) jsTryNext(goja.FunctionCall) goja.Value {
	d, ok := c.next()
	if !ok {
		return goja.Null()
	}
	return c.applyJS(d)
}

func (c *cursor) jsItcount(goja.FunctionCall) goja.Value {
	n := 0
	for {
		if _, ok := c.next(); !ok {
			return c.s.vm.ToValue(n)
		}
		n++
	}
}

func (c *cursor) jsCount(goja.FunctionCall) goja.Value {
	s := c.s
	if c.agg {
		s.throw(fmt.Errorf("count is not available on an aggregation cursor; use itcount()"))
	}
	n, err := s.c.cl.Database(c.db).Collection(c.coll).CountDocuments(s.ctx, c.filter)
	s.check(err)
	return s.vm.ToValue(n)
}

func (c *cursor) jsSize(goja.FunctionCall) goja.Value {
	s := c.s
	o := options.Count()
	if c.skip > 0 {
		o.SetSkip(c.skip)
	}
	if c.limit > 0 {
		o.SetLimit(c.limit)
	}
	n, err := s.c.cl.Database(c.db).Collection(c.coll).CountDocuments(s.ctx, c.filter, o)
	s.check(err)
	return s.vm.ToValue(n)
}

func (c *cursor) jsExplain(a goja.FunctionCall) goja.Value {
	s := c.s
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
}

func (c *cursor) jsClose(goja.FunctionCall) goja.Value {
	if c.live != nil {
		c.live.Close(c.s.ctx)
	}
	c.done = true
	return goja.Undefined()
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
