// Package rdb is Chloe's Redis client: key browsing, typed value reads and edits,
// a command console and server information. Values may be binary, so they travel
// as Value (text, or base64 when the bytes aren't valid UTF-8).
package rdb

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/redis/go-redis/v9"

	"chloe/internal/model"
	"chloe/internal/tlsconf"
	"chloe/internal/tunnel"
)

// Value is a Redis string: text, or base64 when Base64 is set.
type Value struct {
	Text   string `json:"text"`
	Base64 bool   `json:"base64,omitempty"`
}

// Enc wraps raw bytes for the UI.
func Enc(s string) Value {
	if utf8.ValidString(s) {
		return Value{Text: s}
	}
	return Value{Text: base64.StdEncoding.EncodeToString([]byte(s)), Base64: true}
}

// Bytes returns the raw bytes of v.
func (v Value) Bytes() (string, error) {
	if !v.Base64 {
		return v.Text, nil
	}
	b, err := base64.StdEncoding.DecodeString(v.Text)
	return string(b), err
}

// Client is an open Redis connection profile. Each logical database gets its own
// small pool, created on first use.
type Client struct {
	opts    redis.Options
	mu      sync.Mutex
	dbs     map[int]*redis.Client
	version string
}

// Open connects and checks the server answers.
func Open(ctx context.Context, c model.Connection, d tunnel.Dialer) (*Client, error) {
	opts := redis.Options{
		Addr:         net.JoinHostPort(c.Host, strconv.Itoa(c.Port)),
		Username:     c.User,
		Password:     c.Password,
		PoolSize:     4,
		MinIdleConns: 0,
		Dialer:       func(ctx context.Context, network, addr string) (net.Conn, error) { return d.DialContext(ctx, network, addr) },
		DialTimeout:  secs(c.Advanced.ConnectTimeoutSec, 15),
		ReadTimeout:  secs(c.Advanced.ReadTimeoutSec, 30),
		WriteTimeout: secs(c.Advanced.WriteTimeoutSec, 30),
		// go-redis would otherwise send CLIENT SETINFO, which older servers reject.
		DisableIdentity: true,
		ClientName:      "Chloe",
	}
	if c.Advanced.SessionLimit > 0 {
		opts.PoolSize = c.Advanced.SessionLimit
	}
	if c.SSL.Enabled {
		tc, err := tlsconf.Config(c.SSL, c.Host)
		if err != nil {
			return nil, err
		}
		opts.TLSConfig = tc
	}
	cl := &Client{opts: opts, dbs: map[int]*redis.Client{}}
	info, err := cl.db(0).Info(ctx, "server").Result()
	if err != nil {
		cl.Close()
		return nil, err
	}
	cl.version = "Redis " + infoField(info, "redis_version")
	if v := infoField(info, "valkey_version"); v != "" {
		cl.version = "Valkey " + v
	}
	return cl, nil
}

func secs(n, def int) time.Duration {
	if n <= 0 {
		n = def
	}
	return time.Duration(n) * time.Second
}

func infoField(info, name string) string {
	for _, l := range strings.Split(info, "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(l), name+":"); ok {
			return v
		}
	}
	return ""
}

func (c *Client) db(n int) *redis.Client {
	c.mu.Lock()
	defer c.mu.Unlock()
	if cl, ok := c.dbs[n]; ok {
		return cl
	}
	o := c.opts
	o.DB = n
	cl := redis.NewClient(&o)
	c.dbs[n] = cl
	return cl
}

// DB returns the pooled client of logical database n.
func (c *Client) DB(n int) *redis.Client { return c.db(n) }

func (c *Client) ServerVersion() string { return c.version }

// OpenConnections counts pooled connections across databases.
func (c *Client) OpenConnections() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for _, cl := range c.dbs {
		n += int(cl.PoolStats().TotalConns)
	}
	return n
}

func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, cl := range c.dbs {
		cl.Close()
	}
	c.dbs = map[int]*redis.Client{}
	return nil
}

// ---- databases and keys ------------------------------------------------------------

// DBInfo is one logical database.
type DBInfo struct {
	Index int   `json:"index"`
	Keys  int64 `json:"keys"`
}

// Databases lists the logical databases with their key counts. Servers that hide
// CONFIG (managed services) are assumed to have 16.
func (c *Client) Databases(ctx context.Context) ([]DBInfo, error) {
	n := 16
	if cfg, err := c.db(0).ConfigGet(ctx, "databases").Result(); err == nil {
		if v, err := strconv.Atoi(cfg["databases"]); err == nil && v > 0 {
			n = v
		}
	}
	info, err := c.db(0).Info(ctx, "keyspace").Result()
	if err != nil {
		return nil, err
	}
	counts := map[int]int64{}
	for _, l := range strings.Split(info, "\n") {
		l = strings.TrimSpace(l)
		name, rest, ok := strings.Cut(l, ":")
		if !ok || !strings.HasPrefix(name, "db") {
			continue
		}
		idx, err := strconv.Atoi(name[2:])
		if err != nil {
			continue
		}
		for _, kv := range strings.Split(rest, ",") {
			if v, ok := strings.CutPrefix(kv, "keys="); ok {
				counts[idx], _ = strconv.ParseInt(v, 10, 64)
			}
		}
	}
	out := make([]DBInfo, n)
	for i := range out {
		out[i] = DBInfo{Index: i, Keys: counts[i]}
	}
	return out, nil
}

// Key is a key as the browser lists it.
type Key struct {
	Name Value  `json:"name"`
	Type string `json:"type"`
	// TTL in seconds; -1 means no expiry.
	TTL int64 `json:"ttl"`
}

// ScanResult is one batch of a key scan.
type ScanResult struct {
	Keys []Key `json:"keys"`
	// Cursor continues the scan; "0" means it is complete.
	Cursor string `json:"cursor"`
}

// ScanKeys runs SCAN from cursor until about count keys are found (or the scan
// ends), then looks up each key's type and TTL in one pipeline.
func (c *Client) ScanKeys(ctx context.Context, db int, cursor, pattern, typ string, count int) (ScanResult, error) {
	cl := c.db(db)
	if pattern == "" {
		pattern = "*"
	}
	if count <= 0 {
		count = 1000
	}
	cur, _ := strconv.ParseUint(cursor, 10, 64)
	var names []string
	deadline := time.Now().Add(2 * time.Second)
	for {
		var batch []string
		var err error
		if typ != "" {
			batch, cur, err = cl.ScanType(ctx, cur, pattern, int64(count), typ).Result()
		} else {
			batch, cur, err = cl.Scan(ctx, cur, pattern, int64(count)).Result()
		}
		if err != nil {
			return ScanResult{}, err
		}
		names = append(names, batch...)
		// Sparse matches can take many SCAN rounds; return what we have in time.
		if cur == 0 || len(names) >= count || time.Now().After(deadline) {
			break
		}
	}
	res := ScanResult{Cursor: strconv.FormatUint(cur, 10), Keys: make([]Key, len(names))}
	if len(names) == 0 {
		return res, nil
	}
	pipe := cl.Pipeline()
	types := make([]*redis.StatusCmd, len(names))
	ttls := make([]*redis.DurationCmd, len(names))
	for i, n := range names {
		types[i] = pipe.Type(ctx, n)
		ttls[i] = pipe.TTL(ctx, n)
	}
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return res, err
	}
	for i, n := range names {
		res.Keys[i] = Key{Name: Enc(n), Type: types[i].Val(), TTL: ttlSeconds(ttls[i].Val())}
	}
	return res, nil
}

func ttlSeconds(d time.Duration) int64 {
	if d < 0 {
		return -1 // -1 no expiry; -2 (gone) is shown as none too
	}
	return int64(d / time.Second)
}

// ---- reading values -------------------------------------------------------------------

// Field is a hash field, a list item (Index set), a set member, a sorted set member
// (Score set) or a stream entry field.
type Field struct {
	Index *int64   `json:"index,omitempty"`
	Field *Value   `json:"field,omitempty"`
	Value Value    `json:"value"`
	Score *float64 `json:"score,omitempty"`
}

// Entry is a stream entry.
type Entry struct {
	ID     string  `json:"id"`
	Fields []Field `json:"fields"`
}

// KeyValue is a key's metadata and (a page of) its value.
type KeyValue struct {
	Name     Value  `json:"name"`
	Type     string `json:"type"`
	TTL      int64  `json:"ttl"`
	Encoding string `json:"encoding,omitempty"`
	// Size is MEMORY USAGE in bytes (0 when unavailable).
	Size int64 `json:"size"`
	// Length is the element count (bytes for strings).
	Length int64 `json:"length"`

	String  *Value  `json:"string,omitempty"`
	Items   []Field `json:"items,omitempty"`
	Entries []Entry `json:"entries,omitempty"`
	// Cursor continues paging items/entries; "" when everything is loaded.
	Cursor string `json:"cursor"`
}

// MaxString caps how much of a string value is loaded.
const MaxString = 4 << 20

var ErrNoKey = errors.New("the key no longer exists")

// GetValue reads a key. For collections it returns up to count items starting
// at cursor ("" for the start).
func (c *Client) GetValue(ctx context.Context, db int, key Value, cursor string, count int) (KeyValue, error) {
	cl := c.db(db)
	k, err := key.Bytes()
	if err != nil {
		return KeyValue{}, err
	}
	if count <= 0 {
		count = 500
	}
	typ, err := cl.Type(ctx, k).Result()
	if err != nil {
		return KeyValue{}, err
	}
	if typ == "none" {
		return KeyValue{}, ErrNoKey
	}
	kv := KeyValue{Name: key, Type: typ}
	pipe := cl.Pipeline()
	ttl := pipe.TTL(ctx, k)
	enc := pipe.ObjectEncoding(ctx, k)
	mem := pipe.MemoryUsage(ctx, k)
	_, _ = pipe.Exec(ctx) // MEMORY/OBJECT may be disabled; their errors are ignored
	kv.TTL, kv.Encoding, kv.Size = ttlSeconds(ttl.Val()), enc.Val(), mem.Val()

	switch typ {
	case "string":
		n, err := cl.StrLen(ctx, k).Result()
		if err != nil {
			return kv, err
		}
		kv.Length = n
		end := int64(-1)
		if n > MaxString {
			end = MaxString - 1
		}
		s, err := cl.GetRange(ctx, k, 0, end).Result()
		if err != nil {
			return kv, err
		}
		v := Enc(s)
		kv.String = &v
	case "hash":
		kv.Length, _ = cl.HLen(ctx, k).Result()
		cur, _ := strconv.ParseUint(cursor, 10, 64)
		pairs, next, err := cl.HScan(ctx, k, cur, "*", int64(count)).Result()
		if err != nil {
			return kv, err
		}
		for i := 0; i+1 < len(pairs); i += 2 {
			f := Enc(pairs[i])
			kv.Items = append(kv.Items, Field{Field: &f, Value: Enc(pairs[i+1])})
		}
		kv.Cursor = scanCursor(next)
	case "set":
		kv.Length, _ = cl.SCard(ctx, k).Result()
		cur, _ := strconv.ParseUint(cursor, 10, 64)
		members, next, err := cl.SScan(ctx, k, cur, "*", int64(count)).Result()
		if err != nil {
			return kv, err
		}
		for _, m := range members {
			kv.Items = append(kv.Items, Field{Value: Enc(m)})
		}
		kv.Cursor = scanCursor(next)
	case "list":
		kv.Length, _ = cl.LLen(ctx, k).Result()
		start, _ := strconv.ParseInt(cursor, 10, 64)
		vals, err := cl.LRange(ctx, k, start, start+int64(count)-1).Result()
		if err != nil {
			return kv, err
		}
		for i, v := range vals {
			idx := start + int64(i)
			kv.Items = append(kv.Items, Field{Index: &idx, Value: Enc(v)})
		}
		if next := start + int64(len(vals)); next < kv.Length {
			kv.Cursor = strconv.FormatInt(next, 10)
		}
	case "zset":
		kv.Length, _ = cl.ZCard(ctx, k).Result()
		start, _ := strconv.ParseInt(cursor, 10, 64)
		zs, err := cl.ZRangeWithScores(ctx, k, start, start+int64(count)-1).Result()
		if err != nil {
			return kv, err
		}
		for i, z := range zs {
			score := z.Score
			idx := start + int64(i)
			kv.Items = append(kv.Items, Field{Index: &idx, Value: Enc(fmt.Sprint(z.Member)), Score: &score})
		}
		if next := start + int64(len(zs)); next < kv.Length {
			kv.Cursor = strconv.FormatInt(next, 10)
		}
	case "stream":
		kv.Length, _ = cl.XLen(ctx, k).Result()
		from := "-"
		if cursor != "" {
			from = "(" + cursor // exclusive: after the last entry shown
		}
		msgs, err := cl.XRangeN(ctx, k, from, "+", int64(count)).Result()
		if err != nil {
			return kv, err
		}
		for _, m := range msgs {
			e := Entry{ID: m.ID}
			for f, v := range m.Values {
				fv := Enc(f)
				e.Fields = append(e.Fields, Field{Field: &fv, Value: Enc(fmt.Sprint(v))})
			}
			sortFields(e.Fields)
			kv.Entries = append(kv.Entries, e)
		}
		if len(msgs) == count {
			kv.Cursor = msgs[len(msgs)-1].ID
		}
	default:
		// Module types (ReJSON etc.) are listed but not editable here.
	}
	return kv, nil
}

func scanCursor(next uint64) string {
	if next == 0 {
		return ""
	}
	return strconv.FormatUint(next, 10)
}

func sortFields(fs []Field) {
	for i := 1; i < len(fs); i++ {
		for j := i; j > 0 && fs[j].Field.Text < fs[j-1].Field.Text; j-- {
			fs[j], fs[j-1] = fs[j-1], fs[j]
		}
	}
}
