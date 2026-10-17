package core

import (
	"context"
	"strings"
	"testing"
	"time"

	"chloe/internal/bi"
	"chloe/internal/model"
)

func TestBIResultCacheLimits(t *testing.T) {
	defer func(ttl time.Duration, max int64) { BICacheTTL, maxCachedBytes = ttl, max }(BICacheTTL, maxCachedBytes)
	BICacheTTL, maxCachedBytes = time.Hour, 3000
	m := &Manager{}
	ctx := context.Background()
	rows := func(n int) [][]*string {
		v := strings.Repeat("x", n)
		return [][]*string{{&v}}
	}
	read := func(sql string, size int) bool {
		runs := 0
		_, hit, err := m.cachedResult(ctx, bi.DataSource{ConnID: "c"}, bi.Stmt{SQL: sql}, 10, time.Time{}, func(context.Context) ([]model.ResultColumn, [][]*string, bool, error) {
			runs++
			return nil, rows(size), false, nil
		})
		if err != nil {
			t.Fatal(err)
		}
		return hit
	}
	read("a", 1000)
	read("b", 1000)
	if !read("a", 1000) {
		t.Fatal("a wasn't kept")
	}
	// c goes over the budget: b, the least recently used, makes room.
	read("c", 1000)
	if read("b", 1000) {
		t.Fatal("b survived going over the byte budget")
	}
	// A result bigger than the whole budget isn't kept.
	read("huge", 5000)
	if read("huge", 5000) {
		t.Fatal("a result over the budget was kept")
	}
	m.biCache.mu.Lock()
	if m.biCache.bytes > maxCachedBytes || m.biCache.bytes <= 0 {
		t.Errorf("accounted %d bytes", m.biCache.bytes)
	}
	m.biCache.mu.Unlock()

	// Expired results are swept even when nothing reads.
	BICacheTTL = 50 * time.Millisecond
	m2 := &Manager{}
	m = m2
	read("z", 10)
	time.Sleep(200 * time.Millisecond)
	m2.biCache.mu.Lock()
	n, b := len(m2.biCache.entries), m2.biCache.bytes
	m2.biCache.mu.Unlock()
	if n != 0 || b != 0 {
		t.Fatalf("%d entries (%d bytes) left after they expired", n, b)
	}
}
