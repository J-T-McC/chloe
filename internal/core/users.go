package core

import (
	"context"

	"chloe/internal/privs"
)

func (m *Manager) ListUsers(ctx context.Context, connID string) ([]privs.UserInfo, error) {
	c, err := m.conn(connID)
	if err != nil {
		return nil, err
	}
	return c.ListUsers(ctx)
}

// LoadUser reads an account; database selects whose object privileges to include
// (PostgreSQL; ignored by MySQL).
func (m *Manager) LoadUser(ctx context.Context, connID, name, host, database string) (privs.User, error) {
	c, err := m.conn(connID)
	if err != nil {
		return privs.User{}, err
	}
	return c.LoadUser(ctx, name, host, database)
}

func (m *Manager) UserMeta(ctx context.Context, connID string) (privs.Meta, error) {
	c, err := m.conn(connID)
	if err != nil {
		return privs.Meta{}, err
	}
	return c.UserMeta(ctx)
}

// UserSQL previews the statements saving a user would run. old is nil for a new one.
func (m *Manager) UserSQL(connID string, old *privs.User, u privs.User) ([]privs.Stmt, error) {
	c, err := m.conn(connID)
	if err != nil {
		return nil, err
	}
	return privs.SQL(c.Dialect().Name, old, u)
}

// ApplyUser runs a user change. Statements run in order; consecutive statements for
// the same database share a session (PostgreSQL object grants run in their database).
func (m *Manager) ApplyUser(ctx context.Context, connID string, old *privs.User, u privs.User) ApplyResult {
	stmts, err := m.UserSQL(connID, old, u)
	if err != nil {
		return ApplyResult{Error: err.Error()}
	}
	return m.runStmts(ctx, connID, stmts)
}

// DropUsers deletes accounts.
func (m *Manager) DropUsers(ctx context.Context, connID string, users []privs.UserInfo) ApplyResult {
	c, err := m.conn(connID)
	if err != nil {
		return ApplyResult{Error: err.Error()}
	}
	var stmts []privs.Stmt
	for _, u := range users {
		stmts = append(stmts, privs.Stmt{SQL: privs.DropSQL(c.Dialect().Name, u)})
	}
	return m.runStmts(ctx, connID, stmts)
}

func (m *Manager) runStmts(ctx context.Context, connID string, stmts []privs.Stmt) ApplyResult {
	var total ApplyResult
	for i := 0; i < len(stmts); {
		j := i
		var batch []string
		for j < len(stmts) && stmts[j].Database == stmts[i].Database {
			batch = append(batch, stmts[j].SQL)
			j++
		}
		r := m.ExecuteDDL(ctx, connID, stmts[i].Database, "", batch, false)
		total.Executed += r.Executed
		if r.Error != "" {
			total.Error, total.FailedStatement = r.Error, r.FailedStatement
			return total
		}
		i = j
	}
	return total
}

// ObjectGrants lists every account's privileges on one object.
func (m *Manager) ObjectGrants(ctx context.Context, connID string, target privs.Grant) ([]privs.ObjectGrant, error) {
	c, err := m.conn(connID)
	if err != nil {
		return nil, err
	}
	return c.ObjectGrants(ctx, target)
}
