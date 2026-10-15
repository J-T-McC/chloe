package mcpserver

import (
	"fmt"
	"strings"
	"testing"
)

func TestCheckRedisRead(t *testing.T) {
	for _, ok := range [][]string{{"get", "k"}, {"ZRANGE", "z", "0", "-1"}, {"object", "encoding", "k"}, {"XINFO", "STREAM", "s"}, {"MEMORY", "USAGE", "k"}} {
		if err := checkRedisRead(ok); err != nil {
			t.Errorf("%v: %v", ok, err)
		}
	}
	for args, want := range map[string]string{
		"SET k v":         "only runs read commands",
		"SELECT 1":        "as db instead",
		"EVAL_RO x 0":     "only runs read commands",
		"KEYS *":          "redis_scan",
		"OBJECT HELP":     "only with ENCODING",
		"MEMORY DOCTOR":   "only with USAGE",
		"CONFIG GET *":    "server_variables",
		"FLUSHALL":        "only runs read commands",
		"BLPOP q 0":       "only runs read commands",
		"GEORADIUS g 0 0": "only runs read commands",
	} {
		err := checkRedisRead(strings.Fields(args))
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v", args, err)
		}
	}
	if checkRedisRead(nil) == nil {
		t.Error("empty args")
	}
}

func TestSetCommands(t *testing.T) {
	for _, c := range []struct {
		typ  string
		v    any
		want string
		n    int64
	}{
		{"string", "hi", `[[SET k hi]]`, 1},
		{"string", 2.5, `[[SET k 2.5]]`, 1},
		{"hash", map[string]any{"b": "2", "a": 1.0}, `[[HSET k a 1 b 2]]`, 2},
		{"list", []any{"x", "y"}, `[[RPUSH k x y]]`, 2},
		{"set", []any{"x"}, `[[SADD k x]]`, 1},
		{"zset", map[string]any{"m": 3.0, "a": "1.5"}, `[[ZADD k 1.5 a 3 m]]`, 2},
	} {
		cmds, n, err := setCommands("k", c.typ, c.v)
		if err != nil || fmt.Sprint(cmds) != c.want || n != c.n {
			t.Errorf("%s: %v %d %v", c.typ, cmds, n, err)
		}
	}
	for typ, v := range map[string]any{"hash": map[string]any{}, "list": "x", "zset": map[string]any{"m": "high"}, "stream": nil, "json": "{}", "string": nil} {
		if _, _, err := setCommands("k", typ, v); err == nil {
			t.Errorf("%s %v accepted", typ, v)
		}
	}
}

func TestEditCommands(t *testing.T) {
	for _, c := range []struct {
		old  string
		in   RedisEditArgs
		typ  string
		want string
	}{
		{"hash", RedisEditArgs{SetFields: map[string]any{"a": "1"}, DeleteFields: []string{"b"}}, "hash", `[[HSET k a 1] [HDEL k b]]`},
		{"none", RedisEditArgs{Push: []any{"x"}, PushHead: true}, "list", `[[LPUSH k x]]`},
		{"list", RedisEditArgs{Remove: []any{"x", "y"}}, "list", `[[LREM k 0 x] [LREM k 0 y]]`},
		{"set", RedisEditArgs{Add: []any{"a"}, Remove: []any{"b"}}, "set", `[[SADD k a] [SREM k b]]`},
		{"zset", RedisEditArgs{AddScored: map[string]any{"a": 2.0}, Remove: []any{"b"}}, "zset", `[[ZADD k CH 2 a] [ZREM k b]]`},
		{"zset", RedisEditArgs{IncrScores: map[string]any{"a": 1.0}}, "zset", `[[ZINCRBY k 1 a]]`},
		{"none", RedisEditArgs{Incr: 5.0}, "string", `[[INCRBY k 5]]`},
		{"string", RedisEditArgs{Incr: "-0.5"}, "string", `[[INCRBYFLOAT k -0.5]]`},
		{"hash", RedisEditArgs{IncrFields: map[string]any{"n": 2.0}}, "hash", `[[HINCRBY k n 2]]`},
		{"none", RedisEditArgs{AddEntry: map[string]any{"f": "v"}}, "stream", `[[XADD k * f v]]`},
	} {
		typ, cmds, _, err := editCommands("k", c.old, c.in)
		if err != nil || typ != c.typ || fmt.Sprint(cmds) != c.want {
			t.Errorf("%s %+v: %s %v %v", c.old, c.in, typ, cmds, err)
		}
	}
	for _, c := range []struct {
		old  string
		in   RedisEditArgs
		want string
	}{
		{"hash", RedisEditArgs{Push: []any{"x"}}, "is a hash, not a list"},
		{"none", RedisEditArgs{SetFields: map[string]any{"a": "1"}, Push: []any{"x"}}, "one type of value"},
		{"none", RedisEditArgs{}, "nothing to change"},
		{"hash", RedisEditArgs{}, "nothing to change for a hash"},
		{"none", RedisEditArgs{Remove: []any{"x"}}, "no key"},
		{"hash", RedisEditArgs{Remove: []any{"x"}}, "remove works on"},
		{"zset", RedisEditArgs{Add: []any{"x"}}, "is a zset, not a set"},
		{"none", RedisEditArgs{DeleteFields: []string{"a"}}, "no key"},
		{"stream", RedisEditArgs{AddEntry: map[string]any{}}, "at least one field"},
		{"string", RedisEditArgs{Incr: "lots"}, "isn't a number"},
		{"list", RedisEditArgs{Push: []any{map[string]any{}}}, "item 1: values must be strings or numbers, not objects"},
	} {
		_, _, _, err := editCommands("k", c.old, c.in)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s %+v: %v", c.old, c.in, err)
		}
	}
}

func TestRedisText(t *testing.T) {
	if got := redisText("\xff\x01", 100); got != "0xff01" {
		t.Error(got)
	}
	if got := redisText("abcdef", 3); got != "abc…[3 more bytes]" {
		t.Error(got)
	}
	if got := quoteArgs("SET", "k", "a b", "\xff\x00"); got != `SET "k" "a b" "\xff\x00"` {
		t.Error(got)
	}
	if got := showKey("x in db1"); got != `"x in db1"` {
		t.Error(got)
	}
	for secs, want := range map[int64]string{1: "1 second", 90: "90 seconds", 7200: "2 hours", 86400 * 3: "3 days", 99999999999999: "about 3170979 years"} {
		if got := humanDuration(secs); got != want {
			t.Errorf("%d: %s", secs, got)
		}
	}
	if !validCursor("list", "3") || validCursor("list", "-1") || validCursor("set", "abc") || !validCursor("stream", "1700000000000-0") || validCursor("stream", "5") {
		t.Error("validCursor")
	}
	if got := clientFlags("bN"); got != "blocked" {
		t.Error(got)
	}
}
