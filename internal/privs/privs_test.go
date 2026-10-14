package privs

import (
	"reflect"
	"testing"
)

func sqls(t *testing.T, dialect string, old *User, u User) []string {
	t.Helper()
	st, err := SQL(dialect, old, u)
	if err != nil {
		t.Fatal(err)
	}
	out := make([]string, len(st))
	for i, s := range st {
		out[i] = s.SQL
		if s.Database != "" {
			out[i] = "[" + s.Database + "] " + s.SQL
		}
	}
	return out
}

func pw(s string) *string { return &s }

func TestMySQLCreateAndAlter(t *testing.T) {
	u := User{Name: "app", Host: "%", Password: pw("s3cr'et"), Plugin: "caching_sha2_password", SSLType: "SSL", MaxQueries: 100, Locked: true,
		MemberOf: []string{"reader@%"},
		Privileges: []Grant{
			{Level: Global, Privilege: "PROCESS"},
			{Level: Database, Database: "shop", Privilege: "SELECT"},
			{Level: Database, Database: "shop", Privilege: "INSERT"},
			{Level: Column, Database: "shop", Object: "customers", Column: "email", Privilege: "UPDATE", Grantable: true},
		}}
	got := sqls(t, "mysql", nil, u)
	want := []string{
		"CREATE USER 'app'@'%' IDENTIFIED WITH caching_sha2_password BY 's3cr''et' REQUIRE SSL WITH MAX_QUERIES_PER_HOUR 100 ACCOUNT LOCK",
		"GRANT 'reader'@'%' TO 'app'@'%'",
		"GRANT PROCESS ON *.* TO 'app'@'%'",
		"GRANT INSERT, SELECT ON `shop`.* TO 'app'@'%'",
		"GRANT UPDATE (`email`) ON `shop`.`customers` TO 'app'@'%' WITH GRANT OPTION",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("create:\n%q\nwant\n%q", got, want)
	}

	old := u
	old.Password = nil
	next := old
	next.Name, next.Host, next.Locked, next.SSLType = "app2", "localhost", false, ""
	next.MemberOf = []string{"writer@%"}
	next.Privileges = []Grant{
		{Level: Database, Database: "shop", Privilege: "SELECT"},
		{Level: Column, Database: "shop", Object: "customers", Column: "email", Privilege: "UPDATE"},
		{Level: Table, Database: "shop", Object: "orders", Privilege: "DELETE"},
	}
	got = sqls(t, "mysql", &old, next)
	want = []string{
		"RENAME USER 'app'@'%' TO 'app2'@'localhost'",
		"ALTER USER 'app2'@'localhost' REQUIRE NONE ACCOUNT UNLOCK",
		"REVOKE 'reader'@'%' FROM 'app2'@'localhost'",
		"GRANT 'writer'@'%' TO 'app2'@'localhost'",
		"REVOKE GRANT OPTION ON `shop`.`customers` FROM 'app2'@'localhost'",
		"REVOKE PROCESS ON *.* FROM 'app2'@'localhost'",
		"REVOKE INSERT ON `shop`.* FROM 'app2'@'localhost'",
		"GRANT DELETE ON `shop`.`orders` TO 'app2'@'localhost'",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("alter:\n%q\nwant\n%q", got, want)
	}
	if got := sqls(t, "mysql", &old, old); len(got) != 0 {
		t.Fatalf("unchanged user produced %q", got)
	}
}

func TestPostgresCreateAndAlter(t *testing.T) {
	u := User{Name: "app", CanLogin: true, Inherit: true, ConnLimit: -1, Password: pw("x"), ValidUntil: "2030-01-01", Comment: "app login",
		MemberOf: []string{"readers"},
		Privileges: []Grant{
			{Level: Database, Database: "shop", Privilege: "CONNECT"},
			{Level: Schema, Database: "shop", Schema: "public", Privilege: "USAGE"},
			{Level: Table, Database: "shop", Schema: "public", Object: "orders", Privilege: "SELECT", Grantable: true},
			{Level: Column, Database: "shop", Schema: "public", Object: "customers", Column: "email", Privilege: "SELECT"},
			{Level: Function, Database: "shop", Schema: "public", Object: "order_total(bigint)", Privilege: "EXECUTE"},
		}}
	got := sqls(t, "postgres", nil, u)
	want := []string{
		`CREATE ROLE "app" WITH LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE INHERIT NOREPLICATION NOBYPASSRLS CONNECTION LIMIT -1 PASSWORD 'x' VALID UNTIL '2030-01-01'`,
		`COMMENT ON ROLE "app" IS 'app login'`,
		`GRANT "readers" TO "app"`,
		`GRANT CONNECT ON DATABASE "shop" TO "app"`,
		`[shop] GRANT USAGE ON SCHEMA "public" TO "app"`,
		`[shop] GRANT SELECT ("email") ON TABLE "public"."customers" TO "app"`,
		`[shop] GRANT EXECUTE ON FUNCTION "public"."order_total"(bigint) TO "app"`,
		`[shop] GRANT SELECT ON TABLE "public"."orders" TO "app" WITH GRANT OPTION`,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("create:\n%q\nwant\n%q", got, want)
	}
	old := u
	old.Password = nil
	next := old
	next.Name, next.CreateDB, next.ValidUntil, next.Comment = "app2", true, "", ""
	next.Privileges = []Grant{
		{Level: Database, Database: "shop", Privilege: "CONNECT"},
		{Level: Table, Database: "shop", Schema: "public", Object: "orders", Privilege: "SELECT"},
		{Level: Table, Database: "shop", Schema: "public", Object: "orders", Privilege: "INSERT"},
	}
	got = sqls(t, "postgres", &old, next)
	want = []string{
		`ALTER ROLE "app" RENAME TO "app2"`,
		`ALTER ROLE "app2" WITH CREATEDB VALID UNTIL 'infinity'`,
		`COMMENT ON ROLE "app2" IS NULL`,
		`[shop] REVOKE GRANT OPTION FOR SELECT ON TABLE "public"."orders" FROM "app2"`,
		`[shop] REVOKE USAGE ON SCHEMA "public" FROM "app2"`,
		`[shop] REVOKE SELECT ("email") ON TABLE "public"."customers" FROM "app2"`,
		`[shop] REVOKE EXECUTE ON FUNCTION "public"."order_total"(bigint) FROM "app2"`,
		`[shop] GRANT INSERT ON TABLE "public"."orders" TO "app2"`,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("alter:\n%q\nwant\n%q", got, want)
	}
}
