package bi

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A shared workspace file is checked like an edit: a source with a zone that isn't a
// plain name is refused, and query building re-checks the zone in case one slips through.
func TestImportValidatesSources(t *testing.T) {
	l := Library{Dir: t.TempDir()}
	write := func(tz string) string {
		w := New("Shared")
		src := ordersSource()
		src.ID, src.Timezone = "s1", tz
		w.DataSources = []DataSource{src}
		b, err := json.Marshal(w)
		if err != nil {
			t.Fatal(err)
		}
		p := filepath.Join(t.TempDir(), "shared.chloebi")
		if err := os.WriteFile(p, b, 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	if _, err := l.Import(write("Europe/London")); err != nil {
		t.Fatalf("valid workspace: %v", err)
	}
	bad := "UTC'); DELETE FROM orders; --"
	if _, err := l.Import(write(bad)); err == nil || !strings.Contains(err.Error(), "time zone") {
		t.Fatalf("want a time zone error, got %v", err)
	}

	src := ordersSource()
	src.Timezone = bad
	if _, err := PreviewQuery(SQLite, Table(`"orders"`), src, 10); err == nil {
		t.Fatal("query built with an unchecked time zone")
	}
}
