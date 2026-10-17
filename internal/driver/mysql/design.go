package mysql

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"strings"

	"chloe/internal/design"
	"chloe/internal/driver/sqlcommon"
	"chloe/internal/model"
	"chloe/internal/sqlq"
)

func (c *Conn) DesignGenerator() design.Generator { return design.MySQL{} }

var (
	typeMods  = regexp.MustCompile(`^([a-z ]+?)(?:\((.*)\))?((?: unsigned| zerofill)*)$`)
	createOpt = regexp.MustCompile(`(?i)row_format=(\w+)`)
	autoIncRe = regexp.MustCompile(`\) ENGINE=.* AUTO_INCREMENT=(\d+)`)
)

// LoadDesign reads a table's full definition for the designer.
func (c *Conn) LoadDesign(ctx context.Context, ref model.ObjectRef) (design.Table, error) {
	t := design.Table{Database: ref.Database, Name: ref.Name, OrigName: ref.Name}
	recs, err := sqlcommon.QueryStrings(ctx, c.db, `SELECT ENGINE, TABLE_COLLATION, AUTO_INCREMENT, CREATE_OPTIONS, TABLE_COMMENT,
		(SELECT CHARACTER_SET_NAME FROM information_schema.COLLATIONS WHERE COLLATION_NAME = TABLE_COLLATION)
		FROM information_schema.TABLES WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ?`, ref.Database, ref.Name)
	if err != nil {
		return t, err
	}
	if len(recs) == 0 {
		return t, errNotFound(ref)
	}
	r := recs[0]
	t.Options = design.Options{Engine: r[0].String, Collation: r[1].String, AutoIncrement: r[2].String, Charset: r[5].String}
	if m := createOpt.FindStringSubmatch(r[3].String); m != nil {
		t.Options.RowFormat = strings.ToUpper(m[1])
	}
	t.Comment = r[4].String
	// information_schema.TABLES.AUTO_INCREMENT is cached in MySQL 8 (up to a day);
	// SHOW CREATE TABLE always has the live counter.
	t.Options.AutoIncrement = ""
	if sc, err := sqlcommon.QueryStrings(ctx, c.db, "SHOW CREATE TABLE "+dialect.Qualified(ref)); err == nil && len(sc) > 0 {
		if m := autoIncRe.FindStringSubmatch(sc[0][1].String); m != nil {
			t.Options.AutoIncrement = m[1]
		}
	}

	cols, err := sqlcommon.QueryStrings(ctx, c.db, `SELECT COLUMN_NAME, COLUMN_TYPE, DATA_TYPE, IS_NULLABLE, COLUMN_DEFAULT, EXTRA,
		CHARACTER_SET_NAME, COLLATION_NAME, COLUMN_COMMENT, GENERATION_EXPRESSION
		FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ? ORDER BY ORDINAL_POSITION`, ref.Database, ref.Name)
	if err != nil {
		return t, err
	}
	for _, r := range cols {
		col := design.Column{Name: r[0].String, OrigName: r[0].String, Nullable: r[3].String == "YES", Comment: r[8].String}
		parseColumnType(&col, r[1].String, r[2].String)
		extra := strings.ToLower(r[5].String)
		col.AutoIncrement = strings.Contains(extra, "auto_increment")
		col.OnUpdateNow = strings.Contains(extra, "on update current_timestamp")
		if strings.Contains(extra, "generated") && r[9].String != "" {
			col.GeneratedExpr = r[9].String
			col.GeneratedStored = strings.Contains(extra, "stored")
		} else {
			col.Default = c.defaultExpr(r[4], col, extra)
		}
		// Charset/collation are only kept when they differ from the table default, so
		// changing the table's collation doesn't pin every column to the old one.
		if r[7].Valid && r[7].String != t.Options.Collation {
			col.Collation = r[7].String
			if r[6].String != t.Options.Charset {
				col.Charset = r[6].String
			}
		}
		t.Columns = append(t.Columns, col)
	}

	idx, err := sqlcommon.QueryStrings(ctx, c.db, `SELECT INDEX_NAME, NON_UNIQUE, INDEX_TYPE, COLUMN_NAME, SUB_PART, COLLATION,
		INDEX_COMMENT, `+c.optionalCols()+`
		FROM information_schema.STATISTICS WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ? ORDER BY INDEX_NAME = 'PRIMARY' DESC, INDEX_NAME, SEQ_IN_INDEX`, ref.Database, ref.Name)
	if err != nil {
		return t, err
	}
	for _, r := range idx {
		name := r[0].String
		ic := design.IndexColumn{Name: r[3].String, Length: r[4].String, Desc: r[5].String == "D", Expr: r[8].String}
		if name == "PRIMARY" {
			if t.PrimaryKey == nil {
				t.PrimaryKey = &design.PrimaryKey{}
			}
			t.PrimaryKey.Columns = append(t.PrimaryKey.Columns, ic.Name)
			continue
		}
		n := len(t.Indexes)
		if n == 0 || t.Indexes[n-1].Name != name {
			ix := design.Index{Name: name, OrigName: name, Comment: r[6].String, Invisible: r[7].String == "NO"}
			switch strings.ToUpper(r[2].String) {
			case "FULLTEXT":
				ix.Kind = "fulltext"
			case "SPATIAL":
				ix.Kind = "spatial"
			case "HASH":
				ix.Method = "HASH"
			}
			if ix.Kind == "" && r[1].String == "0" {
				ix.Kind = "unique"
			}
			t.Indexes = append(t.Indexes, ix)
			n++
		}
		t.Indexes[n-1].Columns = append(t.Indexes[n-1].Columns, ic)
	}

	fks, err := c.foreignKeys(ctx, ref.Database, ref.Name)
	if err != nil {
		return t, err
	}
	for _, f := range fks {
		fk := design.ForeignKey{Name: f.name, OrigName: f.name, Columns: f.cols, RefTable: f.refTable, RefColumns: f.refCols,
			OnDelete: f.onDelete, OnUpdate: f.onUpdate}
		if f.refDB != ref.Database {
			fk.RefDatabase = f.refDB
		}
		t.ForeignKeys = append(t.ForeignKeys, fk)
	}

	// CHECK constraints exist from MySQL 8.0.16 / MariaDB 10.2; older servers have none.
	if checks, err := sqlcommon.QueryStrings(ctx, c.db, c.checksQuery(), ref.Database, ref.Name); err == nil {
		for _, r := range checks {
			t.Checks = append(t.Checks, design.Check{Name: r[0].String, OrigName: r[0].String, Expr: stripParens(r[1].String), NotEnforced: r[2].String == "NO"})
		}
	}

	trs, err := sqlcommon.QueryStrings(ctx, c.db, `SELECT TRIGGER_NAME, ACTION_TIMING, EVENT_MANIPULATION, ACTION_STATEMENT
		FROM information_schema.TRIGGERS WHERE EVENT_OBJECT_SCHEMA = ? AND EVENT_OBJECT_TABLE = ? ORDER BY ACTION_ORDER`, ref.Database, ref.Name)
	if err != nil {
		return t, err
	}
	for _, r := range trs {
		t.Triggers = append(t.Triggers, design.Trigger{Name: r[0].String, OrigName: r[0].String, Timing: r[1].String,
			Events: []string{r[2].String}, Body: r[3].String})
	}
	return t, nil
}

func errNotFound(ref model.ObjectRef) error {
	return fmt.Errorf("table %s.%s not found", ref.Database, ref.Name)
}

// optionalCols selects index visibility and expression where the server has them.
func (c *Conn) optionalCols() string {
	if c.mariadb {
		return "'YES', NULL"
	}
	if c.atLeast(8, 0, 13) {
		return "IS_VISIBLE, EXPRESSION"
	}
	if c.atLeast(8, 0, 0) {
		return "IS_VISIBLE, NULL"
	}
	return "'YES', NULL"
}

func (c *Conn) checksQuery() string {
	if c.mariadb {
		return `SELECT CONSTRAINT_NAME, CHECK_CLAUSE, 'YES' FROM information_schema.CHECK_CONSTRAINTS
			WHERE CONSTRAINT_SCHEMA = ? AND TABLE_NAME = ? AND LEVEL = 'Table' ORDER BY CONSTRAINT_NAME`
	}
	return `SELECT tc.CONSTRAINT_NAME, cc.CHECK_CLAUSE, tc.ENFORCED FROM information_schema.TABLE_CONSTRAINTS tc
		JOIN information_schema.CHECK_CONSTRAINTS cc ON cc.CONSTRAINT_SCHEMA = tc.CONSTRAINT_SCHEMA AND cc.CONSTRAINT_NAME = tc.CONSTRAINT_NAME
		WHERE tc.TABLE_SCHEMA = ? AND tc.TABLE_NAME = ? AND tc.CONSTRAINT_TYPE = 'CHECK' ORDER BY tc.CONSTRAINT_NAME`
}

// parseColumnType splits COLUMN_TYPE ("decimal(10,2) unsigned zerofill", "enum('a','b')").
func parseColumnType(col *design.Column, columnType, dataType string) {
	col.Type = dataType
	m := typeMods.FindStringSubmatch(strings.ToLower(columnType))
	if m == nil {
		return
	}
	col.Type = m[1]
	args := m[2]
	switch col.Type {
	case "enum", "set":
		// Keep the original casing of the value list.
		if i, j := strings.Index(columnType, "("), strings.LastIndex(columnType, ")"); i >= 0 && j > i {
			col.Length = columnType[i+1 : j]
		}
	default:
		if a, b, ok := strings.Cut(args, ","); ok {
			col.Length, col.Scale = a, b
		} else {
			col.Length = args
		}
	}
	col.Unsigned = strings.Contains(m[3], "unsigned")
	col.Zerofill = strings.Contains(m[3], "zerofill")
	// Display widths of integer types are deprecated in MySQL 8 and only shown for zerofill.
	if strings.HasSuffix(col.Type, "int") && !col.Zerofill && col.Type != "tinyint" {
		col.Length = ""
	}
}

// defaultExpr turns COLUMN_DEFAULT into SQL that can follow DEFAULT.
func (c *Conn) defaultExpr(v sql.NullString, col design.Column, extra string) *string {
	if !v.Valid {
		return nil // no default (or DEFAULT NULL, which is equivalent)
	}
	s := v.String
	if c.mariadb {
		// MariaDB already returns SQL: quoted literals, NULL, or expressions.
		if s == "NULL" {
			return nil
		}
		return &s
	}
	upper := strings.ToUpper(s)
	switch {
	case strings.Contains(extra, "default_generated"):
		// Expression default (8.0.13+). CURRENT_TIMESTAMP variants are allowed bare.
		if strings.HasPrefix(upper, "CURRENT_TIMESTAMP") || strings.HasPrefix(upper, "NOW(") {
			return &s
		}
		e := "(" + s + ")"
		return &e
	case isNumericType(col.Type) || strings.HasPrefix(s, "b'"):
		return &s
	case strings.HasPrefix(upper, "CURRENT_TIMESTAMP"):
		return &s
	}
	q := "'" + strings.NewReplacer(`\`, `\\`, `'`, `''`).Replace(s) + "'"
	return &q
}

func isNumericType(t string) bool {
	switch t {
	case "tinyint", "smallint", "mediumint", "int", "integer", "bigint", "decimal", "numeric", "float", "double", "real", "bit", "year":
		return true
	}
	return false
}

func stripParens(s string) string {
	s = strings.TrimSpace(s)
	for strings.HasPrefix(s, "(") && strings.HasSuffix(s, ")") && sqlq.Balanced(s[1:len(s)-1]) {
		s = strings.TrimSpace(s[1 : len(s)-1])
	}
	return s
}

// atLeast compares the server version (ignoring MariaDB, which versions separately).
func (c *Conn) atLeast(maj, min, patch int) bool {
	var a, b, p int
	parseVersion(c.version, &a, &b, &p)
	if a != maj {
		return a > maj
	}
	if b != min {
		return b > min
	}
	return p >= patch
}

func parseVersion(v string, a, b, p *int) {
	parts := strings.SplitN(strings.SplitN(v, "-", 2)[0], ".", 3)
	dst := []*int{a, b, p}
	for i, part := range parts {
		n := 0
		for _, r := range part {
			if r < '0' || r > '9' {
				break
			}
			n = n*10 + int(r-'0')
		}
		*dst[i] = n
	}
}

// DesignMeta lists the types, charsets, collations and engines for the designer.
func (c *Conn) DesignMeta(ctx context.Context, database string) (design.Meta, error) {
	m := design.Meta{Types: design.MySQLTypes, IndexMethods: []string{"BTREE", "HASH"}}
	if cs, err := sqlcommon.QueryStrings(ctx, c.db, "SELECT CHARACTER_SET_NAME FROM information_schema.CHARACTER_SETS ORDER BY 1"); err == nil {
		for _, r := range cs {
			m.Charsets = append(m.Charsets, r[0].String)
		}
	}
	if co, err := sqlcommon.QueryStrings(ctx, c.db, "SELECT COLLATION_NAME, CHARACTER_SET_NAME, IS_DEFAULT FROM information_schema.COLLATIONS WHERE CHARACTER_SET_NAME IS NOT NULL ORDER BY 2, 1"); err == nil {
		for _, r := range co {
			m.Collations = append(m.Collations, design.CollationInfo{Name: r[0].String, Charset: r[1].String, Default: r[2].String == "Yes"})
		}
	}
	if en, err := sqlcommon.QueryStrings(ctx, c.db, "SELECT ENGINE, SUPPORT FROM information_schema.ENGINES WHERE SUPPORT IN ('YES','DEFAULT') ORDER BY 1"); err == nil {
		for _, r := range en {
			m.Engines = append(m.Engines, r[0].String)
			if r[1].String == "DEFAULT" {
				m.DefaultEngine = r[0].String
			}
		}
	}
	if d, err := sqlcommon.QueryStrings(ctx, c.db, "SELECT DEFAULT_CHARACTER_SET_NAME, DEFAULT_COLLATION_NAME FROM information_schema.SCHEMATA WHERE SCHEMA_NAME = ?", database); err == nil && len(d) > 0 {
		m.DefaultCharset, m.DefaultCollation = d[0][0].String, d[0][1].String
	}
	return m, nil
}
