package rdb

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// NewKey describes a key to create with its first value.
type NewKey struct {
	Name Value  `json:"name"`
	Type string `json:"type"` // string, hash, list, set, zset, stream
	// Value: the string, list item, set member, sorted set member or hash/stream value.
	Value Value `json:"value"`
	// Field: hash field or stream field.
	Field Value   `json:"field"`
	Score float64 `json:"score"`
	// TTL in seconds; 0 for none.
	TTL int64 `json:"ttl"`
}

// CreateKey creates a key; it fails if the key already exists.
func (c *Client) CreateKey(ctx context.Context, db int, nk NewKey) error {
	cl := c.db(db)
	k, err := nk.Name.Bytes()
	if err != nil {
		return err
	}
	if k == "" {
		return fmt.Errorf("enter a key name")
	}
	if n, err := cl.Exists(ctx, k).Result(); err != nil {
		return err
	} else if n > 0 {
		return fmt.Errorf("key %s already exists", k)
	}
	v, err := nk.Value.Bytes()
	if err != nil {
		return err
	}
	f, err := nk.Field.Bytes()
	if err != nil {
		return err
	}
	_, err = cl.TxPipelined(ctx, func(p redis.Pipeliner) error {
		switch nk.Type {
		case "string":
			p.Set(ctx, k, v, 0)
		case "hash":
			p.HSet(ctx, k, f, v)
		case "list":
			p.RPush(ctx, k, v)
		case "set":
			p.SAdd(ctx, k, v)
		case "zset":
			p.ZAdd(ctx, k, redis.Z{Score: nk.Score, Member: v})
		case "stream":
			p.XAdd(ctx, &redis.XAddArgs{Stream: k, Values: []string{f, v}})
		default:
			return fmt.Errorf("unsupported key type %q", nk.Type)
		}
		if nk.TTL > 0 {
			p.Expire(ctx, k, time.Duration(nk.TTL)*time.Second)
		}
		return nil
	})
	return err
}

// DeleteKeys removes keys (UNLINK, so large values are freed in the background).
func (c *Client) DeleteKeys(ctx context.Context, db int, keys []Value) (int64, error) {
	ks := make([]string, len(keys))
	for i, k := range keys {
		b, err := k.Bytes()
		if err != nil {
			return 0, err
		}
		ks[i] = b
	}
	return c.db(db).Unlink(ctx, ks...).Result()
}

// RenameKey renames a key without overwriting an existing one.
func (c *Client) RenameKey(ctx context.Context, db int, key, to Value) error {
	k, err := key.Bytes()
	if err != nil {
		return err
	}
	t, err := to.Bytes()
	if err != nil {
		return err
	}
	ok, err := c.db(db).RenameNX(ctx, k, t).Result()
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("key %s already exists", t)
	}
	return nil
}

// SetTTL sets the expiry in seconds; ttl <= 0 removes it.
func (c *Client) SetTTL(ctx context.Context, db int, key Value, ttl int64) error {
	k, err := key.Bytes()
	if err != nil {
		return err
	}
	cl := c.db(db)
	if ttl <= 0 {
		return cl.Persist(ctx, k).Err()
	}
	return cl.Expire(ctx, k, time.Duration(ttl)*time.Second).Err()
}

// SetString replaces a string value, keeping its TTL.
func (c *Client) SetString(ctx context.Context, db int, key, value Value) error {
	k, err := key.Bytes()
	if err != nil {
		return err
	}
	v, err := value.Bytes()
	if err != nil {
		return err
	}
	return c.db(db).SetArgs(ctx, k, v, redis.SetArgs{KeepTTL: true}).Err()
}

// ItemEdit changes one element of a collection.
//
//	hash:   set Field to Value (OldField renames the field)
//	list:   set the element at Index to Value
//	set:    replace member OldValue with Value (or add when OldValue is nil)
//	zset:   set Value's Score (OldValue renames the member)
type ItemEdit struct {
	Field    *Value   `json:"field,omitempty"`
	OldField *Value   `json:"oldField,omitempty"`
	Index    *int64   `json:"index,omitempty"`
	Value    Value    `json:"value"`
	OldValue *Value   `json:"oldValue,omitempty"`
	Score    *float64 `json:"score,omitempty"`
}

// EditItem applies one element change atomically.
func (c *Client) EditItem(ctx context.Context, db int, key Value, typ string, e ItemEdit) error {
	k, err := key.Bytes()
	if err != nil {
		return err
	}
	v, err := e.Value.Bytes()
	if err != nil {
		return err
	}
	cl := c.db(db)
	old := func(p *Value) (string, bool, error) {
		if p == nil {
			return "", false, nil
		}
		b, err := p.Bytes()
		return b, true, err
	}
	switch typ {
	case "hash":
		if e.Field == nil {
			return fmt.Errorf("missing field")
		}
		f, err := e.Field.Bytes()
		if err != nil {
			return err
		}
		of, renamed, err := old(e.OldField)
		if err != nil {
			return err
		}
		_, err = cl.TxPipelined(ctx, func(p redis.Pipeliner) error {
			if renamed && of != f {
				p.HDel(ctx, k, of)
			}
			p.HSet(ctx, k, f, v)
			return nil
		})
		return err
	case "list":
		if e.Index == nil {
			return cl.RPush(ctx, k, v).Err()
		}
		return cl.LSet(ctx, k, *e.Index, v).Err()
	case "set":
		ov, replacing, err := old(e.OldValue)
		if err != nil {
			return err
		}
		_, err = cl.TxPipelined(ctx, func(p redis.Pipeliner) error {
			if replacing && ov != v {
				p.SRem(ctx, k, ov)
			}
			p.SAdd(ctx, k, v)
			return nil
		})
		return err
	case "zset":
		score := 0.0
		if e.Score != nil {
			score = *e.Score
		}
		ov, renamed, err := old(e.OldValue)
		if err != nil {
			return err
		}
		_, err = cl.TxPipelined(ctx, func(p redis.Pipeliner) error {
			if renamed && ov != v {
				p.ZRem(ctx, k, ov)
			}
			p.ZAdd(ctx, k, redis.Z{Score: score, Member: v})
			return nil
		})
		return err
	}
	return fmt.Errorf("can't edit %s values", typ)
}

// ItemRef identifies elements to delete: hash fields, list indexes, set/zset
// members or stream entry IDs.
type ItemRef struct {
	Field *Value `json:"field,omitempty"`
	Index *int64 `json:"index,omitempty"`
	Value *Value `json:"value,omitempty"`
	ID    string `json:"id,omitempty"`
}

// listTombstone marks list elements for removal by index (Redis can only remove by value).
const listTombstone = "\x00__chloe_deleted__\x00"

// DeleteItems removes elements from a collection. When the last element goes,
// Redis deletes the key.
func (c *Client) DeleteItems(ctx context.Context, db int, key Value, typ string, items []ItemRef) error {
	k, err := key.Bytes()
	if err != nil {
		return err
	}
	cl := c.db(db)
	vals := func(get func(ItemRef) *Value) ([]any, error) {
		out := make([]any, 0, len(items))
		for _, it := range items {
			p := get(it)
			if p == nil {
				return nil, fmt.Errorf("missing element")
			}
			b, err := p.Bytes()
			if err != nil {
				return nil, err
			}
			out = append(out, b)
		}
		return out, nil
	}
	switch typ {
	case "hash":
		fs, err := vals(func(i ItemRef) *Value { return i.Field })
		if err != nil {
			return err
		}
		names := make([]string, len(fs))
		for i, f := range fs {
			names[i] = f.(string)
		}
		return cl.HDel(ctx, k, names...).Err()
	case "set":
		ms, err := vals(func(i ItemRef) *Value { return i.Value })
		if err != nil {
			return err
		}
		return cl.SRem(ctx, k, ms...).Err()
	case "zset":
		ms, err := vals(func(i ItemRef) *Value { return i.Value })
		if err != nil {
			return err
		}
		return cl.ZRem(ctx, k, ms...).Err()
	case "stream":
		ids := make([]string, len(items))
		for i, it := range items {
			ids[i] = it.ID
		}
		return cl.XDel(ctx, k, ids...).Err()
	case "list":
		_, err := cl.TxPipelined(ctx, func(p redis.Pipeliner) error {
			for _, it := range items {
				if it.Index == nil {
					return fmt.Errorf("missing index")
				}
				p.LSet(ctx, k, *it.Index, listTombstone)
			}
			p.LRem(ctx, k, 0, listTombstone)
			return nil
		})
		return err
	}
	return fmt.Errorf("can't delete items from %s values", typ)
}

// PushList adds values at the head or tail of a list.
func (c *Client) PushList(ctx context.Context, db int, key Value, values []Value, head bool) error {
	k, err := key.Bytes()
	if err != nil {
		return err
	}
	vs := make([]any, len(values))
	for i, v := range values {
		if vs[i], err = v.Bytes(); err != nil {
			return err
		}
	}
	if head {
		return c.db(db).LPush(ctx, k, vs...).Err()
	}
	return c.db(db).RPush(ctx, k, vs...).Err()
}

// AddStreamEntry appends an entry (id "*" or empty for auto).
func (c *Client) AddStreamEntry(ctx context.Context, db int, key Value, id string, fields []Field) (string, error) {
	k, err := key.Bytes()
	if err != nil {
		return "", err
	}
	if id == "" {
		id = "*"
	}
	vals := make([]string, 0, len(fields)*2)
	for _, f := range fields {
		if f.Field == nil {
			return "", fmt.Errorf("missing field name")
		}
		fn, err := f.Field.Bytes()
		if err != nil {
			return "", err
		}
		fv, err := f.Value.Bytes()
		if err != nil {
			return "", err
		}
		vals = append(vals, fn, fv)
	}
	return c.db(db).XAdd(ctx, &redis.XAddArgs{Stream: k, ID: id, Values: vals}).Result()
}

// FlushDB deletes every key in a database.
func (c *Client) FlushDB(ctx context.Context, db int) error {
	return c.db(db).FlushDB(ctx).Err()
}

// ---- server -------------------------------------------------------------------------

// Info returns INFO as sections of name/value pairs, in server order.
type InfoSection struct {
	Name   string     `json:"name"`
	Fields [][]string `json:"fields"`
}

func (c *Client) Info(ctx context.Context) ([]InfoSection, error) {
	s, err := c.db(0).Info(ctx, "everything").Result()
	if err != nil {
		return nil, err
	}
	var out []InfoSection
	for _, l := range strings.Split(s, "\n") {
		l = strings.TrimSpace(l)
		if name, ok := strings.CutPrefix(l, "# "); ok {
			out = append(out, InfoSection{Name: name})
			continue
		}
		k, v, ok := strings.Cut(l, ":")
		if !ok || len(out) == 0 {
			continue
		}
		out[len(out)-1].Fields = append(out[len(out)-1].Fields, []string{k, v})
	}
	return out, nil
}

// Client is one entry of CLIENT LIST.
type ClientInfo map[string]string

func (c *Client) Clients(ctx context.Context) ([]ClientInfo, error) {
	s, err := c.db(0).ClientList(ctx).Result()
	if err != nil {
		return nil, err
	}
	var out []ClientInfo
	for _, l := range strings.Split(strings.TrimSpace(s), "\n") {
		ci := ClientInfo{}
		for _, kv := range strings.Fields(l) {
			if k, v, ok := strings.Cut(kv, "="); ok {
				ci[k] = v
			}
		}
		if len(ci) > 0 {
			out = append(out, ci)
		}
	}
	return out, nil
}

func (c *Client) KillClient(ctx context.Context, id string) error {
	n, err := strconv.ParseInt(id, 10, 64)
	if err != nil {
		return fmt.Errorf("bad client id %q", id)
	}
	return c.db(0).ClientKillByFilter(ctx, "ID", strconv.FormatInt(n, 10)).Err()
}

// Config returns CONFIG GET * sorted by name.
func (c *Client) Config(ctx context.Context) (map[string]string, error) {
	return c.db(0).ConfigGet(ctx, "*").Result()
}
