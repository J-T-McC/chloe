package privs

import (
	"strconv"
	"strings"
)

func pgq(s string) string   { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }
func pgStr(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

// PostgresPrivileges lists the privileges offered at each level.
var PostgresPrivileges = map[string][]string{
	Database: {"CONNECT", "CREATE", "TEMPORARY"},
	Schema:   {"CREATE", "USAGE"},
	Table:    {"SELECT", "INSERT", "UPDATE", "DELETE", "TRUNCATE", "REFERENCES", "TRIGGER"},
	Column:   {"SELECT", "INSERT", "UPDATE", "REFERENCES"},
	Sequence: {"SELECT", "UPDATE", "USAGE"},
	Function: {"EXECUTE"},
}

func pgObject(schema, name string) string {
	if schema == "" {
		return pgq(name)
	}
	return pgq(schema) + "." + pgq(name)
}

func pgTarget(g Grant) string {
	switch g.Level {
	case Database:
		return "DATABASE " + pgq(g.Database)
	case Schema:
		return "SCHEMA " + pgq(g.Schema)
	case Sequence:
		return "SEQUENCE " + pgObject(g.Schema, g.Object)
	case Function:
		// Object is "name(arg types)": quote the name, keep the signature.
		name, args, ok := strings.Cut(g.Object, "(")
		if !ok {
			return "FUNCTION " + pgObject(g.Schema, name)
		}
		return "FUNCTION " + pgObject(g.Schema, name) + "(" + args
	default: // table, column
		return "TABLE " + pgObject(g.Schema, g.Object)
	}
}

func pgPrivList(c change) string {
	ps := make([]string, len(c.privs))
	for i, p := range c.privs {
		if c.target.Level == Column {
			ps[i] = p + " (" + pgq(c.target.Column) + ")"
		} else {
			ps[i] = p
		}
	}
	return strings.Join(ps, ", ")
}

// grantDB is where a grant has to run: database-level grants work from anywhere,
// everything else lives inside its database.
func grantDB(g Grant) string {
	if g.Level == Database {
		return ""
	}
	return g.Database
}

func pgAttrs(old *User, u User) string {
	var b strings.Builder
	flag := func(on bool, was bool, name string) {
		if old != nil && on == was {
			return
		}
		if on {
			b.WriteString(" " + name)
		} else {
			b.WriteString(" NO" + name)
		}
	}
	var o User
	if old != nil {
		o = *old
	}
	flag(u.CanLogin, o.CanLogin, "LOGIN")
	flag(u.Superuser, o.Superuser, "SUPERUSER")
	flag(u.CreateDB, o.CreateDB, "CREATEDB")
	flag(u.CreateRole, o.CreateRole, "CREATEROLE")
	flag(u.Inherit, o.Inherit, "INHERIT")
	flag(u.Replication, o.Replication, "REPLICATION")
	flag(u.BypassRLS, o.BypassRLS, "BYPASSRLS")
	if old == nil || u.ConnLimit != o.ConnLimit {
		b.WriteString(" CONNECTION LIMIT " + strconv.Itoa(u.ConnLimit))
	}
	if u.Password != nil {
		b.WriteString(" PASSWORD " + pgStr(*u.Password))
	}
	if (old == nil && u.ValidUntil != "") || (old != nil && u.ValidUntil != o.ValidUntil) {
		v := u.ValidUntil
		if v == "" {
			v = "infinity"
		}
		b.WriteString(" VALID UNTIL " + pgStr(v))
	}
	return b.String()
}

func pgSQL(old *User, u User) ([]Stmt, error) {
	var out []Stmt
	add := func(db, sql string) { out = append(out, Stmt{Database: db, SQL: sql}) }
	role := pgq(u.Name)
	if old == nil {
		add("", "CREATE ROLE "+role+" WITH"+pgAttrs(nil, u))
		if u.Comment != "" {
			add("", "COMMENT ON ROLE "+role+" IS "+pgStr(u.Comment))
		}
		for _, r := range u.MemberOf {
			add("", "GRANT "+pgq(r)+" TO "+role)
		}
		out = append(out, pgGrants(nil, u.Privileges, role)...)
		return out, nil
	}
	if old.Name != u.Name {
		add("", "ALTER ROLE "+pgq(old.Name)+" RENAME TO "+role)
	}
	if attrs := pgAttrs(old, u); attrs != "" {
		add("", "ALTER ROLE "+role+" WITH"+attrs)
	}
	if old.Comment != u.Comment {
		c := "NULL"
		if u.Comment != "" {
			c = pgStr(u.Comment)
		}
		add("", "COMMENT ON ROLE "+role+" IS "+c)
	}
	addRoles, removeRoles := membershipDiff(old.MemberOf, u.MemberOf)
	for _, r := range removeRoles {
		add("", "REVOKE "+pgq(r)+" FROM "+role)
	}
	for _, r := range addRoles {
		add("", "GRANT "+pgq(r)+" TO "+role)
	}
	out = append(out, pgGrants(old.Privileges, u.Privileges, role)...)
	return out, nil
}

func pgGrants(old, cur []Grant, role string) []Stmt {
	add, addGrantable, remove, revokeOption := grantDiff(old, cur)
	var out []Stmt
	for _, c := range revokeOption {
		out = append(out, Stmt{grantDB(c.target), "REVOKE GRANT OPTION FOR " + pgPrivList(c) + " ON " + pgTarget(c.target) + " FROM " + role})
	}
	for _, c := range remove {
		out = append(out, Stmt{grantDB(c.target), "REVOKE " + pgPrivList(c) + " ON " + pgTarget(c.target) + " FROM " + role})
	}
	for _, c := range add {
		out = append(out, Stmt{grantDB(c.target), "GRANT " + pgPrivList(c) + " ON " + pgTarget(c.target) + " TO " + role})
	}
	for _, c := range addGrantable {
		out = append(out, Stmt{grantDB(c.target), "GRANT " + pgPrivList(c) + " ON " + pgTarget(c.target) + " TO " + role + " WITH GRANT OPTION"})
	}
	return out
}
