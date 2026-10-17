package core

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"chloe/internal/datamodel"
	"chloe/internal/driver"
	"chloe/internal/model"
)

// Structure Synchronization compares the tables and views of two databases or
// schemas of the same engine and builds the script that makes the target match.

type StructureOptions struct {
	CompareTables bool `json:"compareTables"`
	CompareViews  bool `json:"compareViews"`
	// DropCascade drops PostgreSQL tables and views with CASCADE.
	DropCascade bool `json:"dropCascade"`
}

type StructureCompareRequest struct {
	Source  Endpoint         `json:"source"`
	Target  Endpoint         `json:"target"`
	Options StructureOptions `json:"options"`
}

// StructItem is one object's comparison result.
type StructItem struct {
	Kind string `json:"kind"` // table, view
	Name string `json:"name"`
	// Op is create (only in the source), modify (different), drop (only in the target) or same.
	Op string `json:"op"`
	// Pre runs before any table changes (dropping a view that is replaced or
	// removed); Statements is the object's own change; Post runs once every new
	// table exists (foreign keys of new tables).
	Pre        []string `json:"pre,omitempty"`
	Statements []string `json:"statements"`
	Post       []string `json:"post,omitempty"`
	SourceDDL  string   `json:"sourceDdl,omitempty"`
	TargetDDL  string   `json:"targetDdl,omitempty"`
	Warnings   []string `json:"warnings,omitempty"`
}

type StructureComparison struct {
	Items []StructItem `json:"items"`
}

// CompareStructure diffs the source's structure against the target's.
func (m *Manager) CompareStructure(ctx context.Context, req StructureCompareRequest) (StructureComparison, error) {
	src, err := m.conn(req.Source.ConnID)
	if err != nil {
		return StructureComparison{}, fmt.Errorf("source: %w", err)
	}
	dst, err := m.conn(req.Target.ConnID)
	if err != nil {
		return StructureComparison{}, fmt.Errorf("target: %w", err)
	}
	d := dst.Dialect()
	if src.Dialect().Name != d.Name {
		return StructureComparison{}, fmt.Errorf("structure synchronization needs two databases of the same engine (%s and %s); use Data Transfer between engines", src.Dialect().Name, d.Name)
	}
	if d.Name == "sqlite" {
		return StructureComparison{}, errors.New("structure synchronization isn't available for SQLite")
	}
	if d.HasSchemas {
		if req.Source.Schema == "" {
			req.Source.Schema = d.DefaultSchema
		}
		if req.Target.Schema == "" {
			req.Target.Schema = d.DefaultSchema
		}
	}
	if req.Source == req.Target {
		return StructureComparison{}, errors.New("the source and the target are the same")
	}
	var out StructureComparison
	if req.Options.CompareTables {
		items, err := m.compareTables(ctx, src, dst, req)
		if err != nil {
			return out, err
		}
		out.Items = append(out.Items, items...)
	}
	if req.Options.CompareViews {
		items, err := m.compareViews(ctx, src, dst, req)
		if err != nil {
			return out, err
		}
		out.Items = append(out.Items, items...)
	}
	return out, nil
}

var nextvalSchemaRe = regexp.MustCompile(`^(nextval\(')((?:[^']|'')+)('.*)$`)

func (m *Manager) compareTables(ctx context.Context, src, dst driver.Conn, req StructureCompareRequest) ([]StructItem, error) {
	mdl, err := m.ReverseModel(ctx, req.Source.ConnID, req.Source.Database, req.Source.Schema, nil)
	if err != nil {
		return nil, fmt.Errorf("source: %w", err)
	}
	// References into the source database/schema point at the target's.
	for i := range mdl.Tables {
		t := &mdl.Tables[i]
		for j := range t.ForeignKeys {
			f := &t.ForeignKeys[j]
			if f.RefDatabase == req.Source.Database {
				f.RefDatabase = ""
			}
			if f.RefSchema == req.Source.Schema {
				f.RefSchema = req.Target.Schema
			}
		}
		if req.Source.Schema != "" && req.Source.Schema != req.Target.Schema {
			for j := range t.Columns {
				c := &t.Columns[j]
				if c.Default == nil {
					continue
				}
				if mm := nextvalSchemaRe.FindStringSubmatch(*c.Default); mm != nil && strings.HasPrefix(mm[2], req.Source.Schema+".") {
					v := mm[1] + req.Target.Schema + "." + strings.TrimPrefix(mm[2], req.Source.Schema+".") + mm[3]
					c.Default = &v
				}
			}
		}
	}
	srcTables := map[string]bool{}
	for _, t := range mdl.Tables {
		srcTables[strings.ToLower(t.Name)] = true
	}
	objs, err := dst.ListObjects(ctx, req.Target.Database, req.Target.Schema, model.KindTable)
	if err != nil {
		return nil, fmt.Errorf("target: %w", err)
	}
	names := make([]string, len(objs))
	for i, o := range objs {
		names[i] = o.Name
	}
	existing, err := loadDesigns(ctx, dst, req.Target.Database, req.Target.Schema, names)
	if err != nil {
		return nil, fmt.Errorf("target %w", err)
	}
	diffs, err := mdl.Compare(existing, datamodel.Target{Database: req.Target.Database, Schema: req.Target.Schema})
	if err != nil {
		return nil, err
	}
	d := dst.Dialect()
	items := make([]StructItem, 0, len(diffs))
	for _, df := range diffs {
		it := StructItem{Kind: "table", Name: df.Name, Op: df.Op, Statements: df.Statements, Post: df.Post, Warnings: df.Warnings}
		if df.Op != "drop" {
			it.SourceDDL, _ = src.DDL(ctx, tableRef(req.Source.Database, req.Source.Schema, df.Name))
		}
		if df.Op != "create" {
			it.TargetDDL, _ = dst.DDL(ctx, tableRef(req.Target.Database, req.Target.Schema, df.Name))
		}
		if df.Op == "drop" && d.Name == "postgres" && req.Options.DropCascade {
			it.Statements = []string{it.Statements[0] + " CASCADE"}
		}
		items = append(items, it)
	}
	return items, nil
}

func (m *Manager) compareViews(ctx context.Context, src, dst driver.Conn, req StructureCompareRequest) ([]StructItem, error) {
	d := dst.Dialect()
	list := func(c driver.Conn, e Endpoint) (map[string]string, error) {
		objs, err := c.ListObjects(ctx, e.Database, e.Schema, model.KindView)
		if err != nil {
			return nil, err
		}
		out := map[string]string{}
		for _, o := range objs {
			ddl, err := c.DDL(ctx, model.ObjectRef{Database: e.Database, Schema: e.Schema, Name: o.Name, Kind: model.KindView})
			if err != nil {
				return nil, fmt.Errorf("%s: %w", o.Name, err)
			}
			out[o.Name] = ddl
		}
		return out, nil
	}
	srcViews, err := list(src, req.Source)
	if err != nil {
		return nil, fmt.Errorf("source: %w", err)
	}
	dstViews, err := list(dst, req.Target)
	if err != nil {
		return nil, fmt.Errorf("target: %w", err)
	}
	srcQ, dstQ := req.Source.Database, req.Target.Database
	if d.HasSchemas {
		srcQ, dstQ = req.Source.Schema, req.Target.Schema
	}
	ref := func(name string) string {
		return d.Qualified(model.ObjectRef{Database: req.Target.Database, Schema: req.Target.Schema, Name: name, Kind: model.KindView})
	}
	drop := func(name string) string {
		q := "DROP VIEW IF EXISTS " + ref(name)
		if d.Name == "postgres" && req.Options.DropCascade {
			q += " CASCADE"
		}
		return q
	}
	names := make([]string, 0, len(srcViews))
	for n := range srcViews {
		names = append(names, n)
	}
	sort.Strings(names)
	var items []StructItem
	for _, n := range names {
		want := retargetDDL(d, srcViews[n], srcQ, dstQ)
		it := StructItem{Kind: "view", Name: n, SourceDDL: srcViews[n]}
		cur, ok := dstViews[n]
		switch {
		case !ok:
			it.Op, it.Statements = "create", []string{want}
		case normalizeDDL(d, cur, dstQ) == normalizeDDL(d, want, dstQ):
			it.Op, it.TargetDDL = "same", cur
		default:
			it.Op, it.TargetDDL = "modify", cur
			it.Pre, it.Statements = []string{drop(n)}, []string{want}
		}
		delete(dstViews, n)
		items = append(items, it)
	}
	rest := make([]string, 0, len(dstViews))
	for n := range dstViews {
		rest = append(rest, n)
	}
	sort.Strings(rest)
	for _, n := range rest {
		items = append(items, StructItem{Kind: "view", Name: n, Op: "drop", TargetDDL: dstViews[n], Pre: []string{drop(n)}})
	}
	return items, nil
}

var (
	ddlSpace   = regexp.MustCompile(`\s+`)
	mysqlAlgo  = regexp.MustCompile(`(?i)\s+ALGORITHM\s*=\s*\w+`)
	mysqlSecur = regexp.MustCompile(`(?i)\s+SQL\s+SECURITY\s+\w+`)
)

// retargetDDL rewrites an object's DDL from the source database/schema to the
// target's, without the MySQL definer.
func retargetDDL(d driver.Dialect, ddl, src, dst string) string {
	ddl = viewDefinerRe.ReplaceAllString(ddl, "")
	if src == "" || dst == "" || src == dst {
		return ddl
	}
	ddl = strings.ReplaceAll(ddl, d.QuoteIdent(src)+".", d.QuoteIdent(dst)+".")
	if d.HasSchemas {
		// PostgreSQL leaves names that need no quotes unquoted.
		ddl = regexp.MustCompile(`(^|[^\w"])`+regexp.QuoteMeta(src)+`\.`).ReplaceAllString(ddl, "${1}"+d.QuoteIdent(dst)+".")
	}
	return ddl
}

// normalizeDDL makes two views' DDL comparable: qualifiers, definer, algorithm and spacing removed.
func normalizeDDL(d driver.Dialect, ddl, qualifier string) string {
	ddl = viewDefinerRe.ReplaceAllString(ddl, "")
	ddl = mysqlAlgo.ReplaceAllString(ddl, "")
	ddl = mysqlSecur.ReplaceAllString(ddl, "")
	if qualifier != "" {
		ddl = strings.ReplaceAll(ddl, d.QuoteIdent(qualifier)+".", "")
		ddl = regexp.MustCompile(`(^|[^\w"])`+regexp.QuoteMeta(qualifier)+`\.`).ReplaceAllString(ddl, "$1")
	}
	ddl = strings.TrimSuffix(strings.TrimSpace(ddl), ";")
	return strings.ToLower(ddlSpace.ReplaceAllString(ddl, " "))
}

// StructureScript orders the chosen items' statements into one deployment script:
// view drops, new tables, changed tables, foreign keys of new tables, views, then
// dropped tables.
func StructureScript(items []StructItem) []string {
	var out []string
	add := func(pick func(StructItem) []string) {
		for _, it := range items {
			out = append(out, pick(it)...)
		}
	}
	add(func(it StructItem) []string { return it.Pre })
	add(func(it StructItem) []string {
		if it.Kind == "table" && it.Op == "create" {
			return it.Statements
		}
		return nil
	})
	add(func(it StructItem) []string {
		if it.Kind == "table" && it.Op == "modify" {
			return it.Statements
		}
		return nil
	})
	add(func(it StructItem) []string { return it.Post })
	add(func(it StructItem) []string {
		if it.Kind == "view" {
			return it.Statements
		}
		return nil
	})
	add(func(it StructItem) []string {
		if it.Kind == "table" && it.Op == "drop" {
			return it.Statements
		}
		return nil
	})
	return out
}

// DeployRequest runs a deployment script on a target.
type DeployRequest struct {
	Target          Endpoint `json:"target"`
	Statements      []string `json:"statements"`
	ContinueOnError bool     `json:"continueOnError"`
	Title           string   `json:"title,omitempty"`
}

// Deploy runs a script statement by statement as a job, logging each one.
func (m *Manager) Deploy(req DeployRequest) (Job, error) {
	c, err := m.conn(req.Target.ConnID)
	if err != nil {
		return Job{}, err
	}
	if len(req.Statements) == 0 {
		return Job{}, errors.New("nothing to deploy")
	}
	title := req.Title
	if title == "" {
		title = "Deploy to " + endpointName(m, req.Target)
	}
	return m.startJob("deploy", title, "statements", func(ctx context.Context, p *progress) (string, error) {
		ds, err := c.NewSession(ctx, req.Target.Database, req.Target.Schema)
		if err != nil {
			return "", err
		}
		defer ds.Close()
		p.set(0, int64(len(req.Statements)))
		ok, failed := 0, 0
		for i, st := range req.Statements {
			if ctx.Err() != nil {
				return "", ctx.Err()
			}
			first := strings.SplitN(strings.TrimSpace(st), "\n", 2)[0]
			if _, err := ds.Exec(ctx, st); err != nil {
				failed++
				p.error("[SYNC] %d> %s: %v", i+1, first, err)
				if !req.ContinueOnError {
					return "", fmt.Errorf("statement %d failed: %w", i+1, err)
				}
			} else {
				ok++
				p.logf("[SYNC] %d> %s", i+1, first)
			}
			p.setDone(int64(i + 1))
		}
		msg := fmt.Sprintf("Finished deployment (Total: %d, Success: %d, Fail: %d)", len(req.Statements), ok, failed)
		p.logf("[SYNC] %s", msg)
		return msg, nil
	}), nil
}
