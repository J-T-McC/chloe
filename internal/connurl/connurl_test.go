package connurl

import (
	"testing"

	"chloe/internal/model"
)

func TestParse(t *testing.T) {
	cases := []struct {
		in    string
		check func(t *testing.T, c model.Connection)
	}{
		{"postgresql://postgres.abcd:s3cr%40t@aws-0-us-east-1.pooler.supabase.com:6543/postgres?sslmode=require&options=-c%20statement_timeout%3D5000", func(t *testing.T, c model.Connection) {
			if c.Type != model.PostgreSQL || c.Host != "aws-0-us-east-1.pooler.supabase.com" || c.Port != 6543 {
				t.Fatalf("%+v", c)
			}
			if c.User != "postgres.abcd" || c.Password != "s3cr@t" || !c.SavePassword {
				t.Fatalf("credentials %q %q", c.User, c.Password)
			}
			if !c.SSL.Enabled || c.SSL.Mode != "require" || c.InitialDatabase != "postgres" {
				t.Fatalf("ssl/db %+v %q", c.SSL, c.InitialDatabase)
			}
			if c.Preset != "supabase" || c.Name != "Supabase" {
				t.Fatalf("preset %q name %q", c.Preset, c.Name)
			}
			if c.Advanced.ConnectionParams != "options=-c statement_timeout=5000" {
				t.Fatalf("params %q", c.Advanced.ConnectionParams)
			}
		}},
		{"jdbc:mysql://db.example.com/shop?user=app&password=pw&sslMode=VERIFY_IDENTITY", func(t *testing.T, c model.Connection) {
			if c.Type != model.MySQL || c.Port != 3306 || c.User != "app" || c.Password != "pw" {
				t.Fatalf("%+v", c)
			}
			if !c.SSL.Enabled || !c.SSL.VerifyHost || c.InitialDatabase != "shop" {
				t.Fatalf("ssl/db %+v %q", c.SSL, c.InitialDatabase)
			}
		}},
		{"postgres://u@h1:5433,h2:5434/app", func(t *testing.T, c model.Connection) {
			if c.Host != "h1" || c.Port != 5433 || c.SSL.Enabled || c.Name != "h1" {
				t.Fatalf("%+v", c)
			}
		}},
		{"mysql://root@aws.connect.psdb.cloud/main?sslaccept=strict", func(t *testing.T, c model.Connection) {
			if c.Preset != "planetscale" || !c.SSL.VerifyHost {
				t.Fatalf("%+v", c)
			}
		}},
	}
	for _, tc := range cases {
		c, err := Parse(tc.in)
		if err != nil {
			t.Fatalf("%s: %v", tc.in, err)
		}
		tc.check(t, c)
	}
	for _, bad := range []string{"", "localhost:5432", "oracle://x", "postgres://h:notaport/db", "mongodb://h:port/db", "mongodb:///db"} {
		if _, err := Parse(bad); err == nil {
			t.Fatalf("expected error for %q", bad)
		}
	}
}

func TestFormatRoundTrip(t *testing.T) {
	c := model.Connection{Type: model.PostgreSQL, Host: "db.example.com", Port: 5432, User: "app", Password: "secret",
		InitialDatabase: "main", SSL: model.SSLConfig{Enabled: true, Mode: "verify-full"}}
	s := Format(c)
	if s != "postgresql://app@db.example.com:5432/main?sslmode=verify-full" {
		t.Fatalf("format %q", s)
	}
	back, err := Parse(s)
	if err != nil || back.Host != c.Host || back.SSL.Mode != "verify-full" || back.InitialDatabase != "main" {
		t.Fatalf("round trip %+v %v", back, err)
	}
}

func TestParseRedis(t *testing.T) {
	c, err := Parse("rediss://default:s3cret@cache.example.com:6380/2")
	if err != nil {
		t.Fatal(err)
	}
	if c.Type != model.Redis || c.Host != "cache.example.com" || c.Port != 6380 || c.User != "" || c.Password != "s3cret" || !c.SSL.Enabled {
		t.Fatalf("%+v", c)
	}
	if got := Format(c); got != "rediss://cache.example.com:6380" {
		t.Fatalf("format %q", got)
	}
	if c, _ := Parse("redis://localhost"); c.Port != 6379 || c.SSL.Enabled {
		t.Fatalf("defaults %+v", c)
	}
}

func TestParseMongo(t *testing.T) {
	c, err := Parse("mongodb://app%40corp:p%40ss@db1:27018,db2,db3:27019/shop?replicaSet=rs0&authSource=admin&readPreference=secondary&tls=true")
	if err != nil {
		t.Fatal(err)
	}
	m := c.Mongo
	if c.Type != model.MongoDB || c.User != "app@corp" || c.Password != "p@ss" || c.Host != "db1" || c.Port != 27018 || c.InitialDatabase != "shop" ||
		m.Topology != "replicaset" || m.ReplicaSet != "rs0" || len(m.Members) != 3 || m.AuthSource != "admin" || m.ReadPreference != "secondary" || !c.SSL.Enabled || m.Auth != "password" {
		t.Errorf("%+v", c)
	}
	if got := Format(c); got != "mongodb://app%40corp@db1:27018,db2,db3:27019/shop?authSource=admin&readPreference=secondary&replicaSet=rs0&tls=true" {
		t.Errorf("format %s", got)
	}
	c, err = Parse("mongodb+srv://cluster0.example.net/?authMechanism=MONGODB-X509")
	if err != nil || !c.Mongo.SRV || c.Host != "cluster0.example.net" || c.Mongo.Auth != "x509" || !c.SSL.Enabled {
		t.Errorf("srv %+v %v", c, err)
	}
	if got := Format(c); got != "mongodb+srv://cluster0.example.net/?authMechanism=MONGODB-X509" {
		t.Errorf("format srv %s", got)
	}
	c, err = Parse("mongodb://localhost")
	if err != nil || c.Port != 27017 || c.Mongo.Auth != "none" || c.Mongo.Topology != "standalone" {
		t.Errorf("plain %+v %v", c, err)
	}
}
