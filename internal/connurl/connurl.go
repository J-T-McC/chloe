// Package connurl converts between connection URLs and connection profiles, and
// describes presets for hosted database services.
package connurl

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"chloe/internal/model"
)

// Parse reads postgres://, postgresql://, mysql://, mariadb:// and jdbc: URLs.
// The password, if present, is returned in the profile (it is not saved until the
// user saves the connection).
func Parse(raw string) (model.Connection, error) {
	var c model.Connection
	raw = strings.TrimSpace(raw)
	raw = strings.TrimPrefix(raw, "jdbc:")
	u, err := url.Parse(raw)
	if err != nil {
		return c, fmt.Errorf("not a connection URL: %w", err)
	}
	switch strings.ToLower(u.Scheme) {
	case "postgres", "postgresql":
		c.Type = model.PostgreSQL
	case "mysql", "mysqlx":
		c.Type = model.MySQL
	case "mariadb":
		c.Type = model.MariaDB
	case "redis", "rediss", "valkey", "valkeys":
		c.Type = model.Redis
		c.SSL.Enabled = strings.EqualFold(u.Scheme, "rediss") || strings.EqualFold(u.Scheme, "valkeys")
	case "":
		return c, errors.New("connection URL needs a scheme such as postgres:// or mysql://")
	default:
		return c, fmt.Errorf("unsupported connection URL scheme %q", u.Scheme)
	}

	// Multi-host URLs (host1,host2): use the first host.
	hostPort := strings.Split(u.Host, ",")[0]
	host, port := hostPort, ""
	if h, p, err := net.SplitHostPort(hostPort); err == nil {
		host, port = h, p
	}
	c.Host = host
	c.Port = model.DefaultPort(c.Type)
	if port != "" {
		n, err := strconv.Atoi(port)
		if err != nil {
			return c, fmt.Errorf("invalid port %q", port)
		}
		c.Port = n
	}
	if u.User != nil {
		c.User = u.User.Username()
		if pw, ok := u.User.Password(); ok {
			c.Password = pw
			c.SavePassword = true
		}
	}
	db := strings.TrimPrefix(u.Path, "/")
	q := u.Query()
	// JDBC puts credentials in the query string.
	if v := q.Get("user"); v != "" && c.User == "" {
		c.User = v
	}
	if v := q.Get("password"); v != "" && c.Password == "" {
		c.Password, c.SavePassword = v, true
	}

	if c.Type == model.Redis {
		// redis://[user:password@]host:port[/db]; the database number isn't kept.
		if c.User == "default" {
			c.User = ""
		}
	} else if c.Type == model.PostgreSQL {
		c.InitialDatabase = db
		if m := q.Get("sslmode"); m != "" && m != "disable" {
			c.SSL.Enabled = true
			c.SSL.Mode = m
		}
		c.SSL.CAFile = q.Get("sslrootcert")
		c.SSL.CertFile = q.Get("sslcert")
		c.SSL.KeyFile = q.Get("sslkey")
		// Everything else becomes extra connection parameters.
		var extra []string
		for _, k := range sortedKeys(q) {
			switch k {
			case "sslmode", "sslrootcert", "sslcert", "sslkey", "user", "password":
				continue
			case "connect_timeout":
				c.Advanced.ConnectTimeoutSec, _ = strconv.Atoi(q.Get(k))
				continue
			}
			extra = append(extra, k+"="+q.Get(k))
		}
		c.Advanced.ConnectionParams = strings.Join(extra, "\n")
	} else {
		c.InitialDatabase = db
		switch strings.ToLower(firstOf(q, "ssl-mode", "sslMode", "tls", "ssl", "useSSL", "sslaccept")) {
		case "", "disabled", "false", "0":
		case "verify_ca", "verify_identity", "strict":
			c.SSL.Enabled, c.SSL.VerifyHost = true, true
		default:
			c.SSL.Enabled = true
		}
	}
	c.Preset = DetectPreset(c.Host)
	c.Name = defaultName(c)
	c.Defaults()
	return c, nil
}

// Format renders a profile as a URL without its password, for copying.
func Format(c model.Connection) string {
	scheme := "mysql"
	switch c.Type {
	case model.PostgreSQL:
		scheme = "postgresql"
	case model.MariaDB:
		scheme = "mariadb"
	case model.Redis:
		scheme = "redis"
		if c.SSL.Enabled {
			scheme = "rediss"
		}
	}
	u := url.URL{Scheme: scheme, Host: net.JoinHostPort(c.Host, strconv.Itoa(c.Port))}
	if c.User != "" {
		u.User = url.User(c.User)
	}
	q := url.Values{}
	if c.Type == model.PostgreSQL {
		u.Path = "/" + c.InitialDatabase
		if c.SSL.Enabled {
			mode := c.SSL.Mode
			if mode == "" {
				mode = "require"
			}
			q.Set("sslmode", mode)
		}
		for _, line := range strings.Split(c.Advanced.ConnectionParams, "\n") {
			if k, v, ok := strings.Cut(strings.TrimSpace(line), "="); ok {
				q.Set(k, v)
			}
		}
	} else if c.Type != model.Redis {
		if c.InitialDatabase != "" {
			u.Path = "/" + c.InitialDatabase
		}
		if c.SSL.Enabled {
			mode := "REQUIRED"
			if c.SSL.VerifyHost {
				mode = "VERIFY_IDENTITY"
			}
			q.Set("ssl-mode", mode)
		}
	}
	u.RawQuery = q.Encode()
	return u.String()
}

func firstOf(q url.Values, keys ...string) string {
	for _, k := range keys {
		if v := q.Get(k); v != "" {
			return v
		}
	}
	return ""
}

func sortedKeys(q url.Values) []string {
	keys := make([]string, 0, len(q))
	for k := range q {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func defaultName(c model.Connection) string {
	if p := PresetByID(c.Preset); p != nil {
		return p.Name
	}
	return c.Host
}
