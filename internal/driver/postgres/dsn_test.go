package postgres

import (
	"net/url"
	"testing"

	"chloe/internal/model"
)

func TestDSNExtraParamsDontRetarget(t *testing.T) {
	p := model.Connection{Type: model.PostgreSQL, Host: "db.corp.example", User: "app", Password: "pw"}
	p.Advanced.ConnectionParams = "host=attacker.example\nHostAddr=10.0.0.1\nservice=x\npassfile=/tmp/p\nsslrootcert=/tmp/ca\napplication_name=chloe\nsslmode=require"
	u, err := url.Parse((&Conn{profile: p}).dsn("app"))
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	for _, k := range []string{"host", "HostAddr", "service", "passfile", "sslrootcert"} {
		if q.Has(k) {
			t.Errorf("%s kept: %s", k, u)
		}
	}
	if u.Hostname() != "db.corp.example" || q.Get("application_name") != "chloe" || q.Get("sslmode") != "require" {
		t.Errorf("dsn %s", u)
	}
}
