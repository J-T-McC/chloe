package postgres

import (
	"context"
	"fmt"
	"strings"

	"chloe/internal/design"
	"chloe/internal/driver/sqlcommon"
	"chloe/internal/model"
)

func (c *Conn) LoadView(ctx context.Context, ref model.ObjectRef) (design.View, error) {
	db, err := c.pool(ctx, ref.Database)
	if err != nil {
		return design.View{}, err
	}
	schema := ref.Schema
	if schema == "" {
		schema = "public"
	}
	rows, err := sqlcommon.QueryStrings(ctx, db, `SELECT c.relkind, pg_get_viewdef(c.oid, true), coalesce(obj_description(c.oid, 'pg_class'), ''),
		coalesce(array_to_string(c.reloptions, ','), ''), coalesce(t.spcname, ''), coalesce(m.ispopulated, true)
		FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
		LEFT JOIN pg_tablespace t ON t.oid = c.reltablespace
		LEFT JOIN pg_matviews m ON m.schemaname = n.nspname AND m.matviewname = c.relname
		WHERE n.nspname = $1 AND c.relname = $2 AND c.relkind IN ('v', 'm')`, schema, ref.Name)
	if err != nil {
		return design.View{}, err
	}
	if len(rows) == 0 {
		return design.View{}, fmt.Errorf("view %s.%s not found", schema, ref.Name)
	}
	r := rows[0]
	v := design.View{
		Database: ref.Database, Schema: schema, Name: ref.Name, OrigName: ref.Name,
		Materialized: r[0].String == "m",
		Definition:   strings.TrimSuffix(strings.TrimSpace(r[1].String), ";"),
		Comment:      r[2].String,
		Tablespace:   r[4].String,
		NoData:       r[5].String == "false",
	}
	for _, o := range strings.Split(r[3].String, ",") {
		k, val, _ := strings.Cut(o, "=")
		switch k {
		case "check_option":
			v.CheckOption = strings.ToUpper(val)
		case "security_barrier":
			v.SecurityBarrier = val == "true"
		case "security_invoker":
			v.SecurityInvoker = val == "true"
		}
	}
	return v, nil
}
