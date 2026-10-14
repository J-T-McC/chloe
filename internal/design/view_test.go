package design

import (
	"reflect"
	"testing"
)

func TestViewSQL(t *testing.T) {
	base := View{Database: "shop", Name: "v", Definition: "SELECT id FROM t;\n"}
	pgBase := View{Database: "app", Schema: "s", Name: "v", Definition: "SELECT id FROM t"}
	mv := pgBase
	mv.Materialized = true
	cases := []struct {
		name    string
		dialect string
		old     *View
		v       View
		want    []string
		warn    bool
	}{
		{"mysql create", "mysql", nil, base, []string{"CREATE VIEW `shop`.`v` AS\nSELECT id FROM t"}, false},
		{"mysql create options", "mysql", nil, View{Database: "shop", Name: "v", Definition: "SELECT 1", Algorithm: "merge", Definer: "app@%", Security: "invoker", CheckOption: "cascaded"},
			[]string{"CREATE ALGORITHM = MERGE DEFINER = `app`@`%` SQL SECURITY INVOKER VIEW `shop`.`v` AS\nSELECT 1\nWITH CASCADED CHECK OPTION"}, false},
		{"mysql unchanged", "mysql", &base, base, nil, false},
		{"mysql rename + redefine", "mysql", &base, View{Database: "shop", Name: "w", Definition: "SELECT 2"},
			[]string{"RENAME TABLE `shop`.`v` TO `shop`.`w`", "CREATE OR REPLACE VIEW `shop`.`w` AS\nSELECT 2"}, false},
		{"mysql rename only", "mysql", &base, View{Database: "shop", Name: "w", Definition: "SELECT id FROM t"}, []string{"RENAME TABLE `shop`.`v` TO `shop`.`w`"}, false},
		{"pg create", "postgres", nil, View{Database: "app", Schema: "s", Name: "v", Definition: "SELECT 1", SecurityBarrier: true, CheckOption: "LOCAL", Comment: "c"},
			[]string{"CREATE VIEW \"s\".\"v\" WITH (check_option = local, security_barrier = true) AS\nSELECT 1", "COMMENT ON VIEW \"s\".\"v\" IS 'c'"}, false},
		{"pg options only", "postgres", &View{Database: "app", Schema: "s", Name: "v", Definition: "SELECT 1", SecurityBarrier: true}, View{Database: "app", Schema: "s", Name: "v", Definition: "SELECT 1", SecurityInvoker: true, CheckOption: "cascaded"},
			[]string{`ALTER VIEW "s"."v" SET (security_invoker = true, check_option = cascaded)`, `ALTER VIEW "s"."v" RESET (security_barrier)`}, false},
		{"pg redefine", "postgres", &pgBase, View{Database: "app", Schema: "s", Name: "v", Definition: "SELECT id, 2 FROM t"},
			[]string{"CREATE OR REPLACE VIEW \"s\".\"v\" AS\nSELECT id, 2 FROM t"}, false},
		{"matview create", "postgres", nil, View{Database: "app", Schema: "s", Name: "m", Materialized: true, Definition: "SELECT 1", NoData: true, Tablespace: "fast"},
			[]string{"CREATE MATERIALIZED VIEW \"s\".\"m\" TABLESPACE \"fast\" AS\nSELECT 1\nWITH NO DATA"}, false},
		{"matview rename + comment", "postgres", &mv, View{Database: "app", Schema: "s", Name: "m2", Materialized: true, Definition: "SELECT id FROM t", Comment: "x"},
			[]string{`ALTER MATERIALIZED VIEW "s"."v" RENAME TO "m2"`, `COMMENT ON MATERIALIZED VIEW "s"."m2" IS 'x'`}, false},
		{"matview redefine", "postgres", &mv, View{Database: "app", Schema: "s", Name: "v", Materialized: true, Definition: "SELECT 2"},
			[]string{`DROP MATERIALIZED VIEW "s"."v"`, "CREATE MATERIALIZED VIEW \"s\".\"v\" AS\nSELECT 2\nWITH DATA"}, true},
		{"view to matview", "postgres", &pgBase, mv,
			[]string{`DROP VIEW "s"."v"`, "CREATE MATERIALIZED VIEW \"s\".\"v\" AS\nSELECT id FROM t\nWITH DATA"}, true},
	}
	for _, c := range cases {
		got, warns, err := ViewSQL(c.dialect, c.old, c.v)
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s:\n got %q\nwant %q", c.name, got, c.want)
		}
		if (len(warns) > 0) != c.warn {
			t.Errorf("%s: warnings %q", c.name, warns)
		}
	}
	if _, _, err := ViewSQL("mysql", nil, View{Name: "v"}); err == nil {
		t.Error("empty definition accepted")
	}
}
