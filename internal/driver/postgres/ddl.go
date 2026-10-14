package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"chloe/internal/driver/sqlcommon"
	"chloe/internal/model"
)

// DDL reconstructs CREATE statements. PostgreSQL has no SHOW CREATE TABLE, so table
// DDL is assembled from the catalog using the server's own pg_get_* deparsers.
func (c *Conn) DDL(ctx context.Context, ref model.ObjectRef) (string, error) {
	db, err := c.pool(ctx, ref.Database)
	if err != nil {
		return "", err
	}
	schema := ref.Schema
	if schema == "" {
		schema = "public"
	}
	one := func(q string, args ...any) (string, error) {
		recs, err := sqlcommon.QueryStrings(ctx, db, q, args...)
		if err != nil {
			return "", err
		}
		if len(recs) == 0 {
			return "", errors.New("object not found")
		}
		var parts []string
		for _, r := range recs {
			parts = append(parts, r[0].String)
		}
		return strings.Join(parts, "\n\n"), nil
	}
	qn := dialect.Qualified(model.ObjectRef{Schema: schema, Name: ref.Name})

	switch ref.Kind {
	case model.KindTable:
		return c.tableDDL(ctx, ref, schema)
	case model.KindView, model.KindMatView:
		def, err := one(`SELECT pg_get_viewdef(c.oid, true) FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
			WHERE n.nspname = $1 AND c.relname = $2`, schema, ref.Name)
		if err != nil {
			return "", err
		}
		kw := "VIEW"
		if ref.Kind == model.KindMatView {
			kw = "MATERIALIZED VIEW"
		}
		return fmt.Sprintf("CREATE OR REPLACE %s %s AS\n%s", kw, qn, def), nil
	case model.KindFunction, model.KindProcedure:
		q := `SELECT pg_get_functiondef(p.oid) || ';' FROM pg_proc p JOIN pg_namespace n ON n.oid = p.pronamespace
			WHERE n.nspname = $1 AND p.proname = $2`
		// Overloads share a name; the identity signature picks one.
		q += ` AND ($3 = '' OR pg_get_function_identity_arguments(p.oid) = $3)`
		return one(q, schema, ref.Name, ref.Signature)
	case model.KindTrigger:
		return one(`SELECT pg_get_triggerdef(t.oid, true) || ';' FROM pg_trigger t JOIN pg_class c ON c.oid = t.tgrelid
			JOIN pg_namespace n ON n.oid = c.relnamespace WHERE n.nspname = $1 AND t.tgname = $2`, schema, ref.Name)
	case model.KindSequence:
		return one(`SELECT format('CREATE SEQUENCE %s AS %s INCREMENT BY %s MINVALUE %s MAXVALUE %s START WITH %s CACHE %s%s;',
			$3::text, data_type, increment_by, min_value, max_value, start_value, cache_size, CASE WHEN cycle THEN ' CYCLE' ELSE '' END)
			FROM pg_sequences WHERE schemaname = $1 AND sequencename = $2`, schema, ref.Name, qn)
	}
	return "", fmt.Errorf("DDL not supported for %s", ref.Kind)
}

func (c *Conn) tableDDL(ctx context.Context, ref model.ObjectRef, schema string) (string, error) {
	db, err := c.pool(ctx, ref.Database)
	if err != nil {
		return "", err
	}
	td, err := c.TableDetail(ctx, ref)
	if err != nil {
		return "", err
	}
	if len(td.Columns) == 0 {
		return "", errors.New("table not found")
	}
	qn := dialect.Qualified(model.ObjectRef{Schema: schema, Name: ref.Name})
	var b strings.Builder
	fmt.Fprintf(&b, "CREATE TABLE %s (\n", qn)
	var lines []string
	for _, col := range td.Columns {
		l := "  " + dialect.QuoteIdent(col.Name) + " " + col.Type
		if col.Default != nil {
			l += " DEFAULT " + *col.Default
		}
		if !col.Nullable {
			l += " NOT NULL"
		}
		lines = append(lines, l)
	}
	cons, err := sqlcommon.QueryStrings(ctx, db, `SELECT con.conname, pg_get_constraintdef(con.oid, true)
		FROM pg_constraint con JOIN pg_class c ON c.oid = con.conrelid JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = $1 AND c.relname = $2 AND con.contype IN ('p','u','f','c','x')
		ORDER BY CASE con.contype WHEN 'p' THEN 0 WHEN 'u' THEN 1 WHEN 'c' THEN 2 WHEN 'f' THEN 3 ELSE 4 END, con.conname`, schema, ref.Name)
	if err != nil {
		return "", err
	}
	for _, r := range cons {
		lines = append(lines, "  CONSTRAINT "+dialect.QuoteIdent(r[0].String)+" "+r[1].String)
	}
	b.WriteString(strings.Join(lines, ",\n"))
	b.WriteString("\n)")
	part, _ := sqlcommon.QueryStrings(ctx, db, `SELECT pg_get_partkeydef(c.oid) FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = $1 AND c.relname = $2 AND c.relkind = 'p'`, schema, ref.Name)
	if len(part) > 0 && part[0][0].Valid {
		b.WriteString(" PARTITION BY " + part[0][0].String)
	}
	b.WriteString(";\n")

	idx, err := sqlcommon.QueryStrings(ctx, db, `SELECT pg_get_indexdef(i.indexrelid) FROM pg_index i
		JOIN pg_class c ON c.oid = i.indrelid JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = $1 AND c.relname = $2
		  AND NOT EXISTS (SELECT 1 FROM pg_constraint con WHERE con.conindid = i.indexrelid AND con.contype IN ('p','u','x'))
		ORDER BY 1`, schema, ref.Name)
	if err != nil {
		return "", err
	}
	for _, r := range idx {
		b.WriteString("\n" + r[0].String + ";")
	}

	comments, _ := sqlcommon.QueryStrings(ctx, db, `SELECT a.attname, col_description(c.oid, a.attnum), obj_description(c.oid, 'pg_class')
		FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
		LEFT JOIN pg_attribute a ON a.attrelid = c.oid AND a.attnum > 0 AND NOT a.attisdropped
		WHERE n.nspname = $1 AND c.relname = $2 ORDER BY a.attnum`, schema, ref.Name)
	if len(comments) > 0 && comments[0][2].Valid {
		fmt.Fprintf(&b, "\n\nCOMMENT ON TABLE %s IS %s;", qn, quoteLiteral(comments[0][2].String))
	}
	for _, r := range comments {
		if r[1].Valid {
			fmt.Fprintf(&b, "\nCOMMENT ON COLUMN %s.%s IS %s;", qn, dialect.QuoteIdent(r[0].String), quoteLiteral(r[1].String))
		}
	}
	return strings.TrimRight(b.String(), "\n") + "\n", nil
}

func quoteLiteral(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }
