package core_test

// Integration tests against live databases. Start them with:
//   docker run -d --name chloe-mysql -e MYSQL_ROOT_PASSWORD=chloe -p 33306:3306 mysql:8.4
//   docker run -d --name chloe-pg -e POSTGRES_PASSWORD=chloe -p 55432:5432 postgres:17
// load testdata/seed_*.sql, then run with CHLOE_IT=1 go test ./internal/core/

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"chloe/internal/core"
	_ "chloe/internal/driver/mysql"
	_ "chloe/internal/driver/postgres"
	_ "chloe/internal/driver/sqlite"
	"chloe/internal/model"
	"chloe/internal/store"
	"chloe/internal/tunnel"
)

type engine struct {
	conn     model.Connection
	database string
	schema   string
}

func engines() []engine {
	return []engine{
		{model.Connection{Name: "mysql", Type: model.MySQL, Host: "127.0.0.1", Port: 33306, User: "root", Password: "chloe", SavePassword: true}, "shop", ""},
		{model.Connection{Name: "pg", Type: model.PostgreSQL, Host: "127.0.0.1", Port: 55432, User: "postgres", Password: "chloe", SavePassword: true}, "postgres", "shop"},
	}
}

func setup(t *testing.T, e engine) (*core.Manager, string) {
	t.Helper()
	if os.Getenv("CHLOE_IT") == "" {
		t.Skip("set CHLOE_IT=1 to run integration tests")
	}
	st, err := store.Open(t.TempDir(), &store.MemorySecrets{})
	if err != nil {
		t.Fatal(err)
	}
	saved, err := st.Save(e.conn)
	if err != nil {
		t.Fatal(err)
	}
	m := core.NewManager(st, nil, core.DefaultSettings)
	t.Cleanup(m.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if _, err := m.Connect(ctx, saved.ID, ""); err != nil {
		t.Fatal(err)
	}
	return m, saved.ID
}

func TestIntegration(t *testing.T) {
	for _, e := range engines() {
		t.Run(e.conn.Name, func(t *testing.T) {
			m, id := setup(t, e)
			ctx := context.Background()
			ref := func(name string, k model.ObjectKind) model.ObjectRef {
				return model.ObjectRef{Database: e.database, Schema: e.schema, Name: name, Kind: k}
			}

			dbs, err := m.ListDatabases(ctx, id)
			if err != nil || !slices.Contains(dbs, e.database) {
				t.Fatalf("databases %v %v", dbs, err)
			}
			if e.schema != "" {
				schemas, err := m.ListSchemas(ctx, id, e.database)
				if err != nil || !slices.Contains(schemas, "shop") {
					t.Fatalf("schemas %v %v", schemas, err)
				}
			}
			tables, err := m.ListObjects(ctx, id, e.database, e.schema, model.KindTable)
			if err != nil || len(tables) != 4 {
				t.Fatalf("tables %+v %v", tables, err)
			}
			views, _ := m.ListObjects(ctx, id, e.database, e.schema, model.KindView)
			if len(views) != 1 || views[0].Name != "customer_orders" {
				t.Fatalf("views %+v", views)
			}

			td, err := m.TableDetail(ctx, id, ref("order_items", model.KindTable))
			if err != nil {
				t.Fatal(err)
			}
			if len(td.Columns) != 3 || !td.Columns[0].PrimaryKey || len(td.ForeignKeys) != 2 {
				t.Fatalf("detail %+v", td)
			}
			// Index columns are names (or expressions), even before the table is analyzed.
			td, err = m.TableDetail(ctx, id, ref("products", model.KindTable))
			if err != nil || len(td.Indexes) != 2 {
				t.Fatalf("products indexes %+v %v", td.Indexes, err)
			}
			for _, ix := range td.Indexes {
				want := "sku" // PostgreSQL: lower(sku)
				if ix.Primary {
					want = "id"
				}
				if len(ix.Columns) != 1 || strings.Contains(ix.Columns[0], "CREATE") || !strings.Contains(ix.Columns[0], want) {
					t.Fatalf("%s columns %q", ix.Name, ix.Columns)
				}
			}
			for _, tb := range tables {
				if tb.Name == "products" && tb.Rows != 2 {
					t.Errorf("products estimated rows %d, want 2", tb.Rows)
				}
			}

			cat, err := m.Catalog(ctx, id, e.database)
			if err != nil {
				t.Fatal(err)
			}
			if len(cat.FKs) < 3 || len(cat.Columns) < 15 {
				t.Fatalf("catalog fks=%d cols=%d", len(cat.FKs), len(cat.Columns))
			}

			ddl, err := m.DDL(ctx, id, ref("customers", model.KindTable))
			if err != nil || !strings.Contains(ddl, "CREATE TABLE") || !strings.Contains(ddl, "full_name") {
				t.Fatalf("ddl %q %v", ddl, err)
			}
			t.Logf("DDL:\n%s", ddl)

			// Script with DDL, DML and two result sets.
			sid, err := m.OpenSession(ctx, id, e.database, e.schema)
			if err != nil {
				t.Fatal(err)
			}
			res, err := m.Execute(ctx, sid, "select id, email, avatar from customers order by id;\nupdate products set price = price where id = 1;\nselect * from customer_orders", core.ExecOptions{})
			if err != nil {
				t.Fatal(err)
			}
			if len(res) != 3 || res[0].Error != "" || res[1].Error != "" || res[2].Error != "" {
				t.Fatalf("exec %+v", res)
			}
			page, err := m.FetchRows(res[0].ResultID, 0, 10, 0)
			if err != nil || len(page.Rows) != 3 || *page.Rows[0][1] != "ada@example.com" {
				t.Fatalf("page %+v %v", page, err)
			}
			if a := page.Rows[0][2]; a == nil || !strings.Contains(strings.ToLower(*a), "89504e47") {
				t.Fatalf("binary formatting: %v", deref(a))
			}
			if page.Rows[1][2] != nil {
				t.Fatalf("expected NULL avatar")
			}
			if res[1].RowsAffected != 1 && e.conn.Type != model.MySQL { // MySQL reports 0 for unchanged rows
				t.Fatalf("rows affected %d", res[1].RowsAffected)
			}

			// Errors carry the statement's line.
			res, _ = m.Execute(ctx, sid, "select 1;\n\nselect * from no_such_table;\nselect 2", core.ExecOptions{})
			if len(res) != 2 || res[1].Error == "" || res[1].ErrorLine != 3 {
				t.Fatalf("error handling %+v", res)
			}

			// Large streaming result + cancel of a slow query.
			big := "select a.n * 1000 + b.n as v from (select 0 n union all select 1 union all select 2 union all select 3 union all select 4 union all select 5 union all select 6 union all select 7 union all select 8 union all select 9) a cross join (select 0 n union all select 1 union all select 2 union all select 3 union all select 4 union all select 5 union all select 6 union all select 7 union all select 8 union all select 9) b"
			res, err = m.Execute(ctx, sid, big, core.ExecOptions{})
			if err != nil || res[0].Error != "" {
				t.Fatalf("big %+v %v", res, err)
			}
			page, _ = m.FetchRows(res[0].ResultID, 90, 50, 0)
			if len(page.Rows) != 10 || !page.Complete {
				t.Fatalf("big page %d complete=%v", len(page.Rows), page.Complete)
			}

			sleep := "select sleep(10)"
			if e.conn.Type == model.PostgreSQL {
				sleep = "select pg_sleep(10)"
			}
			go func() { time.Sleep(500 * time.Millisecond); _ = m.Cancel(ctx, sid) }()
			start := time.Now()
			res, _ = m.Execute(ctx, sid, sleep, core.ExecOptions{})
			if time.Since(start) > 5*time.Second {
				t.Fatalf("cancel did not interrupt query")
			}
			// The session must still be usable after a cancel.
			res, err = m.Execute(ctx, sid, "select 42", core.ExecOptions{})
			if err != nil || res[0].Error != "" {
				t.Fatalf("session unusable after cancel: %+v %v", res, err)
			}
			m.CloseSession(sid)

			// Browse with filter and sort, then edit.
			br, err := m.BrowseTable(ctx, id, core.BrowseRequest{Ref: ref("customers", model.KindTable), Where: "id > 1", Sort: []core.SortSpec{{Column: "id", Desc: true}}, Limit: 100})
			if err != nil || br.Error != "" || br.RowCount != 2 {
				t.Fatalf("browse %+v %v", br, err)
			}
			n, err := m.CountRows(ctx, id, ref("customers", model.KindTable), "")
			if err != nil || n != 3 {
				t.Fatalf("count %d %v", n, err)
			}

			email := "kay@example.com"
			name := "Alan Kay"
			newName := "Alan C. Kay"
			if _, err := m.ApplyChanges(ctx, id, ref("customers", model.KindTable), []core.RowChange{
				{Kind: core.ChangeInsert, Values: map[string]*string{"email": &email, "full_name": &name}},
			}); err != nil {
				t.Fatal(err)
			}
			if _, err := m.ApplyChanges(ctx, id, ref("customers", model.KindTable), []core.RowChange{
				{Kind: core.ChangeUpdate, Key: map[string]*string{"email": &email}, Values: map[string]*string{"full_name": &newName}},
				{Kind: core.ChangeDelete, Key: map[string]*string{"email": &email}},
			}); err != nil {
				t.Fatal(err)
			}
			// A failing change rolls back the whole batch.
			bad := "x"
			results, err := m.ApplyChanges(ctx, id, ref("customers", model.KindTable), []core.RowChange{
				{Kind: core.ChangeInsert, Values: map[string]*string{"email": &email, "full_name": &name}},
				{Kind: core.ChangeInsert, Values: map[string]*string{"email": &bad, "full_name": nil}},
			})
			if err == nil {
				t.Fatalf("expected rollback, got %+v", results)
			}
			if n, _ := m.CountRows(ctx, id, ref("customers", model.KindTable), ""); n != 3 {
				t.Fatalf("rollback left %d rows", n)
			}
		})
	}
}

func deref(s *string) string {
	if s == nil {
		return "<nil>"
	}
	return *s
}

func TestIntegrationConnectionFeatures(t *testing.T) {
	for _, e := range engines() {
		t.Run(e.conn.Name, func(t *testing.T) {
			e.conn.Advanced.InitialQueries = "SET @chloe_init = 42"
			if e.conn.Type == model.PostgreSQL {
				e.conn.Advanced.InitialQueries = "SET application_name = 'chloe-init-test'"
			}
			e.conn.Advanced.SessionLimit = 5
			e.conn.Advanced.KeepaliveSec = 1
			e.conn.Databases = model.CustomDatabases{Enabled: true, List: []model.DatabaseEntry{{Name: e.database}}}
			m, id := setup(t, e)
			ctx := context.Background()

			dbs, err := m.ListDatabases(ctx, id)
			if err != nil || len(dbs) != 1 || dbs[0] != e.database {
				t.Fatalf("custom database list: %v %v", dbs, err)
			}

			sid, err := m.OpenSession(ctx, id, e.database, e.schema)
			if err != nil {
				t.Fatal(err)
			}
			check := "SELECT @chloe_init"
			want := "42"
			if e.conn.Type == model.PostgreSQL {
				check, want = "SHOW application_name", "chloe-init-test"
			}
			res, _ := m.Execute(ctx, sid, check, core.ExecOptions{})
			page, _ := m.FetchRows(res[0].ResultID, 0, 1, 0)
			if len(page.Rows) != 1 || page.Rows[0][0] == nil || *page.Rows[0][0] != want {
				t.Fatalf("initial query not applied: %+v", page.Rows)
			}

			// A slow query shows in the process list and can be cancelled from the monitor.
			sleep := "SELECT SLEEP(20)"
			if e.conn.Type == model.PostgreSQL {
				sleep = "SELECT pg_sleep(20)"
			}
			done := make(chan time.Duration, 1)
			go func() {
				start := time.Now()
				m.Execute(ctx, sid, sleep, core.ExecOptions{})
				done <- time.Since(start)
			}()
			var target string
			for i := 0; i < 40 && target == ""; i++ {
				time.Sleep(100 * time.Millisecond)
				procs, err := m.Processes(ctx, id)
				if err != nil {
					t.Fatal(err)
				}
				for _, p := range procs {
					if strings.Contains(strings.ToLower(p.Query), "sleep(20)") && !strings.Contains(p.Query, "pg_stat_activity") {
						target = p.ID
					}
				}
			}
			if target == "" {
				t.Fatal("sleeping query not found in process list")
			}
			if err := m.KillProcess(ctx, id, target, true); err != nil {
				t.Fatal(err)
			}
			select {
			case d := <-done:
				if d > 10*time.Second {
					t.Fatalf("cancel took %v", d)
				}
			case <-time.After(15 * time.Second):
				t.Fatal("query was not cancelled")
			}

			vars, err := m.Variables(ctx, id)
			if err != nil || len(vars) < 50 {
				t.Fatalf("variables %d %v", len(vars), err)
			}
			status, err := m.ServerStatus(ctx, id)
			if err != nil || len(status) < 5 {
				t.Fatalf("status %d %v", len(status), err)
			}
			info, err := m.Info(id)
			if err != nil || info.Version == "" || info.Sessions < 1 {
				t.Fatalf("info %+v %v", info, err)
			}

			// Closing the database ends its editor sessions.
			if err := m.CloseDatabase(id, e.database); err != nil {
				t.Fatal(err)
			}
			if _, err := m.Execute(ctx, sid, "SELECT 1", core.ExecOptions{}); err == nil {
				t.Fatal("session should be closed with its database")
			}
		})
	}
}

// TestIntegrationSSHTunnel connects through the chloe-sshd container, which reaches
// the databases by their compose service names.
func TestIntegrationSSHTunnel(t *testing.T) {
	old := tunnel.PinnedHostsPath
	tunnel.PinnedHostsPath = filepath.Join(t.TempDir(), "known_hosts")
	t.Cleanup(func() { tunnel.PinnedHostsPath = old })
	tunnel.ConfirmHostKey = func(context.Context, tunnel.HostKeyPrompt) bool { return true }
	t.Cleanup(func() { tunnel.ConfirmHostKey = nil })
	key, _ := filepath.Abs("../../testdata/sshd/id_ed25519")
	auths := []model.SSHConfig{
		{Enabled: true, Host: "127.0.0.1", Port: 2222, User: "tunnel", Auth: model.SSHPassword, Password: "chloe", SavePassword: true},
		{Enabled: true, Host: "127.0.0.1", Port: 2222, User: "tunnel", Auth: model.SSHPublicKey, PrivateKeyPath: key, SavePassword: true},
	}
	targets := map[model.DBType]struct {
		host string
		port int
	}{model.MySQL: {"mysql", 3306}, model.PostgreSQL: {"postgres", 5432}}

	for _, e := range engines() {
		for _, ssh := range auths {
			t.Run(e.conn.Name+"/"+string(ssh.Auth), func(t *testing.T) {
				tg := targets[e.conn.Type]
				e.conn.Host, e.conn.Port, e.conn.SSH = tg.host, tg.port, ssh
				m, id := setup(t, e)
				ctx := context.Background()
				info, err := m.Info(id)
				if err != nil || !strings.HasPrefix(info.Via, "SSH tunnel@") {
					t.Fatalf("info %+v %v", info, err)
				}
				sid, err := m.OpenSession(ctx, id, e.database, e.schema)
				if err != nil {
					t.Fatal(err)
				}
				res, err := m.Execute(ctx, sid, "SELECT count(*) FROM customers", core.ExecOptions{})
				if err != nil || res[0].Error != "" {
					t.Fatalf("query over tunnel: %+v %v", res, err)
				}
			})
		}
	}

	t.Run("wrong password", func(t *testing.T) {
		if os.Getenv("CHLOE_IT") == "" {
			t.Skip()
		}
		st, _ := store.Open(t.TempDir(), &store.MemorySecrets{})
		m := core.NewManager(st, nil, core.DefaultSettings)
		c := engines()[1].conn
		c.Host = "postgres"
		c.SSH = model.SSHConfig{Enabled: true, Host: "127.0.0.1", Port: 2222, User: "tunnel", Auth: model.SSHPassword, Password: "nope"}
		_, err := m.TestConnection(context.Background(), c)
		if err == nil || !strings.Contains(err.Error(), "ssh") {
			t.Fatalf("expected ssh auth error, got %v", err)
		}
	})
}

func TestIntegrationSampleColumn(t *testing.T) {
	for _, e := range engines() {
		t.Run(e.conn.Name, func(t *testing.T) {
			m, id := setup(t, e)
			ref := model.ObjectRef{Database: e.database, Schema: e.schema, Name: "products", Kind: model.KindTable}
			vals, err := m.SampleColumn(context.Background(), id, ref, "attrs", 10)
			if err != nil {
				t.Fatal(err)
			}
			if len(vals) != 2 || !strings.Contains(strings.Join(vals, ""), `"dims"`) {
				t.Fatalf("samples %q", vals)
			}
		})
	}
}

func TestIntegrationCatalogEnums(t *testing.T) {
	for _, e := range engines() {
		t.Run(e.conn.Name, func(t *testing.T) {
			m, id := setup(t, e)
			cat, err := m.Catalog(context.Background(), id, e.database)
			if err != nil {
				t.Fatal(err)
			}
			for _, c := range cat.Columns {
				if c.Table == "orders" && c.Name == "status" {
					if strings.Join(c.EnumValues, ",") != "new,paid,shipped" {
						t.Fatalf("enum values %q", c.EnumValues)
					}
					return
				}
			}
			t.Fatal("orders.status not found")
		})
	}
}

// Without "Use SSL", MySQL still encrypts when the server offers TLS (PostgreSQL does
// too, through sslmode=prefer; the Docker server has no certificate to show it).
func TestIntegrationMySQLPrefersTLS(t *testing.T) {
	m, id := setup(t, engines()[0])
	ctx := context.Background()
	sid, err := m.OpenSession(ctx, id, "shop", "")
	if err != nil {
		t.Fatal(err)
	}
	res, err := m.Execute(ctx, sid, "SHOW SESSION STATUS LIKE 'Ssl_cipher'", core.ExecOptions{})
	if err != nil || len(res) != 1 || res[0].Error != "" {
		t.Fatalf("%v %+v", err, res)
	}
	page, err := m.FetchRows(res[0].ResultID, 0, 1, 0)
	if err != nil || len(page.Rows) != 1 || page.Rows[0][1] == nil || *page.Rows[0][1] == "" {
		t.Fatalf("not encrypted: %+v %v", page, err)
	}
}
