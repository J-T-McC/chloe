package bi

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"chloe/internal/driver"
	"chloe/internal/model"
)

// Join adds another table of the same database to a table data source
// (Navicat's joined tables). Its columns come through as "table.column", so
// they never clash with the first table's.
type Join struct {
	Table string `json:"table"`
	// Type is inner, left, right or full (full on PostgreSQL only).
	Type string   `json:"type"`
	On   []JoinOn `json:"on"`
	// Columns are the joined table's columns, read when the source is saved.
	Columns []string `json:"columns,omitempty"`
}

// JoinOn matches a field of the tables before (Left: a first-table column, or
// "table.column" of an earlier join) with a column of the joined table.
type JoinOn struct {
	Left  string `json:"left"`
	Right string `json:"right"`
}

var joinTypes = map[string]string{"inner": "INNER JOIN", "left": "LEFT JOIN", "right": "RIGHT JOIN", "full": "FULL JOIN"}

func (d DataSource) checkJoins() error {
	if len(d.Joins) == 0 {
		return nil
	}
	if d.Table == "" {
		return errors.New("joins are for table data sources; a query can join in its SQL")
	}
	for _, j := range d.Joins {
		switch {
		case j.Table == "":
			return errors.New("a join needs a table")
		case joinTypes[j.Type] == "":
			return fmt.Errorf("a join is inner, left, right or full, not %q", j.Type)
		case len(j.On) == 0:
			return fmt.Errorf("the join on %s needs at least one pair of matching fields", j.Table)
		}
		for _, on := range j.On {
			if on.Left == "" || on.Right == "" {
				return fmt.Errorf("the join on %s pairs a field with a field", j.Table)
			}
		}
	}
	return nil
}

// joined builds the FROM target of a table source with joins: a subquery with
// the first table's columns as they are and each joined table's as "table.column".
func joined(d driver.Dialect, src DataSource) (string, error) {
	if d.Name == "mysql" && slices.ContainsFunc(src.Joins, func(j Join) bool { return j.Type == "full" }) {
		return "", errors.New("MySQL has no full join; use left or right")
	}
	ref := func(name string) string {
		r := model.ObjectRef{Database: src.Database, Schema: src.Schema, Name: name}
		if d.HasSchemas && r.Schema == "" {
			r.Schema = d.DefaultSchema
		}
		return d.Qualified(r)
	}
	// alias of each table, and where a "table.column" name can be found.
	aliases := map[string]string{strings.ToLower(src.Table): "t0"}
	cols := []string{"t0.*"}
	from := ref(src.Table) + " AS t0"
	left := func(name string) (string, error) {
		if t, c, ok := strings.Cut(name, "."); ok {
			if a, ok := aliases[strings.ToLower(t)]; ok && a != "t0" {
				return a + "." + d.QuoteIdent(c), nil
			}
		}
		return "t0." + d.QuoteIdent(name), nil
	}
	for i, j := range src.Joins {
		alias := fmt.Sprintf("t%d", i+1)
		if _, dup := aliases[strings.ToLower(j.Table)]; dup {
			return "", fmt.Errorf("%s is joined twice; join a table once", j.Table)
		}
		var on []string
		for _, p := range j.On {
			l, err := left(p.Left)
			if err != nil {
				return "", err
			}
			if len(j.Columns) > 0 && !slices.Contains(j.Columns, p.Right) {
				return "", fmt.Errorf("%s has no column %q", j.Table, p.Right)
			}
			on = append(on, l+" = "+alias+"."+d.QuoteIdent(p.Right))
		}
		aliases[strings.ToLower(j.Table)] = alias
		if len(j.Columns) == 0 {
			return "", fmt.Errorf("the columns of %s haven't been read; save the data source again", j.Table)
		}
		for _, c := range j.Columns {
			cols = append(cols, alias+"."+d.QuoteIdent(c)+" AS "+d.QuoteIdent(j.Table+"."+c))
		}
		from += " " + joinTypes[j.Type] + " " + ref(j.Table) + " AS " + alias + " ON " + strings.Join(on, " AND ")
	}
	return "(SELECT " + strings.Join(cols, ", ") + " FROM " + from + ")", nil
}
