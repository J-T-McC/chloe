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

// One 16 MB statement: reads double while it's unfinished, so it is scanned a
// few times, not once per chunk.
func BenchmarkScanLongStatement(b *testing.B) {
	script := "insert into t values " + strings.Repeat("(1, 'abc'), ", 16<<20/12) + "(2, 'x');"
	for b.Loop() {
		s := NewScanner(strings.NewReader(script), MySQL)
		for _, ok := s.Next(); ok; _, ok = s.Next() {
		}
	}
}

func BenchmarkScanManyStatements(b *testing.B) {
	script := strings.Repeat("insert into t values (1, 'a;b');\n", 160000)
	for b.Loop() {
		s := NewScanner(strings.NewReader(script), MySQL)
		for _, ok := s.Next(); ok; _, ok = s.Next() {
		}
	}
}
