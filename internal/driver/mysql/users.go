package mysql

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"

	"chloe/internal/driver/sqlcommon"
	"chloe/internal/privs"
)

func (c *Conn) ListUsers(ctx context.Context) ([]privs.UserInfo, error) {
	rows, err := sqlcommon.QueryStrings(ctx, c.db, `SELECT User, Host, plugin, account_locked, authentication_string = '', Super_priv FROM mysql.user ORDER BY User, Host`)
	if err != nil {
		return nil, fmt.Errorf("listing users needs SELECT on mysql.user: %w", err)
	}
	out := make([]privs.UserInfo, len(rows))
	for i, r := range rows {
		locked := r[3].String == "Y"
		out[i] = privs.UserInfo{Name: r[0].String, Host: r[1].String, Plugin: r[2].String, Locked: locked, CanLogin: !locked,
			Super: r[5].String == "Y", IsRole: locked && r[4].String == "1"}
	}
	return out, nil
}

func grantee(name, host string) string {
	return "'" + strings.ReplaceAll(name, "'", "''") + "'@'" + strings.ReplaceAll(host, "'", "''") + "'"
}

func (c *Conn) LoadUser(ctx context.Context, name, host, _ string) (privs.User, error) {
	u := privs.User{Name: name, Host: host, OrigName: name, OrigHost: host, MemberOf: []string{}, Privileges: []privs.Grant{}}
	rows, err := sqlcommon.QueryStrings(ctx, c.db, `SELECT plugin, max_questions, max_updates, max_connections, max_user_connections, ssl_type, account_locked, password_expired
		FROM mysql.user WHERE User = ? AND Host = ?`, name, host)
	if err != nil {
		return u, err
	}
	if len(rows) == 0 {
		return u, fmt.Errorf("user %s@%s not found", name, host)
	}
	r := rows[0]
	u.Plugin = r[0].String
	fmt.Sscan(r[1].String, &u.MaxQueries)
	fmt.Sscan(r[2].String, &u.MaxUpdates)
	fmt.Sscan(r[3].String, &u.MaxConnections)
	fmt.Sscan(r[4].String, &u.MaxUserConnections)
	switch r[5].String {
	case "ANY":
		u.SSLType = "SSL"
	case "X509":
		u.SSLType = "X509"
	}
	u.Locked = r[6].String == "Y"
	u.PasswordExpired = r[7].String == "Y"

	if roles, err := sqlcommon.QueryStrings(ctx, c.db, `SELECT FROM_USER, FROM_HOST FROM mysql.role_edges WHERE TO_USER = ? AND TO_HOST = ? ORDER BY 1, 2`, name, host); err == nil {
		for _, x := range roles {
			u.MemberOf = append(u.MemberOf, x[0].String+"@"+x[1].String)
		}
	}
	g := grantee(name, host)
	add := func(gr privs.Grant, grantable string) {
		gr.Grantable = grantable == "YES"
		u.Privileges = append(u.Privileges, gr)
	}
	if rs, err := sqlcommon.QueryStrings(ctx, c.db, `SELECT PRIVILEGE_TYPE, IS_GRANTABLE FROM information_schema.USER_PRIVILEGES WHERE GRANTEE = ?`, g); err == nil {
		for _, x := range rs {
			if x[0].String != "USAGE" {
				add(privs.Grant{Level: privs.Global, Privilege: x[0].String}, x[1].String)
			}
		}
	}
	if rs, err := sqlcommon.QueryStrings(ctx, c.db, `SELECT TABLE_SCHEMA, PRIVILEGE_TYPE, IS_GRANTABLE FROM information_schema.SCHEMA_PRIVILEGES WHERE GRANTEE = ?`, g); err == nil {
		for _, x := range rs {
			add(privs.Grant{Level: privs.Database, Database: x[0].String, Privilege: x[1].String}, x[2].String)
		}
	}
	if rs, err := sqlcommon.QueryStrings(ctx, c.db, `SELECT TABLE_SCHEMA, TABLE_NAME, PRIVILEGE_TYPE, IS_GRANTABLE FROM information_schema.TABLE_PRIVILEGES WHERE GRANTEE = ?`, g); err == nil {
		for _, x := range rs {
			add(privs.Grant{Level: privs.Table, Database: x[0].String, Object: x[1].String, Privilege: x[2].String}, x[3].String)
		}
	}
	if rs, err := sqlcommon.QueryStrings(ctx, c.db, `SELECT TABLE_SCHEMA, TABLE_NAME, COLUMN_NAME, PRIVILEGE_TYPE, IS_GRANTABLE FROM information_schema.COLUMN_PRIVILEGES WHERE GRANTEE = ?`, g); err == nil {
		for _, x := range rs {
			add(privs.Grant{Level: privs.Column, Database: x[0].String, Object: x[1].String, Column: x[2].String, Privilege: x[3].String}, x[4].String)
		}
	}
	if rs, err := sqlcommon.QueryStrings(ctx, c.db, `SELECT Db, Routine_name, Routine_type, Proc_priv FROM mysql.procs_priv WHERE User = ? AND Host = ?`, name, host); err == nil {
		for _, x := range rs {
			set := strings.Split(x[3].String, ",")
			grantable := "NO"
			for _, p := range set {
				if p == "Grant" {
					grantable = "YES"
				}
			}
			for _, p := range set {
				if p == "" || p == "Grant" {
					continue
				}
				add(privs.Grant{Level: privs.Function, Database: x[0].String, Object: x[1].String, RoutineType: x[2].String, Privilege: strings.ToUpper(p)}, grantable)
			}
		}
	}
	return u, nil
}

func (c *Conn) UserMeta(ctx context.Context) (privs.Meta, error) {
	m := privs.Meta{Roles: []string{}, Levels: map[string][]string{}}
	for k, v := range privs.MySQLPrivileges {
		m.Levels[k] = append([]string(nil), v...)
	}
	// Dynamic privileges (BACKUP_ADMIN, …) are server-wide.
	if rs, err := sqlcommon.QueryStrings(ctx, c.db, "SHOW PRIVILEGES"); err == nil {
		known := map[string]bool{}
		for _, p := range m.Levels[privs.Global] {
			known[p] = true
		}
		for _, x := range rs {
			p := strings.ToUpper(x[0].String)
			if strings.Contains(p, "_") && !known[p] {
				m.Levels[privs.Global] = append(m.Levels[privs.Global], p)
			}
		}
		sort.Strings(m.Levels[privs.Global])
	}
	if rs, err := sqlcommon.QueryStrings(ctx, c.db, "SELECT User, Host FROM mysql.user ORDER BY User, Host"); err == nil {
		for _, x := range rs {
			m.Roles = append(m.Roles, x[0].String+"@"+x[1].String)
		}
	}
	if rs, err := sqlcommon.QueryStrings(ctx, c.db, "SELECT PLUGIN_NAME FROM information_schema.PLUGINS WHERE PLUGIN_TYPE = 'AUTHENTICATION' AND PLUGIN_STATUS = 'ACTIVE' ORDER BY 1"); err == nil {
		for _, x := range rs {
			m.Plugins = append(m.Plugins, x[0].String)
		}
	}
	return m, nil
}

var granteeRe = regexp.MustCompile(`^'((?:[^']|'')*)'@'((?:[^']|'')*)'$`)

func (c *Conn) ObjectGrants(ctx context.Context, t privs.Grant) ([]privs.ObjectGrant, error) {
	var out []privs.ObjectGrant
	fromGrantee := func(g, priv, grantable string) {
		if m := granteeRe.FindStringSubmatch(g); m != nil {
			out = append(out, privs.ObjectGrant{User: strings.ReplaceAll(m[1], "''", "'"), Host: strings.ReplaceAll(m[2], "''", "'"), Privilege: priv, Grantable: grantable == "YES"})
		}
	}
	switch t.Level {
	case privs.Database:
		rs, err := sqlcommon.QueryStrings(ctx, c.db, `SELECT GRANTEE, PRIVILEGE_TYPE, IS_GRANTABLE FROM information_schema.SCHEMA_PRIVILEGES WHERE TABLE_SCHEMA = ?`, t.Database)
		if err != nil {
			return nil, err
		}
		for _, x := range rs {
			fromGrantee(x[0].String, x[1].String, x[2].String)
		}
	case privs.Table:
		rs, err := sqlcommon.QueryStrings(ctx, c.db, `SELECT GRANTEE, PRIVILEGE_TYPE, IS_GRANTABLE FROM information_schema.TABLE_PRIVILEGES WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ?`, t.Database, t.Object)
		if err != nil {
			return nil, err
		}
		for _, x := range rs {
			fromGrantee(x[0].String, x[1].String, x[2].String)
		}
	case privs.Function:
		rs, err := sqlcommon.QueryStrings(ctx, c.db, `SELECT User, Host, Proc_priv FROM mysql.procs_priv WHERE Db = ? AND Routine_name = ? AND Routine_type = ?`, t.Database, t.Object, t.RoutineType)
		if err != nil {
			return nil, err
		}
		for _, x := range rs {
			set := strings.Split(x[2].String, ",")
			grantable := slices.Contains(set, "Grant")
			for _, p := range set {
				if p != "" && p != "Grant" {
					out = append(out, privs.ObjectGrant{User: x[0].String, Host: x[1].String, Privilege: strings.ToUpper(p), Grantable: grantable})
				}
			}
		}
	default:
		return nil, fmt.Errorf("can't list grants on a %s", t.Level)
	}
	return out, nil
}
