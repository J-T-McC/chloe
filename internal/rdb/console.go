package rdb

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/redis/go-redis/v9"
)

// Console is a dedicated connection for typed commands, so SELECT, MULTI and
// friends keep their state between commands.
type Console struct {
	conn *redis.Conn
	DB   int
}

// OpenConsole reserves a connection starting in database db.
func (c *Client) OpenConsole(ctx context.Context, db int) (*Console, error) {
	conn := c.db(db).Conn()
	if err := conn.Ping(ctx).Err(); err != nil {
		conn.Close()
		return nil, err
	}
	return &Console{conn: conn, DB: db}, nil
}

func (s *Console) Close() error { return s.conn.Close() }

// Exec runs one command line and formats the reply the way redis-cli does.
func (s *Console) Exec(ctx context.Context, line string) (string, error) {
	args, err := SplitArgs(line)
	if err != nil {
		return "", err
	}
	if len(args) == 0 {
		return "", nil
	}
	switch strings.ToUpper(args[0]) {
	case "SUBSCRIBE", "PSUBSCRIBE", "SSUBSCRIBE", "MONITOR", "SYNC", "PSYNC":
		return "", fmt.Errorf("%s isn't supported in the console", strings.ToUpper(args[0]))
	}
	a := make([]any, len(args))
	for i, x := range args {
		a[i] = x
	}
	v, err := s.conn.Do(ctx, a...).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		var re redis.Error
		if errors.As(err, &re) {
			return "(error) " + err.Error(), nil
		}
		return "", err
	}
	if strings.EqualFold(args[0], "SELECT") && err == nil && len(args) > 1 {
		if n, e := strconv.Atoi(args[1]); e == nil {
			s.DB = n
		}
	}
	return Format(v, ""), nil
}

// Format renders a reply like redis-cli: quoted strings, (integer), (nil),
// numbered arrays and nested indentation.
func Format(v any, indent string) string {
	switch x := v.(type) {
	case nil:
		return "(nil)"
	case string:
		if x == "OK" || x == "QUEUED" || x == "PONG" {
			return x
		}
		return strconv.Quote(x)
	case int64:
		return "(integer) " + strconv.FormatInt(x, 10)
	case float64:
		return "(double) " + strconv.FormatFloat(x, 'g', -1, 64)
	case bool:
		if x {
			return "(true)"
		}
		return "(false)"
	case []any:
		if len(x) == 0 {
			return "(empty array)"
		}
		w := len(strconv.Itoa(len(x)))
		var b strings.Builder
		for i, e := range x {
			prefix := fmt.Sprintf("%*d) ", w, i+1)
			if i > 0 {
				b.WriteString("\n" + indent)
			}
			b.WriteString(prefix + Format(e, indent+strings.Repeat(" ", len(prefix))))
		}
		return b.String()
	case map[any]any:
		if len(x) == 0 {
			return "(empty hash)"
		}
		keys := make([]string, 0, len(x))
		byKey := map[string]any{}
		for k, val := range x {
			ks := fmt.Sprint(k)
			keys = append(keys, ks)
			byKey[ks] = val
		}
		sort.Strings(keys)
		flat := make([]any, 0, len(keys)*2)
		for _, k := range keys {
			flat = append(flat, k, byKey[k])
		}
		return Format(flat, indent)
	case error:
		return "(error) " + x.Error()
	}
	return fmt.Sprint(v)
}

// SplitArgs splits a command line like redis-cli: whitespace separates
// arguments; "double quotes" support \n \t \xHH escapes; 'single quotes' are literal.
func SplitArgs(line string) ([]string, error) {
	var out []string
	i, n := 0, len(line)
	for {
		for i < n && (line[i] == ' ' || line[i] == '\t' || line[i] == '\n' || line[i] == '\r') {
			i++
		}
		if i >= n {
			return out, nil
		}
		var b strings.Builder
		inDouble, inSingle, done := false, false, false
		for !done {
			if i >= n {
				if inDouble || inSingle {
					return nil, errors.New("unbalanced quotes in command")
				}
				break
			}
			ch := line[i]
			switch {
			case inDouble:
				switch {
				case ch == '\\' && i+3 < n && line[i+1] == 'x' && isHex(line[i+2]) && isHex(line[i+3]):
					v, _ := strconv.ParseUint(line[i+2:i+4], 16, 8)
					b.WriteByte(byte(v))
					i += 3
				case ch == '\\' && i+1 < n:
					i++
					switch line[i] {
					case 'n':
						b.WriteByte('\n')
					case 'r':
						b.WriteByte('\r')
					case 't':
						b.WriteByte('\t')
					case 'b':
						b.WriteByte('\b')
					case 'a':
						b.WriteByte('\a')
					default:
						b.WriteByte(line[i])
					}
				case ch == '"':
					inDouble = false
					if i+1 < n && line[i+1] != ' ' && line[i+1] != '\t' {
						return nil, errors.New("closing quote must be followed by a space")
					}
					done = true
				default:
					b.WriteByte(ch)
				}
			case inSingle:
				switch {
				case ch == '\\' && i+1 < n && line[i+1] == '\'':
					b.WriteByte('\'')
					i++
				case ch == '\'':
					inSingle = false
					if i+1 < n && line[i+1] != ' ' && line[i+1] != '\t' {
						return nil, errors.New("closing quote must be followed by a space")
					}
					done = true
				default:
					b.WriteByte(ch)
				}
			default:
				switch ch {
				case ' ', '\t', '\n', '\r':
					done = true
				case '"':
					inDouble = true
				case '\'':
					inSingle = true
				default:
					b.WriteByte(ch)
				}
			}
			i++
		}
		out = append(out, b.String())
	}
}

func isHex(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}

// Commands lists command names the server knows (for console completion).
func (c *Client) Commands(ctx context.Context) ([]string, error) {
	res, err := c.db(0).Command(ctx).Result()
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(res))
	for name := range res {
		out = append(out, strings.ToUpper(name))
	}
	sort.Strings(out)
	return out, nil
}
