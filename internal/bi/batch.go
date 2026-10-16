package bi

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"chloe/internal/driver"
)

// A dashboard draws many charts on one data source at once, and each chart's
// query runs the source's SQL inside it. Charts with the same filters can share
// one read of the source instead: the source (with its custom fields and those
// filters) becomes a CTE that's read once, and each chart is grouped from it in
// a branch of one UNION ALL. The database still does the grouping, sorting and
// comparing, so results follow its collation, as they do one chart at a time.

// BatchCTE is the name the shared read goes by in a batch.
const BatchCTE = "bi_source"

// BatchPart is where one chart's rows are in a batch's result: the rows whose
// first cell is its index, and Width cells from Offset (after that first cell).
type BatchPart struct {
	Offset, Width int
}

// Batchable reports whether charts on a source may share one read of it: a
// query or joined tables (reading a plain table again costs no more than the
// copy would) on MySQL or PostgreSQL.
func Batchable(d driver.Dialect, src DataSource) bool {
	return (src.SQL != "" || len(src.Joins) > 0) && (d.Name == "mysql" || d.Name == "postgres")
}

// BatchQuery reads several charts on one data source in one statement. The
// charts must have the same filters (they go into the shared read once).
func BatchQuery(d driver.Dialect, from Source, src DataSource, charts []Chart, limits []int) (Stmt, []BatchPart, error) {
	if len(charts) < 2 || len(limits) != len(charts) {
		return Stmt{}, nil, errors.New("a batch reads two charts or more")
	}
	for _, c := range charts[1:] {
		if !slices.EqualFunc(c.Filters, charts[0].Filters, sameFilter) {
			return Stmt{}, nil, errors.New("charts in a batch have the same filters")
		}
	}
	layered, err := withCustom(d, from, src)
	if err != nil {
		return Stmt{}, nil, err
	}
	b, err := newQB(d, src, layered.Args)
	if err != nil {
		return Stmt{}, nil, err
	}
	w, err := b.where(src.allFields(), append(slices.Clone(src.Filters), charts[0].Filters...))
	if err != nil {
		return Stmt{}, nil, err
	}
	shared := "WITH " + d.QuoteIdent(BatchCTE) + " AS (SELECT * FROM " + layered.SQL + " AS s" + w + ")"
	args := b.args

	// Branches read the shared rows: custom fields are columns of it now, and
	// the filters are applied.
	plain := src
	plain.Fields, plain.CustomFields, plain.Filters = src.allFields(), nil, nil
	type branch struct {
		sql   string
		width int
		group []int // positions of the grouped columns, from 0
	}
	var branches []branch
	for i, c := range charts {
		c.Filters = nil
		q, cols, group, err := chartQuery(d, Source{SQL: d.QuoteIdent(BatchCTE), Args: args}, plain, c, limits[i])
		if err != nil {
			return Stmt{}, nil, err
		}
		sql := q.SQL
		if d.Name == "mysql" {
			// Read the shared rows once, not merged into each branch.
			sql = "SELECT /*+ NO_MERGE(s) */ " + strings.TrimPrefix(sql, "SELECT ")
		}
		branches = append(branches, branch{sql: sql, width: len(cols), group: group})
		args = q.Args
	}

	parts := make([]BatchPart, len(branches))
	width := 0
	for i, br := range branches {
		parts[i] = BatchPart{Offset: width, Width: br.width}
		width += br.width
	}
	cell := func(i, j int) string { return "b" + strconv.Itoa(i) + "." + d.QuoteIdent("c"+strconv.Itoa(j)) }
	var sel []string
	if d.Name == "postgres" {
		// PostgreSQL types a UNION's columns pair by pair, and two NULLs make text,
		// so a first branch that never runs gives every column its type.
		cols := []string{"0 AS p"}
		var from []string
		for i, br := range branches {
			for j := 0; j < br.width; j++ {
				cols = append(cols, cell(i, j)+" AS v"+strconv.Itoa(parts[i].Offset+j))
			}
			from = append(from, "("+br.sql+") AS b"+strconv.Itoa(i))
		}
		sel = append(sel, "SELECT "+strings.Join(cols, ", ")+" FROM "+strings.Join(from, ", ")+" WHERE false")
	}
	for i, br := range branches {
		cols := []string{strconv.Itoa(i)}
		if i == 0 && d.Name != "postgres" {
			cols[0] += " AS p"
		}
		for k := 0; k < width; k++ {
			v := "NULL"
			if k >= parts[i].Offset && k < parts[i].Offset+br.width {
				v = cell(i, k-parts[i].Offset)
			}
			if i == 0 && d.Name != "postgres" {
				v += " AS v" + strconv.Itoa(k)
			}
			cols = append(cols, v)
		}
		sel = append(sel, "SELECT "+strings.Join(cols, ", ")+" FROM ("+br.sql+") AS b"+strconv.Itoa(i))
	}
	// Each chart's rows in its own order: its part first, then its grouped columns
	// (the other charts' columns are NULL there).
	order := []string{"1"}
	for i, br := range branches {
		for _, j := range br.group {
			order = append(order, strconv.Itoa(2+parts[i].Offset+j))
		}
	}
	// PostgreSQL's typing branch repeats each chart's SQL with the same $n, so
	// the arguments aren't repeated.
	sql := shared + " " + strings.Join(sel, " UNION ALL ") + " ORDER BY " + strings.Join(order, ", ")
	return Stmt{SQL: sql, Args: args}, parts, nil
}

func sameFilter(a, b Filter) bool {
	return a.Field == b.Field && a.Op == b.Op && a.Negate == b.Negate && a.Disabled == b.Disabled && slices.Equal(a.Values, b.Values)
}

// SplitBatch takes one chart's rows out of a batch's result.
func SplitBatch(rows [][]*string, part int, p BatchPart) ([][]*string, error) {
	want := strconv.Itoa(part)
	out := [][]*string{}
	for _, r := range rows {
		if len(r) < 1+p.Offset+p.Width || r[0] == nil {
			return nil, fmt.Errorf("a batch row has %d cells", len(r))
		}
		if *r[0] == want {
			out = append(out, r[1+p.Offset:1+p.Offset+p.Width])
		}
	}
	return out, nil
}
