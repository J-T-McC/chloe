package postgres

import (
	"context"
	"fmt"
	"strconv"

	"chloe/internal/design"
	"chloe/internal/driver/sqlcommon"
)

func (c *Conn) LoadDatabase(ctx context.Context, name string) (design.Database, error) {
	db, err := c.pool(ctx, "")
	if err != nil {
		return design.Database{}, err
	}
	rows, err := sqlcommon.QueryStrings(ctx, db, `SELECT pg_get_userbyid(d.datdba), pg_encoding_to_char(d.encoding), d.datcollate, d.datctype,
		t.spcname, d.datconnlimit, coalesce(shobj_description(d.oid, 'pg_database'), '')
		FROM pg_database d JOIN pg_tablespace t ON t.oid = d.dattablespace WHERE d.datname = $1`, name)
	if err != nil {
		return design.Database{}, err
	}
	if len(rows) == 0 {
		return design.Database{}, fmt.Errorf("database %s not found", name)
	}
	r := rows[0]
	d := design.Database{Name: name, Owner: r[0].String, Encoding: r[1].String, Collation: r[2].String, Ctype: r[3].String, Tablespace: r[4].String, Comment: r[6].String}
	if n, err := strconv.Atoi(r[5].String); err == nil && n >= 0 {
		d.ConnLimit = &n
	}
	return d, nil
}

// Server encodings PostgreSQL accepts for CREATE DATABASE.
var pgEncodings = []string{
	"UTF8", "SQL_ASCII", "EUC_CN", "EUC_JP", "EUC_JIS_2004", "EUC_KR", "EUC_TW", "ISO_8859_5", "ISO_8859_6", "ISO_8859_7", "ISO_8859_8",
	"KOI8R", "KOI8U", "LATIN1", "LATIN2", "LATIN3", "LATIN4", "LATIN5", "LATIN6", "LATIN7", "LATIN8", "LATIN9", "LATIN10", "MULE_INTERNAL",
	"WIN866", "WIN874", "WIN1250", "WIN1251", "WIN1252", "WIN1253", "WIN1254", "WIN1255", "WIN1256", "WIN1257", "WIN1258",
}

func (c *Conn) DatabaseMeta(ctx context.Context) (design.DatabaseMeta, error) {
	m := design.DatabaseMeta{Encodings: pgEncodings}
	db, err := c.pool(ctx, "")
	if err != nil {
		return m, err
	}
	list := func(q string) []string {
		var out []string
		if rows, err := sqlcommon.QueryStrings(ctx, db, q); err == nil {
			for _, r := range rows {
				out = append(out, r[0].String)
			}
		}
		return out
	}
	m.Owners = list(`SELECT rolname FROM pg_roles WHERE rolname !~ '^pg_' ORDER BY 1`)
	m.Templates = list(`SELECT datname FROM pg_database WHERE datistemplate OR datallowconn ORDER BY NOT datistemplate, datname`)
	m.Tablespaces = list(`SELECT spcname FROM pg_tablespace ORDER BY 1`)
	m.Locales = list(`SELECT DISTINCT collcollate FROM pg_collation WHERE collcollate IS NOT NULL AND collcollate <> '' ORDER BY 1`)
	return m, nil
}
