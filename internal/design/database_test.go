package design

import (
	"reflect"
	"testing"
)

func TestDatabaseSQL(t *testing.T) {
	ten := 10
	cases := []struct {
		dialect string
		old     *Database
		d       Database
		want    []string
		err     bool
	}{
		{"mysql", nil, Database{Name: "shop"}, []string{"CREATE DATABASE `shop`"}, false},
		{"mysql", nil, Database{Name: "shop", Charset: "utf8mb4", Collation: "utf8mb4_bin"}, []string{"CREATE DATABASE `shop` CHARACTER SET utf8mb4 COLLATE utf8mb4_bin"}, false},
		{"mysql", &Database{Name: "shop", Charset: "latin1"}, Database{Name: "shop", Charset: "utf8mb4"}, []string{"ALTER DATABASE `shop` CHARACTER SET utf8mb4"}, false},
		{"mysql", &Database{Name: "shop"}, Database{Name: "shop"}, nil, false},
		{"mysql", &Database{Name: "a"}, Database{Name: "b"}, nil, true},
		{"mysql", nil, Database{Name: " "}, nil, true},
		{"postgres", nil, Database{Name: "app", Owner: "bob", Template: "template0", Encoding: "UTF8", ConnLimit: &ten, Comment: "it's mine"},
			[]string{"CREATE DATABASE \"app\"\n  OWNER \"bob\"\n  TEMPLATE \"template0\"\n  ENCODING 'UTF8'\n  CONNECTION LIMIT 10", "COMMENT ON DATABASE \"app\" IS 'it''s mine'"}, false},
		{"postgres", &Database{Name: "app", Owner: "bob", ConnLimit: &ten, Comment: "x"}, Database{Name: "app2", Owner: "alice", Comment: ""},
			[]string{`ALTER DATABASE "app" RENAME TO "app2"`, `ALTER DATABASE "app2" OWNER TO "alice"`, `ALTER DATABASE "app2" CONNECTION LIMIT -1`, `COMMENT ON DATABASE "app2" IS NULL`}, false},
		{"postgres", &Database{Name: "app", Encoding: "UTF8"}, Database{Name: "app", Encoding: "LATIN1"}, nil, true},
	}
	for i, c := range cases {
		got, err := DatabaseSQL(c.dialect, c.old, c.d)
		if (err != nil) != c.err {
			t.Errorf("%d: err %v", i, err)
			continue
		}
		if !c.err && !reflect.DeepEqual(got, c.want) {
			t.Errorf("%d:\n got %q\nwant %q", i, got, c.want)
		}
	}
}
