package design

// Built-in types offered by the designer. Connected designers add the server's
// user-defined types; offline data models use these lists alone.

// MySQLTypes are MySQL's built-in column types.
var MySQLTypes = []string{
	"tinyint", "smallint", "mediumint", "int", "bigint", "decimal", "float", "double", "bit", "boolean",
	"char", "varchar", "tinytext", "text", "mediumtext", "longtext", "binary", "varbinary",
	"tinyblob", "blob", "mediumblob", "longblob", "enum", "set",
	"date", "time", "datetime", "timestamp", "year", "json",
	"geometry", "point", "linestring", "polygon", "multipoint", "multilinestring", "multipolygon", "geometrycollection",
}

// PostgresTypes are common PostgreSQL built-in column types.
var PostgresTypes = []string{
	"smallint", "integer", "bigint", "numeric", "real", "double precision", "smallserial", "serial", "bigserial", "money",
	"varchar", "char", "text", "citext", "bytea", "boolean",
	"date", "time", "timetz", "timestamp", "timestamptz", "interval",
	"uuid", "json", "jsonb", "xml", "inet", "cidr", "macaddr", "bit", "varbit", "tsvector", "tsquery",
	"point", "line", "lseg", "box", "path", "polygon", "circle",
	"int4range", "int8range", "numrange", "tsrange", "tstzrange", "daterange",
	"integer[]", "bigint[]", "text[]", "varchar[]", "uuid[]", "jsonb[]",
}

// SQLiteTypes are the type names SQLite tools conventionally offer; SQLite
// accepts any name and maps it to an affinity (STRICT tables allow only the first six).
var SQLiteTypes = []string{
	"INTEGER", "REAL", "TEXT", "BLOB", "ANY", "INT",
	"NUMERIC", "BOOLEAN", "DATE", "DATETIME", "TIMESTAMP", "TIME", "VARCHAR", "CHAR", "DECIMAL", "DOUBLE", "FLOAT", "BIGINT", "JSON",
}

// OfflineMeta is designer metadata for a dialect without a server to ask: built-in
// types and the usual charsets, collations, engines and index methods.
func OfflineMeta(dialect string) Meta {
	if dialect == "sqlite" {
		return Meta{Types: append([]string(nil), SQLiteTypes...),
			Collations: []CollationInfo{{Name: "BINARY", Default: true}, {Name: "NOCASE"}, {Name: "RTRIM"}}}
	}
	if dialect == "postgres" {
		return Meta{
			Types:        append([]string(nil), PostgresTypes...),
			Collations:   []CollationInfo{{Name: "C"}, {Name: "POSIX"}, {Name: "default"}},
			IndexMethods: []string{"BTREE", "HASH", "GIN", "GIST", "SPGIST", "BRIN"},
		}
	}
	m := Meta{
		Types:            append([]string(nil), MySQLTypes...),
		Charsets:         []string{"ascii", "binary", "latin1", "utf8mb3", "utf8mb4"},
		Engines:          []string{"ARCHIVE", "CSV", "InnoDB", "MEMORY", "MyISAM"},
		DefaultEngine:    "InnoDB",
		DefaultCharset:   "utf8mb4",
		DefaultCollation: "utf8mb4_0900_ai_ci",
		IndexMethods:     []string{"BTREE", "HASH"},
	}
	for _, c := range []CollationInfo{
		{Name: "ascii_bin", Charset: "ascii"}, {Name: "ascii_general_ci", Charset: "ascii", Default: true},
		{Name: "binary", Charset: "binary", Default: true},
		{Name: "latin1_bin", Charset: "latin1"}, {Name: "latin1_general_ci", Charset: "latin1"}, {Name: "latin1_swedish_ci", Charset: "latin1", Default: true},
		{Name: "utf8mb3_bin", Charset: "utf8mb3"}, {Name: "utf8mb3_general_ci", Charset: "utf8mb3", Default: true}, {Name: "utf8mb3_unicode_ci", Charset: "utf8mb3"},
		{Name: "utf8mb4_0900_ai_ci", Charset: "utf8mb4", Default: true}, {Name: "utf8mb4_0900_as_cs", Charset: "utf8mb4"}, {Name: "utf8mb4_bin", Charset: "utf8mb4"},
		{Name: "utf8mb4_general_ci", Charset: "utf8mb4"}, {Name: "utf8mb4_unicode_ci", Charset: "utf8mb4"}, {Name: "utf8mb4_unicode_520_ci", Charset: "utf8mb4"},
	} {
		m.Collations = append(m.Collations, c)
	}
	return m
}
