package design

import (
	"fmt"
	"strconv"
	"strings"
)

// Database holds the properties of a database for New / Edit Database.
type Database struct {
	Name string `json:"name"`
	// MySQL: character set and collation. PostgreSQL: Encoding plus LC_COLLATE
	// (Collation) and LC_CTYPE (Ctype), fixed once the database exists.
	Charset   string `json:"charset,omitempty"`
	Collation string `json:"collation,omitempty"`

	// PostgreSQL
	Encoding   string `json:"encoding,omitempty"`
	Ctype      string `json:"ctype,omitempty"`
	Owner      string `json:"owner,omitempty"`
	Template   string `json:"template,omitempty"`
	Tablespace string `json:"tablespace,omitempty"`
	// ConnLimit is the connection limit; -1 (or nil) means unlimited.
	ConnLimit *int   `json:"connLimit,omitempty"`
	Comment   string `json:"comment,omitempty"`
	// SQLite: the attached database file, created when Create is set.
	File   string `json:"file,omitempty"`
	Create bool   `json:"create,omitempty"`
}

// DatabaseMeta lists choices for the database dialog.
type DatabaseMeta struct {
	Charsets    []string        `json:"charsets,omitempty"`
	Collations  []CollationInfo `json:"collations"`
	Encodings   []string        `json:"encodings,omitempty"`
	Locales     []string        `json:"locales,omitempty"`
	Owners      []string        `json:"owners,omitempty"`
	Templates   []string        `json:"templates,omitempty"`
	Tablespaces []string        `json:"tablespaces,omitempty"`
	// Server defaults shown as placeholders.
	DefaultCharset   string `json:"defaultCharset,omitempty"`
	DefaultCollation string `json:"defaultCollation,omitempty"`
}

// DatabaseSQL returns the statements that create d (old nil) or change old into d.
func DatabaseSQL(dialect string, old *Database, d Database) ([]string, error) {
	if strings.TrimSpace(d.Name) == "" {
		return nil, fmt.Errorf("enter a database name")
	}
	switch dialect {
	case "postgres":
		return pgDatabaseSQL(old, d)
	case "sqlite":
		return sqliteDatabaseSQL(old, d)
	}
	return myDatabaseSQL(old, d)
}

func myDatabaseSQL(old *Database, d Database) ([]string, error) {
	opts := ""
	if d.Charset != "" {
		opts += " CHARACTER SET " + d.Charset
	}
	if d.Collation != "" {
		opts += " COLLATE " + d.Collation
	}
	if old == nil {
		return []string{"CREATE DATABASE " + myq(d.Name) + opts}, nil
	}
	if old.Name != d.Name {
		return nil, fmt.Errorf("MySQL can't rename a database")
	}
	if old.Charset == d.Charset && old.Collation == d.Collation || opts == "" {
		return nil, nil
	}
	return []string{"ALTER DATABASE " + myq(d.Name) + opts}, nil
}

func pgDatabaseSQL(old *Database, d Database) ([]string, error) {
	limit := func(p *int) int {
		if p == nil {
			return -1
		}
		return *p
	}
	if old == nil {
		var b strings.Builder
		b.WriteString("CREATE DATABASE " + pgq(d.Name))
		if d.Owner != "" {
			b.WriteString("\n  OWNER " + pgq(d.Owner))
		}
		if d.Template != "" {
			b.WriteString("\n  TEMPLATE " + pgq(d.Template))
		}
		if d.Encoding != "" {
			b.WriteString("\n  ENCODING " + pgStr(d.Encoding))
		}
		if d.Collation != "" {
			b.WriteString("\n  LC_COLLATE " + pgStr(d.Collation))
		}
		if d.Ctype != "" {
			b.WriteString("\n  LC_CTYPE " + pgStr(d.Ctype))
		}
		if d.Tablespace != "" {
			b.WriteString("\n  TABLESPACE " + pgq(d.Tablespace))
		}
		if l := limit(d.ConnLimit); l >= 0 {
			b.WriteString("\n  CONNECTION LIMIT " + strconv.Itoa(l))
		}
		out := []string{b.String()}
		if d.Comment != "" {
			out = append(out, "COMMENT ON DATABASE "+pgq(d.Name)+" IS "+pgStr(d.Comment))
		}
		return out, nil
	}
	if d.Encoding != old.Encoding || d.Collation != old.Collation || d.Ctype != old.Ctype {
		return nil, fmt.Errorf("a database's encoding, collation and character type can't be changed")
	}
	var out []string
	name := old.Name
	if d.Name != old.Name {
		out = append(out, "ALTER DATABASE "+pgq(name)+" RENAME TO "+pgq(d.Name))
		name = d.Name
	}
	if d.Owner != old.Owner && d.Owner != "" {
		out = append(out, "ALTER DATABASE "+pgq(name)+" OWNER TO "+pgq(d.Owner))
	}
	if d.Tablespace != old.Tablespace && d.Tablespace != "" {
		out = append(out, "ALTER DATABASE "+pgq(name)+" SET TABLESPACE "+pgq(d.Tablespace))
	}
	if limit(d.ConnLimit) != limit(old.ConnLimit) {
		out = append(out, "ALTER DATABASE "+pgq(name)+" CONNECTION LIMIT "+strconv.Itoa(limit(d.ConnLimit)))
	}
	if d.Comment != old.Comment {
		c := "NULL"
		if d.Comment != "" {
			c = pgStr(d.Comment)
		}
		out = append(out, "COMMENT ON DATABASE "+pgq(name)+" IS "+c)
	}
	return out, nil
}
