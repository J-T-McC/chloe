package postgres

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"chloe/internal/design"
	"chloe/internal/driver/sqlcommon"
	"chloe/internal/model"
	"chloe/internal/sqlq"
)

func (c *Conn) DesignGenerator() design.Generator { return design.Postgres{} }

// Long SQL type names that take modifiers are shown by their short aliases so the
// modifier can live in the Length column: "timestamp(3) with time zone" -> timestamptz, 3.
var typeAliases = map[string]string{
	"character varying":           "varchar",
	"character":                   "char",
	"bit varying":                 "varbit",
	"timestamp without time zone": "timestamp",
	"timestamp with time zone":    "timestamptz",
	"time without time zone":      "time",
	"time with time zone":         "timetz",
}

var modifierRe = regexp.MustCompile(`^(.*?)\(([^)]*)\)(.*)$`)

// splitType turns format_type output into type, length and scale.
func splitType(full string) (typ, length, scale string) {
	arr := ""
	for strings.HasSuffix(full, "[]") {
		full, arr = full[:len(full)-2], arr+"[]"
	}
	if m := modifierRe.FindStringSubmatch(full); m != nil {
		full = strings.TrimSpace(m[1] + m[3])
		if a, b, ok := strings.Cut(m[2], ","); ok {
			length, scale = a, b
		} else {
			length = m[2]
		}
	}
	if a, ok := typeAliases[full]; ok {
		full = a
	}
	return full + arr, length, scale
}

func (c *Conn) LoadDesign(ctx context.Context, ref model.ObjectRef) (design.Table, error) {
	schema := ref.Schema
	if schema == "" {
		schema = "public"
	}
	t := design.Table{Database: ref.Database, Schema: schema, Name: ref.Name, OrigName: ref.Name}
	db, err := c.pool(ctx, ref.Database)
	if err != nil {
		return t, err
	}
	rel, err := sqlcommon.QueryStrings(ctx, db, `SELECT c.oid, COALESCE(obj_description(c.oid, 'pg_class'), ''), c.relpersistence = 'u',
		COALESCE((SELECT spcname FROM pg_tablespace WHERE oid = c.reltablespace), '')
		FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace WHERE n.nspname = $1 AND c.relname = $2`, schema, ref.Name)
	if err != nil {
		return t, err
	}
	if len(rel) == 0 {
		return t, fmt.Errorf("table %s.%s not found", schema, ref.Name)
	}
	oid := rel[0][0].String
	t.Comment = rel[0][1].String
	t.Options.Unlogged = rel[0][2].String == "true"
	t.Options.Tablespace = rel[0][3].String

	cols, err := sqlcommon.QueryStrings(ctx, db, `SELECT a.attname, format_type(a.atttypid, a.atttypmod), NOT a.attnotnull,
		CASE WHEN a.attgenerated = '' THEN pg_get_expr(d.adbin, d.adrelid) END,
		CASE WHEN a.attgenerated <> '' THEN pg_get_expr(d.adbin, d.adrelid) END, a.attgenerated,
		a.attidentity, CASE WHEN a.attcollation <> t.typcollation THEN co.collname END,
		COALESCE(col_description(a.attrelid, a.attnum), ''),
		s.seqstart, s.seqincrement, s.seqmin, s.seqmax, s.seqcache, s.seqcycle
		FROM pg_attribute a
		JOIN pg_type t ON t.oid = a.atttypid
		LEFT JOIN pg_attrdef d ON d.adrelid = a.attrelid AND d.adnum = a.attnum
		LEFT JOIN pg_collation co ON co.oid = a.attcollation
		LEFT JOIN pg_sequence s ON a.attidentity <> '' AND s.seqrelid = pg_get_serial_sequence(a.attrelid::regclass::text, a.attname)::regclass
		WHERE a.attrelid = $1 AND a.attnum > 0 AND NOT a.attisdropped ORDER BY a.attnum`, oid)
	if err != nil {
		return t, err
	}
	for _, r := range cols {
		col := design.Column{Name: r[0].String, OrigName: r[0].String, Nullable: r[2].String == "true", Comment: r[8].String, Collation: r[7].String}
		col.Type, col.Length, col.Scale = splitType(r[1].String)
		if r[3].Valid {
			v := r[3].String
			col.Default = &v
		}
		if r[4].Valid {
			col.GeneratedExpr = r[4].String
			col.GeneratedStored = r[5].String == "s"
		}
		switch r[6].String {
		case "a":
			col.Identity = "always"
		case "d":
			col.Identity = "by default"
		}
		if col.Identity != "" && r[9].Valid {
			col.IdentityOpts = &design.IdentityOpts{Start: r[9].String, Increment: r[10].String, Min: r[11].String, Max: r[12].String,
				Cache: r[13].String, Cycle: r[14].String == "true"}
		}
		t.Columns = append(t.Columns, col)
	}

	cons, err := sqlcommon.QueryStrings(ctx, db, `SELECT con.conname, con.contype, pg_get_constraintdef(con.oid, true),
		array_to_string(ARRAY(SELECT a.attname FROM unnest(con.conkey) WITH ORDINALITY k(n, o) JOIN pg_attribute a ON a.attrelid = con.conrelid AND a.attnum = k.n ORDER BY k.o), E'\x1f'),
		COALESCE(rn.nspname, ''), COALESCE(rc.relname, ''),
		array_to_string(ARRAY(SELECT a.attname FROM unnest(con.confkey) WITH ORDINALITY k(n, o) JOIN pg_attribute a ON a.attrelid = con.confrelid AND a.attnum = k.n ORDER BY k.o), E'\x1f'),
		con.confupdtype, con.confdeltype
		FROM pg_constraint con
		LEFT JOIN pg_class rc ON rc.oid = con.confrelid LEFT JOIN pg_namespace rn ON rn.oid = rc.relnamespace
		WHERE con.conrelid = $1 AND con.contype IN ('p','u','c','f') ORDER BY con.contype, con.conname`, oid)
	if err != nil {
		return t, err
	}
	split := func(s string) []string {
		if s == "" {
			return nil
		}
		return strings.Split(s, "\x1f")
	}
	for _, r := range cons {
		name, cols := r[0].String, split(r[3].String)
		switch r[1].String {
		case "p":
			t.PrimaryKey = &design.PrimaryKey{Name: name, Columns: cols}
		case "u":
			t.Uniques = append(t.Uniques, design.Unique{Name: name, OrigName: name, Columns: cols})
		case "c":
			t.Checks = append(t.Checks, design.Check{Name: name, OrigName: name, Expr: checkExpr(r[2].String)})
		case "f":
			fk := design.ForeignKey{Name: name, OrigName: name, Columns: cols, RefTable: r[5].String, RefColumns: split(r[6].String),
				OnUpdate: fkAction[r[7].String], OnDelete: fkAction[r[8].String]}
			if r[4].String != schema {
				fk.RefSchema = r[4].String
			}
			t.ForeignKeys = append(t.ForeignKeys, fk)
		}
	}

	idx, err := sqlcommon.QueryStrings(ctx, db, `SELECT ic.relname, i.indisunique, am.amname, COALESCE(pg_get_expr(i.indpred, i.indrelid, true), ''),
		COALESCE(obj_description(ic.oid, 'pg_class'), ''),
		array_to_string(ARRAY(SELECT pg_get_indexdef(i.indexrelid, k + 1, true) || E'\x1e' || (i.indkey[k] = 0)::text || E'\x1e' || ((i.indoption[k] & 1) = 1)::text
			FROM generate_series(0, i.indnkeyatts - 1) k ORDER BY k), E'\x1f')
		FROM pg_index i JOIN pg_class ic ON ic.oid = i.indexrelid JOIN pg_am am ON am.oid = ic.relam
		WHERE i.indrelid = $1
		  AND NOT EXISTS (SELECT 1 FROM pg_constraint con WHERE con.conindid = i.indexrelid AND con.contype IN ('p','u','x'))
		ORDER BY ic.relname`, oid)
	if err != nil {
		return t, err
	}
	for _, r := range idx {
		ix := design.Index{Name: r[0].String, OrigName: r[0].String, Where: r[3].String, Comment: r[4].String}
		if r[1].String == "true" {
			ix.Kind = "unique"
		}
		if m := strings.ToUpper(r[2].String); m != "BTREE" {
			ix.Method = m
		}
		for _, part := range split(r[5].String) {
			f := strings.Split(part, "\x1e")
			ic := design.IndexColumn{Desc: f[2] == "true"}
			if f[1] == "true" {
				ic.Expr = f[0] // pg_get_indexdef gives the bare expression, e.g. lower(sku)
			} else {
				ic.Name = strings.Trim(f[0], `"`)
			}
			ix.Columns = append(ix.Columns, ic)
		}
		t.Indexes = append(t.Indexes, ix)
	}

	trs, err := sqlcommon.QueryStrings(ctx, db, `SELECT tgname, tgtype, pg_get_triggerdef(oid, true) FROM pg_trigger
		WHERE tgrelid = $1 AND NOT tgisinternal ORDER BY tgname`, oid)
	if err != nil {
		return t, err
	}
	for _, r := range trs {
		t.Triggers = append(t.Triggers, parseTrigger(r[0].String, r[1].String, r[2].String))
	}
	return t, nil
}

// checkExpr unwraps pg_get_constraintdef's "CHECK ((expr))".
func checkExpr(def string) string {
	s := strings.TrimSpace(strings.TrimPrefix(def, "CHECK "))
	s = strings.TrimSuffix(s, " NOT VALID")
	for strings.HasPrefix(s, "(") && strings.HasSuffix(s, ")") && sqlq.Balanced(s[1:len(s)-1]) {
		s = s[1 : len(s)-1]
	}
	return s
}

var (
	whenRe = regexp.MustCompile(`(?s) WHEN \((.*)\) EXECUTE (?:FUNCTION|PROCEDURE) `)
	execRe = regexp.MustCompile(`(?s) EXECUTE (?:FUNCTION|PROCEDURE) (.*)$`)
)

// parseTrigger decodes pg_trigger.tgtype bits and the trigger definition.
func parseTrigger(name, tgtype, def string) design.Trigger {
	n, _ := strconv.Atoi(tgtype)
	tr := design.Trigger{Name: name, OrigName: name, ForEachRow: n&1 != 0, Timing: "AFTER"}
	switch {
	case n&2 != 0:
		tr.Timing = "BEFORE"
	case n&64 != 0:
		tr.Timing = "INSTEAD OF"
	}
	for bit, ev := range map[int]string{4: "INSERT", 8: "DELETE", 16: "UPDATE", 32: "TRUNCATE"} {
		if n&bit != 0 {
			tr.Events = append(tr.Events, ev)
		}
	}
	// Stable event order for comparisons.
	order := map[string]int{"INSERT": 0, "UPDATE": 1, "DELETE": 2, "TRUNCATE": 3}
	for i := 1; i < len(tr.Events); i++ {
		for j := i; j > 0 && order[tr.Events[j]] < order[tr.Events[j-1]]; j-- {
			tr.Events[j], tr.Events[j-1] = tr.Events[j-1], tr.Events[j]
		}
	}
	if m := whenRe.FindStringSubmatch(def); m != nil {
		tr.When = m[1]
	}
	if m := execRe.FindStringSubmatch(def); m != nil {
		tr.Function = m[1]
	}
	return tr
}

func (c *Conn) DesignMeta(ctx context.Context, database string) (design.Meta, error) {
	m := design.Meta{Types: append([]string(nil), design.PostgresTypes...)}
	db, err := c.pool(ctx, database)
	if err != nil {
		return m, err
	}
	// User-defined enums, domains, composites and range types.
	if ut, err := sqlcommon.QueryStrings(ctx, db, `SELECT CASE WHEN n.nspname = 'public' THEN t.typname ELSE n.nspname || '.' || t.typname END
		FROM pg_type t JOIN pg_namespace n ON n.oid = t.typnamespace
		WHERE t.typtype IN ('e','d','c','r') AND n.nspname NOT IN ('pg_catalog','information_schema') AND n.nspname NOT LIKE 'pg\_toast%'
		  AND (t.typrelid = 0 OR (SELECT relkind FROM pg_class WHERE oid = t.typrelid) = 'c') ORDER BY 1`); err == nil {
		for _, r := range ut {
			m.Types = append(m.Types, r[0].String)
		}
	}
	if co, err := sqlcommon.QueryStrings(ctx, db, `SELECT collname FROM pg_collation WHERE collencoding IN (-1, (SELECT encoding FROM pg_database WHERE datname = current_database())) ORDER BY collname`); err == nil {
		for _, r := range co {
			m.Collations = append(m.Collations, design.CollationInfo{Name: r[0].String})
		}
	}
	if am, err := sqlcommon.QueryStrings(ctx, db, `SELECT upper(amname) FROM pg_am WHERE amtype = 'i' ORDER BY amname = 'btree' DESC, amname`); err == nil {
		for _, r := range am {
			m.IndexMethods = append(m.IndexMethods, r[0].String)
		}
	}
	if fn, err := sqlcommon.QueryStrings(ctx, db, `SELECT n.nspname || '.' || p.proname FROM pg_proc p JOIN pg_namespace n ON n.oid = p.pronamespace
		WHERE p.prorettype = 'trigger'::regtype AND n.nspname NOT IN ('pg_catalog','information_schema') ORDER BY 1`); err == nil {
		for _, r := range fn {
			m.TriggerFunctions = append(m.TriggerFunctions, r[0].String)
		}
	}
	return m, nil
}
