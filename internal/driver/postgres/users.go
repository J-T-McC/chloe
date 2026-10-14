package postgres

import (
	"context"
	"fmt"
	"strconv"

	"chloe/internal/driver/sqlcommon"
	"chloe/internal/privs"
)

func (c *Conn) ListUsers(ctx context.Context) ([]privs.UserInfo, error) {
	db, err := c.pool(ctx, "")
	if err != nil {
		return nil, err
	}
	rows, err := sqlcommon.QueryStrings(ctx, db, `SELECT rolname, rolcanlogin, rolsuper FROM pg_roles WHERE rolname !~ '^pg_' ORDER BY 1`)
	if err != nil {
		return nil, err
	}
	out := make([]privs.UserInfo, len(rows))
	for i, r := range rows {
		login := r[1].String == "true"
		out[i] = privs.UserInfo{Name: r[0].String, CanLogin: login, Super: r[2].String == "true", IsRole: !login}
	}
	return out, nil
}

func (c *Conn) LoadUser(ctx context.Context, name, _, database string) (privs.User, error) {
	u := privs.User{Name: name, OrigName: name, MemberOf: []string{}, Privileges: []privs.Grant{}}
	db, err := c.pool(ctx, "")
	if err != nil {
		return u, err
	}
	rows, err := sqlcommon.QueryStrings(ctx, db, `SELECT rolcanlogin, rolsuper, rolcreatedb, rolcreaterole, rolinherit, rolreplication, rolbypassrls,
		rolconnlimit, coalesce(rolvaliduntil::text, ''), coalesce(shobj_description(oid, 'pg_authid'), ''), oid
		FROM pg_roles WHERE rolname = $1`, name)
	if err != nil {
		return u, err
	}
	if len(rows) == 0 {
		return u, fmt.Errorf("role %s not found", name)
	}
	r := rows[0]
	b := func(i int) bool { return r[i].String == "true" }
	u.CanLogin, u.Superuser, u.CreateDB, u.CreateRole, u.Inherit, u.Replication, u.BypassRLS = b(0), b(1), b(2), b(3), b(4), b(5), b(6)
	u.ConnLimit, _ = strconv.Atoi(r[7].String)
	if v := r[8].String; v != "infinity" {
		u.ValidUntil = v
	}
	u.Comment = r[9].String
	oid := r[10].String

	if rs, err := sqlcommon.QueryStrings(ctx, db, `SELECT g.rolname FROM pg_auth_members m JOIN pg_roles g ON g.oid = m.roleid WHERE m.member = $1::oid ORDER BY 1`, oid); err == nil {
		for _, x := range rs {
			u.MemberOf = append(u.MemberOf, x[0].String)
		}
	}
	add := func(g privs.Grant, grantable string) {
		g.Grantable = grantable == "true"
		u.Privileges = append(u.Privileges, g)
	}
	// Database privileges (the catalog is shared, so any database answers).
	if rs, err := sqlcommon.QueryStrings(ctx, db, `SELECT d.datname, a.privilege_type, a.is_grantable FROM pg_database d, aclexplode(d.datacl) a
		WHERE a.grantee = $1::oid AND NOT (a.grantor = a.grantee AND d.datdba = a.grantee)`, oid); err == nil {
		for _, x := range rs {
			add(privs.Grant{Level: privs.Database, Database: x[0].String, Privilege: x[1].String}, x[2].String)
		}
	}
	if database == "" {
		return u, nil
	}
	in, err := c.pool(ctx, database)
	if err != nil {
		return u, err
	}
	// Owners' implicit privileges show up in ACLs as self-grants; they aren't grants to manage.
	if rs, err := sqlcommon.QueryStrings(ctx, in, `SELECT n.nspname, a.privilege_type, a.is_grantable FROM pg_namespace n, aclexplode(n.nspacl) a
		WHERE a.grantee = $1::oid AND NOT (a.grantor = a.grantee AND n.nspowner = a.grantee)`, oid); err == nil {
		for _, x := range rs {
			add(privs.Grant{Level: privs.Schema, Database: database, Schema: x[0].String, Privilege: x[1].String}, x[2].String)
		}
	}
	if rs, err := sqlcommon.QueryStrings(ctx, in, `SELECT n.nspname, c.relname, c.relkind, a.privilege_type, a.is_grantable
		FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace, aclexplode(c.relacl) a
		WHERE a.grantee = $1::oid AND c.relkind IN ('r','p','v','m','f','S') AND NOT (a.grantor = a.grantee AND c.relowner = a.grantee)`, oid); err == nil {
		for _, x := range rs {
			level := privs.Table
			if x[2].String == "S" {
				level = privs.Sequence
			}
			add(privs.Grant{Level: level, Database: database, Schema: x[0].String, Object: x[1].String, Privilege: x[3].String}, x[4].String)
		}
	}
	if rs, err := sqlcommon.QueryStrings(ctx, in, `SELECT n.nspname, c.relname, att.attname, a.privilege_type, a.is_grantable
		FROM pg_attribute att JOIN pg_class c ON c.oid = att.attrelid JOIN pg_namespace n ON n.oid = c.relnamespace, aclexplode(att.attacl) a
		WHERE a.grantee = $1::oid AND att.attnum > 0 AND NOT att.attisdropped`, oid); err == nil {
		for _, x := range rs {
			add(privs.Grant{Level: privs.Column, Database: database, Schema: x[0].String, Object: x[1].String, Column: x[2].String, Privilege: x[3].String}, x[4].String)
		}
	}
	if rs, err := sqlcommon.QueryStrings(ctx, in, `SELECT n.nspname, p.proname || '(' || pg_get_function_identity_arguments(p.oid) || ')', a.privilege_type, a.is_grantable
		FROM pg_proc p JOIN pg_namespace n ON n.oid = p.pronamespace, aclexplode(p.proacl) a
		WHERE a.grantee = $1::oid AND NOT (a.grantor = a.grantee AND p.proowner = a.grantee)`, oid); err == nil {
		for _, x := range rs {
			add(privs.Grant{Level: privs.Function, Database: database, Schema: x[0].String, Object: x[1].String, Privilege: x[2].String}, x[3].String)
		}
	}
	return u, nil
}

func (c *Conn) UserMeta(ctx context.Context) (privs.Meta, error) {
	m := privs.Meta{Roles: []string{}, Levels: privs.PostgresPrivileges}
	db, err := c.pool(ctx, "")
	if err != nil {
		return m, err
	}
	if rs, err := sqlcommon.QueryStrings(ctx, db, `SELECT rolname FROM pg_roles ORDER BY rolname ~ '^pg_', rolname`); err == nil {
		for _, x := range rs {
			m.Roles = append(m.Roles, x[0].String)
		}
	}
	return m, nil
}

func (c *Conn) ObjectGrants(ctx context.Context, t privs.Grant) ([]privs.ObjectGrant, error) {
	var q string
	args := []any{}
	switch t.Level {
	case privs.Database:
		q = `SELECT r.rolname, a.privilege_type, a.is_grantable FROM pg_database d, aclexplode(d.datacl) a JOIN pg_roles r ON r.oid = a.grantee
			WHERE d.datname = $1 AND NOT (a.grantor = a.grantee AND d.datdba = a.grantee)`
		args = append(args, t.Database)
	case privs.Schema:
		q = `SELECT r.rolname, a.privilege_type, a.is_grantable FROM pg_namespace n, aclexplode(n.nspacl) a JOIN pg_roles r ON r.oid = a.grantee
			WHERE n.nspname = $1 AND NOT (a.grantor = a.grantee AND n.nspowner = a.grantee)`
		args = append(args, t.Schema)
	case privs.Table, privs.Sequence:
		q = `SELECT r.rolname, a.privilege_type, a.is_grantable FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace, aclexplode(c.relacl) a JOIN pg_roles r ON r.oid = a.grantee
			WHERE n.nspname = $1 AND c.relname = $2 AND NOT (a.grantor = a.grantee AND c.relowner = a.grantee)`
		args = append(args, t.Schema, t.Object)
	case privs.Function:
		q = `SELECT r.rolname, a.privilege_type, a.is_grantable FROM pg_proc p JOIN pg_namespace n ON n.oid = p.pronamespace, aclexplode(p.proacl) a JOIN pg_roles r ON r.oid = a.grantee
			WHERE n.nspname = $1 AND p.proname || '(' || pg_get_function_identity_arguments(p.oid) || ')' = $2 AND NOT (a.grantor = a.grantee AND p.proowner = a.grantee)`
		args = append(args, t.Schema, t.Object)
	default:
		return nil, fmt.Errorf("can't list grants on a %s", t.Level)
	}
	db, err := c.pool(ctx, t.Database)
	if err != nil {
		return nil, err
	}
	rs, err := sqlcommon.QueryStrings(ctx, db, q, args...)
	if err != nil {
		return nil, err
	}
	out := make([]privs.ObjectGrant, len(rs))
	for i, x := range rs {
		out[i] = privs.ObjectGrant{User: x[0].String, Privilege: x[1].String, Grantable: x[2].String == "true"}
	}
	return out, nil
}
