package mcpserver

import "testing"

// Export file and folder names stay inside the reports folder and aren't hidden.
func TestFileSafe(t *testing.T) {
	for in, want := range map[string]string{"..": "dashboard", ".": "dashboard", " ": "dashboard", ".hidden": "hidden", "../x": "-x", "a/b": "a-b", "Q3 sales": "Q3 sales", "v1.2": "v1.2"} {
		if got := fileSafe(in); got != want {
			t.Errorf("fileSafe(%q) = %q, want %q", in, got, want)
		}
	}
}
