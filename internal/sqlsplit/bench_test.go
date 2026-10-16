package sqlsplit

import (
	"strings"
	"testing"
)

// A 7 MB script; each statement after the first starts at the comment above it.
func BenchmarkSplitLargeScript(b *testing.B) {
	script := strings.Repeat("INSERT INTO t VALUES (1, 'a;b');\n-- note\n", 160000)
	b.ResetTimer()
	for b.Loop() {
		if got := Split(script, MySQL); len(got) != 160000 || got[len(got)-1].StartLine != 2*160000-2 {
			b.Fatalf("got %d statements, last at line %d", len(got), got[len(got)-1].StartLine)
		}
	}
}
