package core

import (
	"context"

	"chloe/internal/driver"
	"chloe/internal/model"
)

// Ensure opens a saved connection if it isn't open yet, using its saved password.
func (m *Manager) Ensure(ctx context.Context, connID string) error {
	if m.IsConnected(connID) {
		return nil
	}
	_, err := m.Connect(ctx, connID, "")
	return err
}

// ConnType reports a saved connection's engine.
func (m *Manager) ConnType(connID string) (model.DBType, error) {
	c, err := m.Store.Get(connID)
	return c.Type, err
}

// WithSession runs fn on a dedicated session of a SQL connection (opened on
// demand). The session's server connection is discarded afterwards, so nothing
// fn sets on it (transactions, pragmas) outlives the call.
func (m *Manager) WithSession(ctx context.Context, connID, database, schema string, fn func(driver.Session, driver.Dialect) error) error {
	if err := m.Ensure(ctx, connID); err != nil {
		return err
	}
	c, err := m.conn(connID)
	if err != nil {
		return err
	}
	ds, err := c.NewSession(ctx, database, schema)
	if err != nil {
		return err
	}
	defer ds.Close()
	return fn(ds, c.Dialect())
}
