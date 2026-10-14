package mysql

import (
	"context"
	"fmt"

	"chloe/internal/design"
	"chloe/internal/driver/sqlcommon"
)

func (c *Conn) LoadDatabase(ctx context.Context, name string) (design.Database, error) {
	rows, err := sqlcommon.QueryStrings(ctx, c.db, "SELECT DEFAULT_CHARACTER_SET_NAME, DEFAULT_COLLATION_NAME FROM information_schema.SCHEMATA WHERE SCHEMA_NAME = ?", name)
	if err != nil {
		return design.Database{}, err
	}
	if len(rows) == 0 {
		return design.Database{}, fmt.Errorf("database %s not found", name)
	}
	return design.Database{Name: name, Charset: rows[0][0].String, Collation: rows[0][1].String}, nil
}

func (c *Conn) DatabaseMeta(ctx context.Context) (design.DatabaseMeta, error) {
	var m design.DatabaseMeta
	cs, err := sqlcommon.QueryStrings(ctx, c.db, "SELECT CHARACTER_SET_NAME FROM information_schema.CHARACTER_SETS ORDER BY 1")
	if err != nil {
		return m, err
	}
	for _, r := range cs {
		m.Charsets = append(m.Charsets, r[0].String)
	}
	if co, err := sqlcommon.QueryStrings(ctx, c.db, "SELECT COLLATION_NAME, CHARACTER_SET_NAME, IS_DEFAULT FROM information_schema.COLLATIONS WHERE CHARACTER_SET_NAME IS NOT NULL ORDER BY 2, 1"); err == nil {
		for _, r := range co {
			m.Collations = append(m.Collations, design.CollationInfo{Name: r[0].String, Charset: r[1].String, Default: r[2].String == "Yes"})
		}
	}
	if d, err := sqlcommon.QueryStrings(ctx, c.db, "SELECT @@character_set_server, @@collation_server"); err == nil && len(d) > 0 {
		m.DefaultCharset, m.DefaultCollation = d[0][0].String, d[0][1].String
	}
	return m, nil
}
