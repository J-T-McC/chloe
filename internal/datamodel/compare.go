package datamodel

import (
	"fmt"
	"sort"
	"strings"

	"chloe/internal/design"
)

// TableDiff is one table's result of comparing a source structure to a target:
// create (only in the source), modify (in both, different), drop (only in the
// target) or same.
type TableDiff struct {
	Name string `json:"name"`
	Op   string `json:"op"`
	// Statements make the target table match. Post holds a new table's foreign keys,
	// which run once every new table exists.
	Statements []string `json:"statements"`
	Post       []string `json:"post,omitempty"`
	Warnings   []string `json:"warnings,omitempty"`
}

// Compare diffs source tables (already placed for the target: database, schema and
// references) against the target's tables. Tables are matched by name, case-insensitively.
func (m Model) Compare(existing []design.Table, to Target) ([]TableDiff, error) {
	gen, err := m.Generator()
	if err != nil {
		return nil, err
	}
	have := map[string]design.Table{}
	for _, t := range existing {
		have[strings.ToLower(t.Name)] = t
	}
	var out []TableDiff
	for _, mt := range m.sortedTables() {
		mt = m.placed(mt, to)
		cur, ok := have[strings.ToLower(mt.Name)]
		if !ok {
			bare := mt
			bare.ForeignKeys = nil
			stmts, err := gen.Create(bare)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", mt.Name, err)
			}
			before, after := m.sequences(mt)
			fks, err := addForeignKeys(gen, mt)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", mt.Name, err)
			}
			out = append(out, TableDiff{Name: mt.Name, Op: "create", Statements: append(append(before, stmts...), after...), Post: fks})
			continue
		}
		delete(have, strings.ToLower(mt.Name))
		want := matchByName(cur, mt)
		if m.Dialect != "postgres" {
			want = keepForeignKeyIndexes(cur, want)
		}
		stmts, err := gen.Alter(cur, want)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", mt.Name, err)
		}
		d := TableDiff{Name: mt.Name, Op: "same", Statements: stmts}
		if len(stmts) > 0 {
			d.Op = "modify"
		}
		if m.Dialect == "postgres" && design.ColumnOrderChanged(cur, want) {
			d.Warnings = append(d.Warnings, "PostgreSQL can't reorder existing columns")
		}
		out = append(out, d)
	}
	names := make([]string, 0, len(have))
	for _, t := range have {
		names = append(names, t.Name)
	}
	sort.Strings(names)
	for _, n := range names {
		t := have[strings.ToLower(n)]
		q := "`" + strings.ReplaceAll(t.Name, "`", "``") + "`"
		if m.Dialect == "postgres" {
			q = `"` + strings.ReplaceAll(t.Schema, `"`, `""`) + `"."` + strings.ReplaceAll(t.Name, `"`, `""`) + `"`
		} else if to.Database != "" {
			q = "`" + strings.ReplaceAll(to.Database, "`", "``") + "`." + q
		}
		out = append(out, TableDiff{Name: t.Name, Op: "drop", Statements: []string{"DROP TABLE " + q}})
	}
	return out, nil
}
