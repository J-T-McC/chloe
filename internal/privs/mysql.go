package privs

import (
	"fmt"
	"strconv"
	"strings"

	"chloe/internal/sqlq"
)

func myq(s string) string { return sqlq.Backtick(s) }

func myStr(s string) string { return sqlq.MySQLString(s) }

// myAccount quotes 'user'@'host'.
func myAccount(name, host string) string {
	if host == "" {
		host = "%"
	}
	return myStr(name) + "@" + myStr(host)
}

// SplitAccount splits "role@host" (the last @ separates the host).
func SplitAccount(s string) (name, host string) {
	if i := strings.LastIndex(s, "@"); i > 0 {
		return s[:i], s[i+1:]
	}
	return s, "%"
}

func myTarget(g Grant) string {
	switch g.Level {
	case Global:
		return "*.*"
	case Database:
		return myq(g.Database) + ".*"
	case Function:
		kind := g.RoutineType
		if kind == "" {
			kind = "FUNCTION"
		}
		return kind + " " + myq(g.Database) + "." + myq(g.Object)
	default: // table, column
		return myq(g.Database) + "." + myq(g.Object)
	}
}

func myPrivList(c change) string {
	ps := make([]string, len(c.privs))
	for i, p := range c.privs {
		if c.target.Level == Column {
			ps[i] = p + " (" + myq(c.target.Column) + ")"
		} else {
			ps[i] = p
		}
	}
	return strings.Join(ps, ", ")
}

// MySQLPrivileges lists the privileges offered at each level.
var MySQLPrivileges = map[string][]string{
	Global: {"ALTER", "ALTER ROUTINE", "CREATE", "CREATE ROLE", "CREATE ROUTINE", "CREATE TABLESPACE", "CREATE TEMPORARY TABLES", "CREATE USER",
		"CREATE VIEW", "DELETE", "DROP", "DROP ROLE", "EVENT", "EXECUTE", "FILE", "INDEX", "INSERT", "LOCK TABLES", "PROCESS", "REFERENCES",
		"RELOAD", "REPLICATION CLIENT", "REPLICATION SLAVE", "SELECT", "SHOW DATABASES", "SHOW VIEW", "SHUTDOWN", "SUPER", "TRIGGER", "UPDATE"},
	Database: {"ALTER", "ALTER ROUTINE", "CREATE", "CREATE ROUTINE", "CREATE TEMPORARY TABLES", "CREATE VIEW", "DELETE", "DROP", "EVENT",
		"EXECUTE", "INDEX", "INSERT", "LOCK TABLES", "REFERENCES", "SELECT", "SHOW VIEW", "TRIGGER", "UPDATE"},
	Table:    {"ALTER", "CREATE", "CREATE VIEW", "DELETE", "DROP", "INDEX", "INSERT", "REFERENCES", "SELECT", "SHOW VIEW", "TRIGGER", "UPDATE"},
	Column:   {"INSERT", "REFERENCES", "SELECT", "UPDATE"},
	Function: {"ALTER ROUTINE", "EXECUTE"},
}

func mySQL(old *User, u User) ([]Stmt, error) {
	var out []string
	acct := myAccount(u.Name, u.Host)
	if old == nil {
		var b strings.Builder
		b.WriteString("CREATE USER " + acct)
		switch {
		case u.Plugin != "" && u.Password != nil:
			b.WriteString(" IDENTIFIED WITH " + u.Plugin + " BY " + myStr(*u.Password))
		case u.Plugin != "":
			b.WriteString(" IDENTIFIED WITH " + u.Plugin)
		case u.Password != nil:
			b.WriteString(" IDENTIFIED BY " + myStr(*u.Password))
		}
		if u.SSLType != "" {
			b.WriteString(" REQUIRE " + u.SSLType)
		}
		if lim := myLimits(nil, u); lim != "" {
			b.WriteString(" WITH" + lim)
		}
		if u.PasswordExpired {
			b.WriteString(" PASSWORD EXPIRE")
		}
		if u.Locked {
			b.WriteString(" ACCOUNT LOCK")
		}
		out = append(out, b.String())
		for _, r := range u.MemberOf {
			n, h := SplitAccount(r)
			out = append(out, "GRANT "+myAccount(n, h)+" TO "+acct)
		}
		out = append(out, myGrants(nil, u.Privileges, acct)...)
		return stmts("", out), nil
	}

	oldAcct := myAccount(old.Name, old.Host)
	if oldAcct != acct {
		out = append(out, "RENAME USER "+oldAcct+" TO "+acct)
	}
	var b strings.Builder
	switch {
	case u.Password != nil && u.Plugin != "" && u.Plugin != old.Plugin:
		b.WriteString(" IDENTIFIED WITH " + u.Plugin + " BY " + myStr(*u.Password))
	case u.Password != nil:
		b.WriteString(" IDENTIFIED BY " + myStr(*u.Password))
	case u.Plugin != "" && u.Plugin != old.Plugin:
		return nil, fmt.Errorf("enter a password to change the authentication plugin")
	}
	if u.SSLType != old.SSLType {
		req := u.SSLType
		if req == "" {
			req = "NONE"
		}
		b.WriteString(" REQUIRE " + req)
	}
	if lim := myLimits(old, u); lim != "" {
		b.WriteString(" WITH" + lim)
	}
	if u.PasswordExpired && !old.PasswordExpired {
		b.WriteString(" PASSWORD EXPIRE")
	}
	if u.Locked != old.Locked {
		if u.Locked {
			b.WriteString(" ACCOUNT LOCK")
		} else {
			b.WriteString(" ACCOUNT UNLOCK")
		}
	}
	if b.Len() > 0 {
		out = append(out, "ALTER USER "+acct+b.String())
	}
	add, remove := membershipDiff(old.MemberOf, u.MemberOf)
	for _, r := range remove {
		n, h := SplitAccount(r)
		out = append(out, "REVOKE "+myAccount(n, h)+" FROM "+acct)
	}
	for _, r := range add {
		n, h := SplitAccount(r)
		out = append(out, "GRANT "+myAccount(n, h)+" TO "+acct)
	}
	out = append(out, myGrants(old.Privileges, u.Privileges, acct)...)
	return stmts("", out), nil
}

// myLimits states resource limits that differ from old (all non-zero ones when old is nil).
func myLimits(old *User, u User) string {
	var b strings.Builder
	lim := func(name string, was, is int) {
		if (old == nil && is != 0) || (old != nil && was != is) {
			b.WriteString(" " + name + " " + strconv.Itoa(is))
		}
	}
	var o User
	if old != nil {
		o = *old
	}
	lim("MAX_QUERIES_PER_HOUR", o.MaxQueries, u.MaxQueries)
	lim("MAX_UPDATES_PER_HOUR", o.MaxUpdates, u.MaxUpdates)
	lim("MAX_CONNECTIONS_PER_HOUR", o.MaxConnections, u.MaxConnections)
	lim("MAX_USER_CONNECTIONS", o.MaxUserConnections, u.MaxUserConnections)
	return b.String()
}

func myGrants(old, cur []Grant, acct string) []string {
	add, addGrantable, remove, revokeOption := grantDiff(old, cur)
	var out []string
	// MySQL grant options belong to a level, not a privilege: revoke them first.
	for _, c := range revokeOption {
		out = append(out, "REVOKE GRANT OPTION ON "+myTarget(c.target)+" FROM "+acct)
	}
	for _, c := range remove {
		out = append(out, "REVOKE "+myPrivList(c)+" ON "+myTarget(c.target)+" FROM "+acct)
	}
	for _, c := range add {
		out = append(out, "GRANT "+myPrivList(c)+" ON "+myTarget(c.target)+" TO "+acct)
	}
	for _, c := range addGrantable {
		out = append(out, "GRANT "+myPrivList(c)+" ON "+myTarget(c.target)+" TO "+acct+" WITH GRANT OPTION")
	}
	return out
}

func stmts(db string, sqls []string) []Stmt {
	out := make([]Stmt, len(sqls))
	for i, s := range sqls {
		out[i] = Stmt{Database: db, SQL: s}
	}
	return out
}
