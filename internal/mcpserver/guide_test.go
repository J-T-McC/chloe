package mcpserver_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"chloe/internal/store"
)

func TestInstructionsFollowTheEndpoint(t *testing.T) {
	f := setup(t, store.MCPConnAccess{Level: store.MCPRead})
	text := f.client.InitializeResult().Instructions
	for _, want := range []string{"describe_table", "EXPLAIN", "read-only: there are no write tools", "give the user the SQL", "more on: sql."} {
		if !strings.Contains(text, want) {
			t.Errorf("read-only instructions lack %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "Reports") || strings.Contains(text, "approve") {
		t.Errorf("read-only instructions mention what the endpoint doesn't have:\n%s", text)
	}
	out := f.call(t, "guide", map[string]any{"topic": "sql"}, "")
	if !strings.Contains(out["text"].(string), "EXPLAIN QUERY PLAN") {
		t.Fatalf("sql topic: %v", out)
	}
	f.call(t, "guide", map[string]any{"topic": "reports"}, "this endpoint has sql")
	// The plan the guide gives for SQLite runs.
	if r := rows(f.call(t, "run_query", map[string]any{"connection": "shop", "sql": "EXPLAIN QUERY PLAN SELECT * FROM customers WHERE id = 1"}, "")); len(r) == 0 {
		t.Fatal("EXPLAIN QUERY PLAN returned nothing")
	}

	// A new session sees the endpoint's new settings.
	st := f.srv.Settings()
	st.Endpoints[0].Access[f.conn] = store.MCPConnAccess{Level: store.MCPWrite}
	st.Endpoints[0].Capabilities = store.MCPCapabilities{Reports: true, SavedQueries: true, Models: true}
	if err := f.srv.Save(st); err != nil {
		t.Fatal(err)
	}
	g := serve(t, f.m, f.conn, st.Endpoints[0])
	text = g.client.InitializeResult().Instructions
	for _, want := range []string{"approve each call", "one approval", "open_query_tab", "guide topic reports", "generate_sql", "more on: sql, writes, reports, models."} {
		if !strings.Contains(text, want) {
			t.Errorf("instructions lack %q:\n%s", want, text)
		}
	}
	out = g.call(t, "guide", map[string]any{"topic": " Reports "}, "")
	if out["topic"] != "reports" || !strings.Contains(out["text"].(string), "share one read") {
		t.Fatalf("reports topic: %v", out)
	}

	st.Endpoints[0].Danger = true
	g = serve(t, f.m, f.conn, st.Endpoints[0])
	if text = g.client.InitializeResult().Instructions; !strings.Contains(text, "danger mode") || strings.Contains(text, "approve each call") {
		t.Errorf("danger mode instructions:\n%s", text)
	}
}

// Every topic file is offered somewhere, and every topic has a file.
func TestGuideTopicsHaveFiles(t *testing.T) {
	var files []string
	fs.WalkDir(os.DirFS("guide"), ".", func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			files = append(files, strings.TrimSuffix(filepath.Base(p), ".md"))
		}
		return nil
	})
	slices.Sort(files)
	want := []string{"models", "mongodb", "redis", "reports", "sql", "writes"}
	if !slices.Equal(files, want) {
		t.Fatalf("guide files %v, topics %v", files, want)
	}
}
