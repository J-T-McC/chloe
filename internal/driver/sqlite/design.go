package sqlite

import (
	"context"
	"fmt"

	"chloe/internal/design"
	"chloe/internal/driver/sqlcommon"
	"chloe/internal/model"
	"chloe/internal/privs"
)

func (c *Conn) DesignGenerator() design.Generator { return design.SQLite{} }

// LoadDesign reads a table from its stored CREATE statement, its indexes and its triggers.
func (c *Conn) LoadDesign(ctx context.Context, ref model.ObjectRef) (design.Table, error) {
	src, err := c.objectSQL(ctx, ref.Database, "table", ref.Name)
	if err != nil {
		return design.Table{}, err
	}
	t, err := parseCreateTable(src)
	if err != nil {
		return t, fmt.Errorf("%s: %w", ref.Name, err)
	}
	t.Database, t.Name, t.OrigName = ref.Database, ref.Name, ref.Name
	for i := range t.Columns {
		t.Columns[i].OrigName = t.Columns[i].Name
	}
	for i := range t.ForeignKeys {
		f := &t.ForeignKeys[i]
		if len(f.RefColumns) == 0 {
			// REFERENCES parent: the parent's primary key.
			if pcols, err := c.columns(ctx, ref.Database, f.RefTable); err == nil {
				for _, pc := range pcols {
					if pc.pk > 0 {
						f.RefColumns = append(f.RefColumns, pc.name)
					}
				}
			}
		}
	}
	recs, err := sqlcommon.QueryStrings(ctx, c.db, "SELECT type, sql FROM "+master(ref.Database)+
		" WHERE tbl_name = ? AND type IN ('index', 'trigger') AND sql IS NOT NULL ORDER BY name", ref.Name)
	if err != nil {
		return t, err
	}
	for _, r := range recs {
		switch r[0].String {
		case "index":
			ix, err := parseCreateIndex(r[1].String)
			if err != nil {
				return t, fmt.Errorf("index: %w", err)
			}
			t.Indexes = append(t.Indexes, ix)
		case "trigger":
			tr, err := parseCreateTrigger(r[1].String)
			if err != nil {
				return t, fmt.Errorf("trigger: %w", err)
			}
			t.Triggers = append(t.Triggers, tr)
		}
	}
	return t, nil
}

func (c *Conn) DesignMeta(context.Context, string) (design.Meta, error) {
	return design.OfflineMeta("sqlite"), nil
}

func (c *Conn) LoadView(ctx context.Context, ref model.ObjectRef) (design.View, error) {
	src, err := c.objectSQL(ctx, ref.Database, "view", ref.Name)
	if err != nil {
		return design.View{}, err
	}
	def, err := parseCreateView(src)
	if err != nil {
		return design.View{}, err
	}
	return design.View{Database: ref.Database, Name: ref.Name, OrigName: ref.Name, Definition: def}, nil
}

func (c *Conn) LoadDatabase(_ context.Context, name string) (design.Database, error) {
	f := c.fileOf(name)
	if f == "" {
		return design.Database{}, fmt.Errorf("database %q isn't attached", name)
	}
	return design.Database{Name: name, File: f}, nil
}

func (c *Conn) DatabaseMeta(context.Context) (design.DatabaseMeta, error) {
	return design.DatabaseMeta{}, nil
}

// ---- users: SQLite has none ----------------------------------------------------------

func (c *Conn) ListUsers(context.Context) ([]privs.UserInfo, error) { return nil, ErrUnsupported }
func (c *Conn) LoadUser(context.Context, string, string, string) (privs.User, error) {
	return privs.User{}, ErrUnsupported
}
func (c *Conn) UserMeta(context.Context) (privs.Meta, error) { return privs.Meta{}, ErrUnsupported }
func (c *Conn) ObjectGrants(context.Context, privs.Grant) ([]privs.ObjectGrant, error) {
	return nil, ErrUnsupported
}
