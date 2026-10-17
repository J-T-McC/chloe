package mcpserver

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/redis/go-redis/v9"

	"chloe/internal/model"
	"chloe/internal/rdb"
	"chloe/internal/store"
)

// Redis tools. Access is per logical database, named by its number ("0", "1"…).
// Writes are built from fixed commands with the agent's keys and values as
// arguments; agents never send a raw write command.

const (
	maxRedisString = 64 << 10
	maxRedisKeys   = 1000
	maxRedisShown  = 20 // commands listed in an approval
)

var redisEngines = []model.DBType{model.Redis}

// redisTarget is a resolved Redis connection and database.
type redisTarget struct {
	conn model.Connection
	db   int
	cl   *redis.Client
	kv   *rdb.Client
}

func (t redisTarget) dbName() string { return "db" + strconv.Itoa(t.db) }

// redisDB finds a Redis connection and database the endpoint may use at need,
// opening the connection if necessary.
func (c *call) redisDB(ctx context.Context, connection string, db int, need store.MCPLevel) (redisTarget, error) {
	conn, a, err := c.connectionOf(connection, redisEngines)
	if err != nil {
		return redisTarget{}, err
	}
	c.act.Connection, c.act.Database = conn.Name, "db"+strconv.Itoa(db)
	if db < 0 {
		return redisTarget{}, errors.New("db is a database number (0, 1…)")
	}
	have := a.db(strconv.Itoa(db))
	if have == store.MCPOff {
		return redisTarget{}, fmt.Errorf("database %d of %s isn't shared with this endpoint (see redis_databases)", db, conn.Name)
	}
	if !atLeast(have, need) {
		return redisTarget{}, fmt.Errorf("this endpoint can only read %s/db%d; ask the user to allow writes in Chloe's MCP Server settings", conn.Name, db)
	}
	kv, err := c.redisClient(ctx, conn)
	if err != nil {
		return redisTarget{}, err
	}
	dbs, err := kv.Databases(ctx)
	if err != nil {
		return redisTarget{}, err
	}
	if db >= len(dbs) {
		return redisTarget{}, fmt.Errorf("%s has databases 0 to %d", conn.Name, len(dbs)-1)
	}
	return redisTarget{conn: conn, db: db, cl: kv.DB(db), kv: kv}, nil
}

func (c *call) redisClient(ctx context.Context, conn model.Connection) (*rdb.Client, error) {
	if err := c.s.m.Ensure(ctx, conn.ID); err != nil {
		return nil, fmt.Errorf("couldn't open %s: %w", conn.Name, err)
	}
	return c.s.m.Redis(conn.ID)
}

// --- keys and values as the agent sees them ---------------------------------------

type RedisDBArgs struct {
	Connection string `json:"connection" jsonschema:"Redis connection name or id from list_connections"`
	DB         int    `json:"db,omitempty" jsonschema:"database number (default 0)"`
}

type RedisKeyArgs struct {
	RedisDBArgs
	Key       string `json:"key"`
	KeyBase64 bool   `json:"key_base64,omitempty" jsonschema:"key is base64, for binary key names (as redis_scan returns them)"`
}

func (a RedisKeyArgs) bytes() (string, error) { return keyBytes(a.Key, a.KeyBase64) }

func keyBytes(k string, b64 bool) (string, error) {
	if b64 {
		b, err := base64.StdEncoding.DecodeString(k)
		if err != nil {
			return "", fmt.Errorf("key isn't valid base64: %w", err)
		}
		k = string(b)
	}
	if k == "" {
		return "", errors.New("key is empty")
	}
	return k, nil
}

// redisText shows a value for the agent: UTF-8 as is, anything else as 0x hex
// (like binary SQL cells), cut at n bytes.
func redisText(s string, n int) string {
	t, _ := redisCut(s, n)
	return t
}

// redisCut is redisText that also reports whether the text was cut.
func redisCut(s string, n int) (string, bool) {
	if !utf8.ValidString(s) {
		s = "0x" + hex.EncodeToString([]byte(s))
	}
	if len(s) > n {
		return s[:n] + fmt.Sprintf("…[%d more bytes]", len(s)-n), true
	}
	return s, false
}

// redisArg turns a JSON scalar from the agent into a Redis argument.
func redisArg(v any) (string, error) {
	switch x := v.(type) {
	case string:
		return x, nil
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64), nil
	case json.Number:
		return x.String(), nil
	case bool:
		return strconv.FormatBool(x), nil
	case nil:
		return "", errors.New("values can't be null")
	case map[string]any:
		return "", errors.New("values must be strings or numbers, not objects")
	case []any:
		return "", errors.New("values must be strings or numbers, not lists")
	}
	return "", fmt.Errorf("values must be strings or numbers, not %T", v)
}

func redisArgs(vs []any) ([]string, error) {
	out := make([]string, len(vs))
	for i, v := range vs {
		var err error
		if out[i], err = redisArg(v); err != nil {
			return nil, fmt.Errorf("item %d: %w", i+1, err)
		}
	}
	return out, nil
}

func scores(m map[string]any) ([]redis.Z, error) {
	zs := make([]redis.Z, 0, len(m))
	for _, member := range slices.Sorted(maps.Keys(m)) {
		s, err := redisArg(m[member])
		if err != nil {
			return nil, err
		}
		f, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return nil, fmt.Errorf("score of %q isn't a number", member)
		}
		zs = append(zs, redis.Z{Member: member, Score: f})
	}
	return zs, nil
}

type RedisDatabaseOut struct {
	DB     int    `json:"db"`
	Keys   int64  `json:"keys"`
	Access string `json:"access" jsonschema:"read or write"`
}

type RedisDatabasesOut struct {
	Databases []RedisDatabaseOut `json:"databases"`
}

type RedisScanArgs struct {
	RedisDBArgs
	Pattern string `json:"pattern,omitempty" jsonschema:"glob pattern such as user:* (default *)"`
	Type    string `json:"type,omitempty" jsonschema:"only keys of this type: string, hash, list, set, zset or stream"`
	Cursor  string `json:"cursor,omitempty" jsonschema:"continue a scan with the cursor the last call returned"`
	Count   int    `json:"count,omitempty" jsonschema:"about how many keys to return (default 50, at most 1000)"`
}

type RedisOverviewArgs struct {
	RedisDBArgs
	Pattern string `json:"pattern,omitempty" jsonschema:"only keys matching this glob pattern (default *)"`
}

type RedisGroupOut struct {
	Pattern  string `json:"pattern" jsonschema:"the keys' common prefix, e.g. user:*, or a whole key that shares no prefix"`
	Type     string `json:"type"`
	Count    int64  `json:"count"`
	Expiring int64  `json:"expiring,omitempty" jsonschema:"how many of them have a TTL"`
	Example  string `json:"example"`
}

type RedisOverviewOut struct {
	Groups   []RedisGroupOut `json:"groups" jsonschema:"largest first"`
	Scanned  int64           `json:"scanned" jsonschema:"keys looked at"`
	Complete bool            `json:"complete" jsonschema:"false when the scan stopped early (time or key limit): counts cover only the keys scanned"`
	Other    int64           `json:"other,omitempty" jsonschema:"keys in groups beyond the first 200, not listed"`
}

type RedisKeyOut struct {
	Key    string `json:"key"`
	Base64 bool   `json:"key_base64,omitempty" jsonschema:"the key is binary and given as base64; pass key_base64 when using it"`
	Type   string `json:"type"`
	TTL    int64  `json:"ttl" jsonschema:"seconds until it expires; -1 for no expiry"`
}

type RedisScanOut struct {
	Keys   []RedisKeyOut `json:"keys"`
	Cursor string        `json:"cursor,omitempty" jsonschema:"pass back to continue the scan; absent once it is complete"`
}

type RedisGetArgs struct {
	RedisKeyArgs
	Cursor string `json:"cursor,omitempty" jsonschema:"continue reading a hash, list, set, sorted set or stream from where the last call stopped"`
	Count  int    `json:"count,omitempty" jsonschema:"elements to return (default 100, at most 1000)"`
}

type RedisItemOut struct {
	Index *int64   `json:"index,omitempty"`
	Field string   `json:"field,omitempty"`
	Value string   `json:"value"`
	Score *float64 `json:"score,omitempty"`
}

type RedisEntryOut struct {
	ID     string            `json:"id"`
	Fields map[string]string `json:"fields"`
}

type RedisValueOut struct {
	Key       string          `json:"key"`
	KeyBase64 bool            `json:"key_base64,omitempty"`
	Type      string          `json:"type"`
	TTL       int64           `json:"ttl" jsonschema:"seconds until it expires; -1 for no expiry"`
	Encoding  string          `json:"encoding,omitempty"`
	Length    int64           `json:"length" jsonschema:"bytes of a string, elements of anything else"`
	Value     *string         `json:"value,omitempty" jsonschema:"a string's value; binary as 0x hex"`
	Items     []RedisItemOut  `json:"items,omitempty" jsonschema:"hash fields, list items (with index), set members or sorted set members (with score)"`
	Entries   []RedisEntryOut `json:"entries,omitempty" jsonschema:"stream entries"`
	Cursor    string          `json:"cursor,omitempty" jsonschema:"more elements follow; pass it back to read them"`
	Truncated bool            `json:"truncated,omitempty" jsonschema:"some text was cut (strings at 64 KB, elements at 4 KB) and ends in …[N more bytes]; never write it back"`
}

type RedisCommandArgs struct {
	RedisDBArgs
	Args []string `json:"args" jsonschema:"the command and its arguments, e.g. [\"ZRANGE\", \"scores\", \"0\", \"9\", \"WITHSCORES\"]"`
}

type RedisCommandOut struct {
	Reply     string `json:"reply" jsonschema:"the reply as redis-cli prints it"`
	Truncated bool   `json:"truncated,omitempty"`
}

type RedisSetArgs struct {
	RedisKeyArgs
	Type      string `json:"type" jsonschema:"string, hash, list, set or zset"`
	Value     any    `json:"value" jsonschema:"string: a string; hash: {field: value}; list and set: [values]; zset: {member: score}"`
	TTL       *int64 `json:"ttl,omitempty" jsonschema:"seconds until the key expires; 0 for no expiry; leave out to keep the existing key's expiry"`
	OnlyIfNew bool   `json:"only_if_new,omitempty" jsonschema:"fail if the key exists instead of replacing it"`
}

type RedisEditArgs struct {
	RedisKeyArgs
	SetFields    map[string]any `json:"set_fields,omitempty" jsonschema:"hash: fields to set, {field: value}"`
	DeleteFields []string       `json:"delete_fields,omitempty" jsonschema:"hash: fields to remove"`
	Push         []any          `json:"push,omitempty" jsonschema:"list: values to append"`
	PushHead     bool           `json:"push_head,omitempty" jsonschema:"list: push at the head instead of the tail"`
	Add          []any          `json:"add,omitempty" jsonschema:"set: members to add"`
	AddScored    map[string]any `json:"add_scored,omitempty" jsonschema:"sorted set: members to add or rescore, {member: score}"`
	Remove       []any          `json:"remove,omitempty" jsonschema:"set or sorted set: members to remove; list: values to remove (every occurrence)"`
	AddEntry     map[string]any `json:"add_entry,omitempty" jsonschema:"stream: an entry to append, {field: value}"`
	Incr         any            `json:"incr,omitempty" jsonschema:"string: add this number to a counter (INCRBY, or INCRBYFLOAT for decimals); negative to subtract"`
	IncrFields   map[string]any `json:"incr_fields,omitempty" jsonschema:"hash: add numbers to fields, {field: by}"`
	IncrScores   map[string]any `json:"incr_scores,omitempty" jsonschema:"sorted set: add to members' scores, {member: by}"`
}

type RedisDeleteArgs struct {
	RedisDBArgs
	Keys       []string `json:"keys" jsonschema:"keys to delete (at most 1000)"`
	KeysBase64 bool     `json:"keys_base64,omitempty" jsonschema:"keys are base64"`
}

type RedisExpireArgs struct {
	RedisKeyArgs
	TTL int64 `json:"ttl" jsonschema:"seconds until the key expires; 0 removes the expiry"`
}

type RedisChangedOut struct {
	Approval  string            `json:"approval,omitempty" jsonschema:"how the change was cleared: approved by the user in Chloe, or ran without asking because danger mode is on"`
	Changed   int64             `json:"changed" jsonschema:"what actually changed: elements written, fields set, values pushed, members added, rescored or removed, entries appended, keys deleted; 0 when nothing matched"`
	NewValues map[string]string `json:"new_values,omitempty" jsonschema:"after an increment: the new value of the counter (by key), hash field or sorted set member; after add_entry: entry_id, the new stream entry's ID"`
}

// --- read-only commands -------------------------------------------------------------

// redisReadCommands are the commands redis_command runs. Each reads keys of the
// selected database only: nothing here selects another database, writes, blocks
// or runs scripts. KEYS is left out because it blocks the server; redis_scan
// does its job.
var redisReadCommands = map[string]bool{}

func init() {
	for _, c := range strings.Fields(`
		GET MGET GETRANGE STRLEN SUBSTR LCS EXISTS TYPE TTL PTTL EXPIRETIME PEXPIRETIME DBSIZE SCAN RANDOMKEY
		HGET HMGET HGETALL HKEYS HVALS HLEN HEXISTS HSTRLEN HRANDFIELD HSCAN
		LRANGE LINDEX LLEN LPOS
		SMEMBERS SISMEMBER SMISMEMBER SCARD SRANDMEMBER SSCAN SINTER SUNION SDIFF SINTERCARD
		ZRANGE ZRANGEBYSCORE ZRANGEBYLEX ZREVRANGE ZREVRANGEBYSCORE ZREVRANGEBYLEX ZSCORE ZMSCORE ZRANK ZREVRANK
		ZCARD ZCOUNT ZLEXCOUNT ZRANDMEMBER ZSCAN ZINTER ZUNION ZDIFF ZINTERCARD
		XRANGE XREVRANGE XLEN XPENDING PFCOUNT
		GEOPOS GEODIST GEOHASH GEOSEARCH GEORADIUS_RO GEORADIUSBYMEMBER_RO
		BITCOUNT BITPOS GETBIT BITFIELD_RO SORT_RO
		JSON.GET JSON.MGET JSON.TYPE JSON.STRLEN JSON.ARRLEN JSON.OBJKEYS JSON.OBJLEN`) {
		redisReadCommands[c] = true
	}
}

// redisReadSubcommands are the allowed subcommands of container commands.
var redisReadSubcommands = map[string][]string{
	"OBJECT": {"ENCODING", "FREQ", "IDLETIME", "REFCOUNT"},
	"MEMORY": {"USAGE"},
	"XINFO":  {"STREAM", "GROUPS", "CONSUMERS"},
}

func checkRedisRead(args []string) error {
	if len(args) == 0 {
		return errors.New("args is empty")
	}
	name := strings.ToUpper(args[0])
	if redisReadCommands[name] {
		return nil
	}
	if subs, ok := redisReadSubcommands[name]; ok {
		if len(args) > 1 && slices.Contains(subs, strings.ToUpper(args[1])) {
			return nil
		}
		return fmt.Errorf("redis_command runs %s only with %s", name, strings.Join(subs, ", "))
	}
	switch name {
	case "KEYS":
		return errors.New("KEYS blocks the server; use redis_scan")
	case "SELECT":
		return errors.New("pass the database number as db instead of SELECT")
	case "INFO":
		return errors.New("use server_status (Monitor) for INFO")
	case "CONFIG":
		return errors.New("use server_variables (Monitor) for CONFIG GET")
	case "CLIENT":
		return errors.New("use list_sessions (Monitor) for CLIENT LIST")
	case "XREAD", "XREADGROUP":
		return errors.New("use XRANGE or XREVRANGE to read a stream")
	}
	return fmt.Errorf("redis_command only runs read commands (GET, HGETALL, ZRANGE, XRANGE…); use redis_set, redis_edit, redis_delete or redis_expire to change data")
}

// checkRedisSize refuses a command that would read a whole collection of more
// than maxRedisKeys elements: the server builds the full reply, blocking while
// it does, before any of it could be cut. A bounded form is suggested instead.
func checkRedisSize(ctx context.Context, cl redis.Cmdable, args []string) error {
	if len(args) < 2 {
		return nil
	}
	name, key := strings.ToUpper(args[0]), args[1]
	opt := func(word string) (string, bool) {
		for i := 2; i < len(args); i++ {
			if strings.EqualFold(args[i], word) {
				if word == "LIMIT" && i+2 < len(args) {
					return args[i+2], true
				}
				if i+1 < len(args) {
					return args[i+1], true
				}
				return "", true
			}
		}
		return "", false
	}
	// bounded reports whether start..stop (as given to LRANGE or ZRANGE) asks for few enough elements.
	bounded := func() bool {
		if len(args) < 4 {
			return false
		}
		a, err1 := strconv.ParseInt(args[2], 10, 64)
		b, err2 := strconv.ParseInt(args[3], 10, 64)
		return err1 == nil && err2 == nil && a >= 0 && b >= a && b-a < maxRedisKeys
	}
	limited := func(word string) bool {
		n, ok := opt(word)
		c, err := strconv.ParseInt(n, 10, 64)
		return ok && err == nil && c >= 0 && c <= maxRedisKeys
	}
	var size func() (int64, error)
	var hint string
	switch name {
	case "HGETALL", "HKEYS", "HVALS":
		size, hint = func() (int64, error) { return cl.HLen(ctx, key).Result() }, "HSCAN "+key+" 0 COUNT 500"
	case "SMEMBERS":
		size, hint = func() (int64, error) { return cl.SCard(ctx, key).Result() }, "SSCAN "+key+" 0 COUNT 500"
	case "LRANGE":
		if bounded() {
			return nil
		}
		size, hint = func() (int64, error) { return cl.LLen(ctx, key).Result() }, "LRANGE "+key+" 0 999"
	case "ZRANGE", "ZREVRANGE":
		_, byScore := opt("BYSCORE")
		_, byLex := opt("BYLEX")
		if limited("LIMIT") || !byScore && !byLex && bounded() {
			return nil
		}
		size, hint = func() (int64, error) { return cl.ZCard(ctx, key).Result() }, "ZRANGE "+key+" 0 999, or ZSCAN "+key+" 0"
	case "ZRANGEBYSCORE", "ZREVRANGEBYSCORE", "ZRANGEBYLEX", "ZREVRANGEBYLEX":
		if limited("LIMIT") {
			return nil
		}
		size, hint = func() (int64, error) { return cl.ZCard(ctx, key).Result() }, "the same command with LIMIT 0 1000"
	case "XRANGE", "XREVRANGE":
		if limited("COUNT") {
			return nil
		}
		size, hint = func() (int64, error) { return cl.XLen(ctx, key).Result() }, "the same command with COUNT 1000"
	default:
		return nil
	}
	n, err := size()
	if err != nil || n <= maxRedisKeys {
		return nil // a wrong type or missing key gets the server's own answer
	}
	return fmt.Errorf("%s holds %d elements, more than the %d one read returns; use %s", key, n, maxRedisKeys, hint)
}

// quoteArgs renders a command like redis-cli input, for approvals and the log.
func quoteArgs(args ...string) string {
	parts := make([]string, len(args))
	for i, a := range args {
		if i == 0 {
			parts[i] = a
			continue
		}
		parts[i] = strconv.Quote(cutRaw(a, maxCellBytes))
	}
	return strings.Join(parts, " ")
}

// shownCommands lists commands for an approval, eliding the tail.
func shownCommands(cmds [][]string) string {
	var lines []string
	for i, c := range cmds {
		if i == maxRedisShown {
			lines = append(lines, fmt.Sprintf("# … and %d more commands", len(cmds)-i))
			break
		}
		lines = append(lines, quoteArgs(c...))
	}
	return strings.Join(lines, "\n")
}

func toAny(ss []string) []any {
	out := make([]any, len(ss))
	for i, s := range ss {
		out[i] = s
	}
	return out
}

// --- tools --------------------------------------------------------------------------

func (s *Server) addRedisTools(srv *mcp.Server, ep string, writes bool) {
	add(s, srv, ep, readOnlyTool("redis_databases", "List Redis databases",
		"Lists the databases of a Redis connection that this endpoint can see, with their key counts."),
		func(ctx context.Context, c *call, in struct {
			Connection string `json:"connection" jsonschema:"Redis connection name or id from list_connections"`
		}) (RedisDatabasesOut, error) {
			conn, a, err := c.connectionOf(in.Connection, redisEngines)
			if err != nil {
				return RedisDatabasesOut{}, err
			}
			c.act.Connection = conn.Name
			kv, err := c.redisClient(ctx, conn)
			if err != nil {
				return RedisDatabasesOut{}, err
			}
			dbs, err := kv.Databases(ctx)
			if err != nil {
				return RedisDatabasesOut{}, err
			}
			out := RedisDatabasesOut{Databases: []RedisDatabaseOut{}}
			for _, d := range dbs {
				if l := a.db(strconv.Itoa(d.Index)); l != store.MCPOff {
					out.Databases = append(out.Databases, RedisDatabaseOut{DB: d.Index, Keys: d.Keys, Access: levelName(l)})
				}
			}
			return out, nil
		})

	add(s, srv, ep, readOnlyTool("redis_overview", "Summarise Redis keys",
		"Summarises a database's keys: how many there are of each prefix and type (e.g. user:* — 120 hashes), with an example key and how many expire. "+
			"Start here to see what a database holds; then list keys of one prefix with redis_scan."),
		func(ctx context.Context, c *call, in RedisOverviewArgs) (RedisOverviewOut, error) {
			t, err := c.redisDB(ctx, in.Connection, in.DB, store.MCPRead)
			if err != nil {
				return RedisOverviewOut{}, err
			}
			c.act.SQL = quoteArgs("SCAN", "0", "MATCH", cmp(in.Pattern, "*"))
			ctx, cancel := context.WithTimeout(ctx, statementTimeout)
			defer cancel()
			out, err := overview(ctx, t.cl, cmp(in.Pattern, "*"))
			c.act.Rows = out.Scanned
			return out, err
		})

	add(s, srv, ep, readOnlyTool("redis_scan", "Scan Redis keys",
		"Lists keys matching a pattern (SCAN), with their type and expiry, a page at a time: pass back the cursor until it's absent. "+
			"To see what a database holds, use redis_overview first."),
		func(ctx context.Context, c *call, in RedisScanArgs) (RedisScanOut, error) {
			t, err := c.redisDB(ctx, in.Connection, in.DB, store.MCPRead)
			if err != nil {
				return RedisScanOut{}, err
			}
			if in.Type != "" && !slices.Contains([]string{"string", "hash", "list", "set", "zset", "stream"}, in.Type) {
				return RedisScanOut{}, fmt.Errorf("unknown type %q", in.Type)
			}
			if _, err := strconv.ParseUint(cmp(in.Cursor, "0"), 10, 64); err != nil {
				return RedisScanOut{}, fmt.Errorf("cursor %q isn't one redis_scan returned; leave it out to start over", in.Cursor)
			}
			c.act.SQL = quoteArgs("SCAN", cmp(in.Cursor, "0"), "MATCH", cmp(in.Pattern, "*"))
			ctx, cancel := context.WithTimeout(ctx, statementTimeout)
			defer cancel()
			count := 50
			if in.Count > 0 {
				count = clampLimit(in.Count)
			}
			res, err := t.kv.ScanKeys(ctx, t.db, in.Cursor, in.Pattern, in.Type, count)
			if err != nil {
				return RedisScanOut{}, err
			}
			out := RedisScanOut{Keys: []RedisKeyOut{}}
			if res.Cursor != "0" {
				out.Cursor = res.Cursor
			}
			for _, k := range res.Keys {
				out.Keys = append(out.Keys, RedisKeyOut{Key: k.Name.Text, Base64: k.Name.Base64, Type: k.Type, TTL: k.TTL})
			}
			c.act.Rows = int64(len(out.Keys))
			return out, nil
		})

	add(s, srv, ep, readOnlyTool("redis_get", "Read Redis key",
		"Reads a key's value with its type and expiry. Hashes, lists, sets, sorted sets (lowest score first) and streams come a page at a time: pass back the cursor for more. "+
			"Strings over 64 KB and elements over 4 KB are cut and the result marked truncated. For the highest scores use redis_command ZRANGE key 0 9 REV WITHSCORES."),
		func(ctx context.Context, c *call, in RedisGetArgs) (RedisValueOut, error) {
			t, err := c.redisDB(ctx, in.Connection, in.DB, store.MCPRead)
			if err != nil {
				return RedisValueOut{}, err
			}
			k, err := in.bytes()
			if err != nil {
				return RedisValueOut{}, err
			}
			c.act.SQL = quoteArgs("GET", k)
			ctx, cancel := context.WithTimeout(ctx, statementTimeout)
			defer cancel()
			if in.Cursor != "" {
				typ, err := t.cl.Type(ctx, k).Result()
				if err != nil {
					return RedisValueOut{}, err
				}
				if !validCursor(typ, in.Cursor) {
					return RedisValueOut{}, fmt.Errorf("cursor %q isn't one redis_get returned for this %s; leave it out to start from the beginning", in.Cursor, typ)
				}
			}
			kv, err := t.kv.GetValue(ctx, t.db, rdb.Enc(k), in.Cursor, clampLimit(in.Count))
			if errors.Is(err, rdb.ErrNoKey) {
				return RedisValueOut{}, fmt.Errorf("no key %q in db%d", redisText(k, 200), t.db)
			}
			if err != nil {
				return RedisValueOut{}, err
			}
			return valueOut(kv)
		})

	add(s, srv, ep, readOnlyTool("redis_command", "Run read-only Redis command",
		"Runs one read command (GET, HGETALL, ZRANGEBYSCORE, XRANGE, PFCOUNT, GEOSEARCH, OBJECT ENCODING…) in the database and returns the reply as redis-cli prints it. "+
			"Commands that write, block, switch databases or run scripts are refused."),
		func(ctx context.Context, c *call, in RedisCommandArgs) (RedisCommandOut, error) {
			if err := checkRedisRead(in.Args); err != nil {
				return RedisCommandOut{}, err
			}
			c.act.SQL = quoteArgs(in.Args...)
			t, err := c.redisDB(ctx, in.Connection, in.DB, store.MCPRead)
			if err != nil {
				return RedisCommandOut{}, err
			}
			ctx, cancel := context.WithTimeout(ctx, statementTimeout)
			defer cancel()
			if err := checkRedisSize(ctx, t.cl, in.Args); err != nil {
				return RedisCommandOut{}, err
			}
			v, err := t.cl.Do(ctx, toAny(in.Args)...).Result()
			if err != nil && !errors.Is(err, redis.Nil) {
				return RedisCommandOut{}, err
			}
			reply := rdb.Format(v, "")
			out := RedisCommandOut{Reply: reply}
			if len(reply) > maxResultBytes {
				out.Reply, out.Truncated = reply[:maxResultBytes]+"\n…", true
			}
			return out, nil
		})

	if !writes {
		return
	}

	add(s, srv, ep, writeTool("redis_set", "Set Redis key",
		"Creates a key, or replaces its whole value, as a string, hash, list, set or sorted set. "+
			"The user approves it in Chloe unless they've turned on danger mode. To change some elements of a collection, use redis_edit.", true),
		func(ctx context.Context, c *call, in RedisSetArgs) (RedisChangedOut, error) {
			t, err := c.redisDB(ctx, in.Connection, in.DB, store.MCPWrite)
			if err != nil {
				return RedisChangedOut{}, err
			}
			k, err := in.bytes()
			if err != nil {
				return RedisChangedOut{}, err
			}
			if in.TTL != nil && *in.TTL < 0 {
				return RedisChangedOut{}, errors.New("ttl can't be negative; 0 means no expiry")
			}
			write, n, err := setCommands(k, in.Type, in.Value)
			if err != nil {
				return RedisChangedOut{}, err
			}
			ctx, cancel := context.WithTimeout(ctx, statementTimeout)
			defer cancel()
			old, err := t.cl.Type(ctx, k).Result()
			if err != nil {
				return RedisChangedOut{}, err
			}
			exists := old != "none"
			if exists && in.OnlyIfNew {
				return RedisChangedOut{}, fmt.Errorf("key %q already exists (a %s)", redisText(k, 200), old)
			}
			// A replaced key keeps its expiry unless ttl is given: a string replaced
			// by a string with KEEPTTL, anything else by the same deadline.
			sameString := old == "string" && in.Type == "string"
			var expireAt int64
			if exists && in.TTL == nil {
				if d, err := t.cl.PTTL(ctx, k).Result(); err == nil && d > 0 {
					if sameString {
						write[0] = append(write[0], "KEEPTTL")
					} else {
						expireAt = time.Now().Add(d).UnixMilli()
					}
				}
			}
			var cmds [][]string
			summary := fmt.Sprintf("Create %s %s in %s", in.Type, showKey(k), t.dbName())
			if exists {
				if !sameString {
					cmds = append(cmds, []string{"DEL", k})
				}
				length := redisLength(ctx, t.cl, k, old)
				summary = fmt.Sprintf("Replace %s %s (%s) with a %s in %s", old, showKey(k), plural(length, "element"), in.Type, t.dbName())
				if old == "string" {
					summary = fmt.Sprintf("Replace string %s with a %s in %s", showKey(k), in.Type, t.dbName())
				}
			}
			cmds = append(cmds, write...)
			switch {
			case in.TTL != nil && *in.TTL > 0:
				cmds = append(cmds, []string{"EXPIRE", k, strconv.FormatInt(*in.TTL, 10)})
			case expireAt > 0:
				cmds = append(cmds, []string{"PEXPIREAT", k, strconv.FormatInt(expireAt, 10)})
			}
			c.act.SQL = shownCommands(cmds)
			if err := c.approve(ctx, ApprovalRequest{Connection: t.conn.Name, Database: t.dbName(), Table: k, Summary: summary, SQL: c.act.SQL, Rows: n}); err != nil {
				return RedisChangedOut{}, err
			}
			if _, _, err := runTx(ctx, t.cl, k, old, cmds); err != nil {
				return RedisChangedOut{}, err
			}
			c.act.Rows = n
			return RedisChangedOut{Changed: n}, nil
		})

	add(s, srv, ep, writeTool("redis_edit", "Edit Redis collection",
		"Changes part of a value: increment a counter, set, delete or increment hash fields, push or remove list values, add, remove or rescore members, append a stream entry. "+
			"A missing key is created. The user approves it in Chloe unless they've turned on danger mode.", true),
		func(ctx context.Context, c *call, in RedisEditArgs) (RedisChangedOut, error) {
			t, err := c.redisDB(ctx, in.Connection, in.DB, store.MCPWrite)
			if err != nil {
				return RedisChangedOut{}, err
			}
			k, err := in.bytes()
			if err != nil {
				return RedisChangedOut{}, err
			}
			ctx, cancel := context.WithTimeout(ctx, statementTimeout)
			defer cancel()
			old, err := t.cl.Type(ctx, k).Result()
			if err != nil {
				return RedisChangedOut{}, err
			}
			typ, cmds, n, err := editCommands(k, old, in)
			if err != nil {
				return RedisChangedOut{}, err
			}
			verb := "Change"
			if old == "none" {
				verb = "Create"
			}
			c.act.SQL = shownCommands(cmds)
			if err := c.approve(ctx, ApprovalRequest{Connection: t.conn.Name, Database: t.dbName(), Table: k,
				Summary: fmt.Sprintf("%s %s %s in %s (%s)", verb, typ, showKey(k), t.dbName(), plural(n, "element")), SQL: c.act.SQL, Rows: n}); err != nil {
				return RedisChangedOut{}, err
			}
			changed, values, err := runTx(ctx, t.cl, k, old, cmds)
			if err != nil {
				return RedisChangedOut{}, err
			}
			c.act.Rows = changed
			return RedisChangedOut{Changed: changed, NewValues: values}, nil
		})

	add(s, srv, ep, writeTool("redis_delete", "Delete Redis keys",
		"Deletes keys (UNLINK). The user approves it in Chloe unless they've turned on danger mode. To delete by pattern, find the keys with redis_scan first.", true),
		func(ctx context.Context, c *call, in RedisDeleteArgs) (RedisChangedOut, error) {
			t, err := c.redisDB(ctx, in.Connection, in.DB, store.MCPWrite)
			if err != nil {
				return RedisChangedOut{}, err
			}
			switch {
			case len(in.Keys) == 0:
				return RedisChangedOut{}, errors.New("keys is empty")
			case len(in.Keys) > maxRedisKeys:
				return RedisChangedOut{}, fmt.Errorf("delete at most %d keys per call", maxRedisKeys)
			}
			var keys []string
			for _, k := range in.Keys {
				b, err := keyBytes(k, in.KeysBase64)
				if err != nil {
					return RedisChangedOut{}, err
				}
				if !slices.Contains(keys, b) {
					keys = append(keys, b)
				}
			}
			ctx, cancel := context.WithTimeout(ctx, statementTimeout)
			defer cancel()
			n, err := t.cl.Exists(ctx, keys...).Result()
			if err != nil {
				return RedisChangedOut{}, err
			}
			if n == 0 {
				return RedisChangedOut{}, errors.New("none of the keys exist (keys are exact names; find keys by pattern with redis_scan)")
			}
			c.act.SQL = shownCommands([][]string{append([]string{"UNLINK"}, keys...)})
			if err := c.approve(ctx, ApprovalRequest{Connection: t.conn.Name, Database: t.dbName(),
				Summary: fmt.Sprintf("Delete %s from %s", plural(n, "key"), t.dbName()), SQL: c.act.SQL, Rows: n}); err != nil {
				return RedisChangedOut{}, err
			}
			deleted, err := t.cl.Unlink(ctx, keys...).Result()
			c.act.Rows = deleted
			return RedisChangedOut{Changed: deleted}, err
		})

	add(s, srv, ep, writeTool("redis_expire", "Set Redis expiry",
		"Sets how many seconds until a key expires, or with ttl 0 removes its expiry. The user approves it in Chloe unless they've turned on danger mode.", true),
		func(ctx context.Context, c *call, in RedisExpireArgs) (RedisChangedOut, error) {
			t, err := c.redisDB(ctx, in.Connection, in.DB, store.MCPWrite)
			if err != nil {
				return RedisChangedOut{}, err
			}
			k, err := in.bytes()
			if err != nil {
				return RedisChangedOut{}, err
			}
			if in.TTL < 0 {
				return RedisChangedOut{}, errors.New("ttl can't be negative; use redis_delete to remove a key")
			}
			ctx, cancel := context.WithTimeout(ctx, statementTimeout)
			defer cancel()
			old, err := t.cl.Type(ctx, k).Result()
			if err != nil {
				return RedisChangedOut{}, err
			}
			if old == "none" {
				return RedisChangedOut{}, fmt.Errorf("no key %q in db%d", redisText(k, 200), t.db)
			}
			cmd := []string{"PERSIST", k}
			summary := fmt.Sprintf("Remove the expiry of %s in %s", showKey(k), t.dbName())
			if in.TTL > 0 {
				cmd = []string{"EXPIRE", k, strconv.FormatInt(in.TTL, 10)}
				summary = fmt.Sprintf("Expire %s in %s in %s", showKey(k), t.dbName(), humanDuration(in.TTL))
			}
			c.act.SQL = quoteArgs(cmd...)
			if err := c.approve(ctx, ApprovalRequest{Connection: t.conn.Name, Database: t.dbName(), Table: k, Summary: summary, SQL: c.act.SQL, Rows: 1}); err != nil {
				return RedisChangedOut{}, err
			}
			if _, _, err := runTx(ctx, t.cl, k, old, [][]string{cmd}); err != nil {
				return RedisChangedOut{}, err
			}
			c.act.Rows = 1
			return RedisChangedOut{Changed: 1}, nil
		})
}

var streamID = regexp.MustCompile(`^\d+-\d+$`)

// validCursor checks a redis_get cursor: an element offset, a scan cursor or a stream entry ID.
func validCursor(typ, cursor string) bool {
	if typ == "stream" {
		return streamID.MatchString(cursor)
	}
	_, err := strconv.ParseUint(cursor, 10, 64)
	return err == nil
}

// humanDuration renders seconds for an approval, e.g. "90 seconds", "2 hours", "about 3 years".
func humanDuration(secs int64) string {
	units := []struct {
		n    int64
		name string
	}{{365 * 86400, "year"}, {86400, "day"}, {3600, "hour"}, {60, "minute"}}
	for _, u := range units {
		if secs >= 2*u.n {
			if secs%u.n == 0 {
				return plural(secs/u.n, u.name)
			}
			return "about " + plural((secs+u.n/2)/u.n, u.name)
		}
	}
	return plural(secs, "second")
}

const (
	overviewKeys   = 200_000
	overviewGroups = 200
	overviewTime   = 10 * time.Second
)

// keyPrefix is the pattern a key is grouped under: everything up to its last
// ':' (or '/', '.', '|') followed by *, or the key itself when it has none.
func keyPrefix(k string) string {
	if i := strings.LastIndexAny(k, ":/.|"); i > 0 && i < len(k)-1 {
		return k[:i+1] + "*"
	}
	return k
}

// overview scans a database and counts keys per prefix and type.
func overview(ctx context.Context, cl *redis.Client, pattern string) (RedisOverviewOut, error) {
	type group struct{ RedisGroupOut }
	groups := map[string]*group{}
	out := RedisOverviewOut{Groups: []RedisGroupOut{}}
	deadline := time.Now().Add(overviewTime)
	var cur uint64
	for {
		keys, next, err := cl.Scan(ctx, cur, pattern, 1000).Result()
		if err != nil {
			return out, err
		}
		if len(keys) > 0 {
			pipe := cl.Pipeline()
			types := make([]*redis.StatusCmd, len(keys))
			ttls := make([]*redis.DurationCmd, len(keys))
			for i, k := range keys {
				types[i], ttls[i] = pipe.Type(ctx, k), pipe.PTTL(ctx, k)
			}
			if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
				return out, err
			}
			for i, k := range keys {
				typ := types[i].Val()
				if typ == "none" {
					continue // expired or deleted since the scan
				}
				p := keyPrefix(k)
				g := groups[p+"\x00"+typ]
				if g == nil {
					g = &group{RedisGroupOut: RedisGroupOut{Pattern: redisText(p, 200), Type: typ, Example: redisText(k, 200)}}
					groups[p+"\x00"+typ] = g
				}
				g.Count++
				if ttls[i].Val() > 0 {
					g.Expiring++
				}
			}
			out.Scanned += int64(len(keys))
		}
		cur = next
		if cur == 0 {
			out.Complete = true
			break
		}
		if out.Scanned >= overviewKeys || time.Now().After(deadline) {
			break
		}
	}
	all := make([]*group, 0, len(groups))
	for _, g := range groups {
		all = append(all, g)
	}
	slices.SortFunc(all, func(a, b *group) int {
		if a.Count != b.Count {
			return int(b.Count - a.Count)
		}
		return strings.Compare(a.Pattern, b.Pattern)
	})
	for i, g := range all {
		if i >= overviewGroups {
			out.Other += g.Count
			continue
		}
		out.Groups = append(out.Groups, g.RedisGroupOut)
	}
	return out, nil
}

func cmp(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

func valueOut(kv rdb.KeyValue) (RedisValueOut, error) {
	out := RedisValueOut{Key: kv.Name.Text, KeyBase64: kv.Name.Base64, Type: kv.Type, TTL: kv.TTL, Encoding: kv.Encoding, Length: kv.Length, Cursor: kv.Cursor}
	str := func(v rdb.Value, n int) string {
		b, _ := v.Bytes()
		t, cut := redisCut(b, n)
		out.Truncated = out.Truncated || cut
		return t
	}
	if kv.String != nil {
		s := str(*kv.String, maxRedisString)
		out.Value = &s
	}
	for _, it := range kv.Items {
		o := RedisItemOut{Index: it.Index, Value: str(it.Value, maxCellBytes), Score: it.Score}
		if kv.Type == "zset" {
			o.Index = nil
		}
		if it.Field != nil {
			o.Field = str(*it.Field, maxCellBytes)
		}
		out.Items = append(out.Items, o)
	}
	switch kv.Type {
	case "hash", "list", "set", "zset":
		if out.Items == nil {
			out.Items = []RedisItemOut{}
		}
	case "stream":
		if kv.Entries == nil {
			out.Entries = []RedisEntryOut{}
		}
	}
	for _, e := range kv.Entries {
		o := RedisEntryOut{ID: e.ID, Fields: map[string]string{}}
		for _, f := range e.Fields {
			o.Fields[str(*f.Field, maxCellBytes)] = str(f.Value, maxCellBytes)
		}
		out.Entries = append(out.Entries, o)
	}
	return out, nil
}

// redisLength counts a key's elements for an approval.
func redisLength(ctx context.Context, cl *redis.Client, k, typ string) int64 {
	var n int64
	switch typ {
	case "string":
		return 1
	case "hash":
		n, _ = cl.HLen(ctx, k).Result()
	case "list":
		n, _ = cl.LLen(ctx, k).Result()
	case "set":
		n, _ = cl.SCard(ctx, k).Result()
	case "zset":
		n, _ = cl.ZCard(ctx, k).Result()
	case "stream":
		n, _ = cl.XLen(ctx, k).Result()
	}
	return n
}

// setCommands builds the commands that write a whole value, and how many
// elements it has.
func setCommands(k, typ string, v any) ([][]string, int64, error) {
	switch typ {
	case "string":
		s, err := redisArg(v)
		if err != nil {
			return nil, 0, fmt.Errorf("a string value: %w", err)
		}
		return [][]string{{"SET", k, s}}, 1, nil
	case "hash":
		m, ok := v.(map[string]any)
		if !ok || len(m) == 0 {
			return nil, 0, errors.New("a hash value is a non-empty object of field: value")
		}
		cmd := []string{"HSET", k}
		for _, f := range slices.Sorted(maps.Keys(m)) {
			s, err := redisArg(m[f])
			if err != nil {
				return nil, 0, fmt.Errorf("field %q: %w", f, err)
			}
			cmd = append(cmd, f, s)
		}
		return [][]string{cmd}, int64(len(m)), nil
	case "list", "set":
		list, ok := v.([]any)
		if !ok || len(list) == 0 {
			return nil, 0, fmt.Errorf("a %s value is a non-empty array", typ)
		}
		vals, err := redisArgs(list)
		if err != nil {
			return nil, 0, err
		}
		name := "RPUSH"
		if typ == "set" {
			name = "SADD"
		}
		return [][]string{append([]string{name, k}, vals...)}, int64(len(vals)), nil
	case "zset":
		m, ok := v.(map[string]any)
		if !ok || len(m) == 0 {
			return nil, 0, errors.New("a zset value is a non-empty object of member: score")
		}
		zs, err := scores(m)
		if err != nil {
			return nil, 0, err
		}
		return [][]string{zadd(k, zs)}, int64(len(zs)), nil
	case "stream":
		return nil, 0, errors.New("streams are appended to, not set: use redis_edit with add_entry")
	}
	return nil, 0, fmt.Errorf("unknown type %q (string, hash, list, set or zset)", typ)
}

func zadd(k string, zs []redis.Z) []string {
	cmd := []string{"ZADD", k}
	for _, z := range zs {
		cmd = append(cmd, strconv.FormatFloat(z.Score, 'g', -1, 64), z.Member.(string))
	}
	return cmd
}

// editCommands builds the commands of a redis_edit for a key of type old
// ("none" when it doesn't exist), the type they work on and the element count.
func editCommands(k, old string, in RedisEditArgs) (string, [][]string, int64, error) {
	want := map[string]bool{}
	note := func(typ string, used bool) {
		if used {
			want[typ] = true
		}
	}
	note("string", in.Incr != nil)
	note("hash", len(in.SetFields) > 0 || len(in.DeleteFields) > 0 || len(in.IncrFields) > 0)
	note("list", len(in.Push) > 0)
	note("set", len(in.Add) > 0)
	note("zset", len(in.AddScored) > 0 || len(in.IncrScores) > 0)
	note("stream", in.AddEntry != nil)
	if len(want) > 1 {
		return "", nil, 0, fmt.Errorf("one call changes one type of value; got changes for %s", strings.Join(slices.Sorted(maps.Keys(want)), " and "))
	}
	typ := old
	for t := range want {
		typ = t
	}
	switch {
	case len(in.Remove) > 0 && typ == "none":
		return "", nil, 0, fmt.Errorf("no key %q", redisText(k, 200))
	case len(in.Remove) > 0 && !slices.Contains([]string{"list", "set", "zset"}, typ):
		return "", nil, 0, fmt.Errorf("remove works on lists, sets and sorted sets; %q is a %s", redisText(k, 200), typ)
	case typ == "none":
		return "", nil, 0, errors.New("nothing to change: pass incr, set_fields, delete_fields, incr_fields, push, add, add_scored, incr_scores, remove or add_entry")
	case old != "none" && old != typ:
		return "", nil, 0, fmt.Errorf("%q is a %s, not a %s", redisText(k, 200), old, typ)
	}
	if old == "none" && len(in.DeleteFields) > 0 {
		return "", nil, 0, fmt.Errorf("no key %q to delete fields from", redisText(k, 200))
	}
	var cmds [][]string
	var n int64
	switch typ {
	case "string":
		if in.Incr == nil {
			break
		}
		by, err := redisArg(in.Incr)
		if err != nil {
			return "", nil, 0, fmt.Errorf("incr: %w", err)
		}
		name, err := incrName(by, "INCRBY", "INCRBYFLOAT")
		if err != nil {
			return "", nil, 0, fmt.Errorf("incr: %w", err)
		}
		cmds, n = [][]string{{name, k, by}}, 1
	case "hash":
		for _, f := range slices.Sorted(maps.Keys(in.IncrFields)) {
			by, err := redisArg(in.IncrFields[f])
			if err != nil {
				return "", nil, 0, fmt.Errorf("incr_fields %q: %w", f, err)
			}
			name, err := incrName(by, "HINCRBY", "HINCRBYFLOAT")
			if err != nil {
				return "", nil, 0, fmt.Errorf("incr_fields %q: %w", f, err)
			}
			cmds = append(cmds, []string{name, k, f, by})
			n++
		}
		if len(in.SetFields) > 0 {
			set, _, err := setCommands(k, "hash", in.SetFields)
			if err != nil {
				return "", nil, 0, err
			}
			cmds = append(cmds, set...)
			n += int64(len(in.SetFields))
		}
		if len(in.DeleteFields) > 0 {
			cmds = append(cmds, append([]string{"HDEL", k}, in.DeleteFields...))
			n += int64(len(in.DeleteFields))
		}
	case "list":
		if len(in.Push) > 0 {
			vals, err := redisArgs(in.Push)
			if err != nil {
				return "", nil, 0, err
			}
			name := "RPUSH"
			if in.PushHead {
				name = "LPUSH"
			}
			cmds = append(cmds, append([]string{name, k}, vals...))
			n += int64(len(vals))
		}
		vals, err := redisArgs(in.Remove)
		if err != nil {
			return "", nil, 0, err
		}
		for _, v := range vals {
			cmds = append(cmds, []string{"LREM", k, "0", v})
		}
		n += int64(len(vals))
	case "set", "zset":
		if len(in.Add) > 0 {
			vals, err := redisArgs(in.Add)
			if err != nil {
				return "", nil, 0, err
			}
			cmds = append(cmds, append([]string{"SADD", k}, vals...))
			n += int64(len(vals))
		}
		if len(in.AddScored) > 0 {
			zs, err := scores(in.AddScored)
			if err != nil {
				return "", nil, 0, err
			}
			// CH counts rescored members too, so the result reports real changes.
			cmds = append(cmds, append([]string{"ZADD", k, "CH"}, zadd(k, zs)[2:]...))
			n += int64(len(zs))
		}
		if len(in.IncrScores) > 0 {
			zs, err := scores(in.IncrScores)
			if err != nil {
				return "", nil, 0, fmt.Errorf("incr_scores: %w", err)
			}
			for _, z := range zs {
				cmds = append(cmds, []string{"ZINCRBY", k, strconv.FormatFloat(z.Score, 'g', -1, 64), z.Member.(string)})
			}
			n += int64(len(zs))
		}
		if len(in.Remove) > 0 {
			vals, err := redisArgs(in.Remove)
			if err != nil {
				return "", nil, 0, err
			}
			name := "SREM"
			if typ == "zset" {
				name = "ZREM"
			}
			cmds = append(cmds, append([]string{name, k}, vals...))
			n += int64(len(vals))
		}
	case "stream":
		if len(in.AddEntry) == 0 {
			return "", nil, 0, errors.New("add_entry needs at least one field")
		}
		cmd := []string{"XADD", k, "*"}
		for _, f := range slices.Sorted(maps.Keys(in.AddEntry)) {
			s, err := redisArg(in.AddEntry[f])
			if err != nil {
				return "", nil, 0, fmt.Errorf("field %q: %w", f, err)
			}
			cmd = append(cmd, f, s)
		}
		cmds, n = [][]string{cmd}, 1
	default:
		return "", nil, 0, fmt.Errorf("redis_edit can't change a %s", typ)
	}
	if len(cmds) == 0 {
		return "", nil, 0, fmt.Errorf("nothing to change for a %s: pass the options for that type", typ)
	}
	return typ, cmds, n, nil
}

// errKeyChanged reports a key that changed between the approval and the write.
func errKeyChanged(k string) error {
	return fmt.Errorf("key %s changed while waiting for approval, so nothing was done; read it again before retrying", showKey(k))
}

// incrName picks the integer or decimal form of an increment command.
func incrName(by, integer, decimal string) (string, error) {
	if _, err := strconv.ParseInt(by, 10, 64); err == nil {
		return integer, nil
	}
	if _, err := strconv.ParseFloat(by, 64); err != nil {
		return "", fmt.Errorf("%q isn't a number", by)
	}
	return decimal, nil
}

// effect counts what one command of a write actually did: fields written,
// values pushed, members added, rescored or removed, entries appended.
func effect(cmd []string, reply redis.Cmder) int64 {
	switch cmd[0] {
	case "HSET":
		return int64(len(cmd)-2) / 2
	case "RPUSH", "LPUSH":
		return int64(len(cmd) - 2)
	case "XADD", "SET", "INCRBY", "INCRBYFLOAT", "HINCRBY", "HINCRBYFLOAT", "ZINCRBY":
		return 1
	case "HDEL", "SADD", "SREM", "ZADD", "ZREM", "LREM":
		if c, ok := reply.(*redis.Cmd); ok {
			n, _ := c.Int64()
			return n
		}
	}
	return 0
}

// runTx runs commands in MULTI/EXEC, so they apply together or not at all. The
// key is watched from before its type is checked against old (the type it had
// when the commands were approved; "none" for a missing key), so a write by
// anyone else in between aborts the transaction instead of mixing with it.
func runTx(ctx context.Context, cl *redis.Client, k, old string, cmds [][]string) (int64, map[string]string, error) {
	var replies []redis.Cmder
	err := cl.Watch(ctx, func(tx *redis.Tx) error {
		now, err := tx.Type(ctx, k).Result()
		if err != nil {
			return err
		}
		if now != old {
			return errKeyChanged(k)
		}
		replies, err = tx.TxPipelined(ctx, func(p redis.Pipeliner) error {
			for _, c := range cmds {
				p.Do(ctx, toAny(c)...)
			}
			return nil
		})
		return err
	}, k)
	if errors.Is(err, redis.TxFailedErr) {
		return 0, nil, errKeyChanged(k)
	}
	var n int64
	var values map[string]string
	for i, c := range cmds {
		if i >= len(replies) {
			break
		}
		n += effect(c, replies[i])
		if name, v, ok := incremented(c, replies[i]); ok {
			if values == nil {
				values = map[string]string{}
			}
			values[name] = v
		}
	}
	return n, values, err
}

// incremented reports the new value an increment command left: the counter's
// (named by its key), a hash field's or a member's score.
func incremented(cmd []string, reply redis.Cmder) (string, string, bool) {
	c, ok := reply.(*redis.Cmd)
	if !ok || c.Err() != nil {
		return "", "", false
	}
	var name string
	switch cmd[0] {
	case "INCRBY", "INCRBYFLOAT":
		name = cmd[1]
	case "HINCRBY", "HINCRBYFLOAT":
		name = cmd[2]
	case "ZINCRBY":
		name = cmd[3]
	case "XADD":
		name = "entry_id" // the new entry's ID
	default:
		return "", "", false
	}
	return redisText(name, 200), fmt.Sprint(c.Val()), true
}

// showKey quotes a key for a summary line, so its text can't read as part of the sentence.
func showKey(k string) string { return strconv.Quote(cutRaw(k, 100)) }

// cutRaw shortens raw bytes for display; strconv.Quote then shows binary as \xNN, like redis-cli.
func cutRaw(s string, n int) string {
	if len(s) > n {
		return s[:n] + fmt.Sprintf("…[%d more bytes]", len(s)-n)
	}
	return s
}
