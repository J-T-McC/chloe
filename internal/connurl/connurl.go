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

// Parse reads postgres://, postgresql://, mysql://, mariadb://, redis://, mongodb://,
// mongodb+srv:// and jdbc: URLs.
// The password, if present, is returned in the profile (it is not saved until the
// user saves the connection).
func Parse(raw string) (model.Connection, error) {
	var c model.Connection
	raw = strings.TrimSpace(raw)
	raw = strings.TrimPrefix(raw, "jdbc:")
	if l := strings.ToLower(raw); strings.HasPrefix(l, "mongodb://") || strings.HasPrefix(l, "mongodb+srv://") {
		return parseMongo(raw)
	}
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

// parseMongo reads mongodb://[user[:password]@]host1[:port1][,host2...][/db][?options]
// and mongodb+srv://. url.Parse can't: the authority may list several hosts.
func parseMongo(raw string) (model.Connection, error) {
	c := model.Connection{Type: model.MongoDB, SavePassword: true}
	scheme, rest, _ := strings.Cut(raw, "://")
	c.Mongo.SRV = strings.EqualFold(scheme, "mongodb+srv")
	rest, query, _ := strings.Cut(rest, "?")
	authority, path, _ := strings.Cut(rest, "/")
	if at := strings.LastIndex(authority, "@"); at >= 0 {
		user, pw, hasPW := strings.Cut(authority[:at], ":")
		var err error
		if c.User, err = url.PathUnescape(user); err != nil {
			return c, fmt.Errorf("invalid user name: %w", err)
		}
		if hasPW {
			if c.Password, err = url.PathUnescape(pw); err != nil {
				return c, fmt.Errorf("invalid password: %w", err)
			}
		}
		authority = authority[at+1:]
	}
	hosts := strings.Split(authority, ",")
	if authority == "" || len(hosts) == 0 {
		return c, errors.New("the URL names no host")
	}
	c.Host, c.Port = hosts[0], 27017
	if h, p, err := net.SplitHostPort(hosts[0]); err == nil {
		n, err := strconv.Atoi(p)
		if err != nil {
			return c, fmt.Errorf("invalid port %q", p)
		}
		c.Host, c.Port = h, n
	}
	c.InitialDatabase, _ = url.PathUnescape(path)
	q, err := url.ParseQuery(query)
	if err != nil {
		return c, fmt.Errorf("invalid options: %w", err)
	}
	c.Mongo.Topology = "standalone"
	if rs := q.Get("replicaSet"); rs != "" || len(hosts) > 1 && !c.Mongo.SRV {
		c.Mongo.Topology, c.Mongo.ReplicaSet = "replicaset", rs
		if rs == "" {
			c.Mongo.Topology = "shard" // several hosts, no set name: mongos routers
		}
		c.Mongo.Members = hosts
	}
	c.Mongo.ReadPreference = q.Get("readPreference")
	c.Mongo.AuthSource = q.Get("authSource")
	switch strings.ToUpper(q.Get("authMechanism")) {
	case "PLAIN":
		c.Mongo.Auth = "ldap"
	case "MONGODB-X509":
		c.Mongo.Auth = "x509"
	case "SCRAM-SHA-1", "SCRAM-SHA-256":
		c.Mongo.Auth, c.Mongo.Mechanism = "password", strings.ToUpper(q.Get("authMechanism"))
	default:
		c.Mongo.Auth = "password"
		if c.User == "" {
			c.Mongo.Auth = "none"
		}
	}
	if v := strings.ToLower(firstOf(q, "tls", "ssl")); v == "true" || c.Mongo.SRV && v != "false" {
		c.SSL.Enabled = true
	}
	c.SSL.CAFile = q.Get("tlsCAFile")
	c.SSL.CertFile = q.Get("tlsCertificateKeyFile")
	if ms, err := strconv.Atoi(q.Get("connectTimeoutMS")); err == nil {
		c.Advanced.ConnectTimeoutSec = (ms + 999) / 1000
	}
	c.Name = c.Host
	c.Defaults()
	return c, nil
}

func formatMongo(c model.Connection) string {
	m := c.Mongo
	var b strings.Builder
	b.WriteString("mongodb")
	if m.SRV {
		b.WriteString("+srv")
	}
	b.WriteString("://")
	if c.User != "" && m.Auth != "none" {
		// userinfo must escape "@" and ":", which PathEscape keeps
		b.WriteString(strings.ReplaceAll(url.QueryEscape(c.User), "+", "%20") + "@")
	}
	switch {
	case m.SRV:
		b.WriteString(c.Host)
	case m.Topology != "standalone" && m.Topology != "" && len(m.Members) > 0:
		var hosts []string
		for _, h := range m.Members {
			if h = strings.TrimSpace(h); h != "" {
				hosts = append(hosts, h)
			}
		}
		b.WriteString(strings.Join(hosts, ","))
	default:
		b.WriteString(net.JoinHostPort(c.Host, strconv.Itoa(c.Port)))
	}
	b.WriteString("/" + url.PathEscape(c.InitialDatabase))
	q := url.Values{}
	if m.ReplicaSet != "" && m.Topology == "replicaset" {
		q.Set("replicaSet", m.ReplicaSet)
	}
	if m.ReadPreference != "" {
		q.Set("readPreference", m.ReadPreference)
	}
	if m.AuthSource != "" {
		q.Set("authSource", m.AuthSource)
	}
	switch m.Auth {
	case "ldap":
		q.Set("authMechanism", "PLAIN")
	case "x509":
		q.Set("authMechanism", "MONGODB-X509")
	default:
		if m.Mechanism != "" {
			q.Set("authMechanism", m.Mechanism)
		}
	}
	if c.SSL.Enabled && !m.SRV {
		q.Set("tls", "true")
	}
	if e := q.Encode(); e != "" {
		b.WriteString("?" + e)
	}
	return b.String()
}

// Format renders a profile as a URL without its password, for copying.
func Format(c model.Connection) string {
	if c.Type == model.MongoDB {
		return formatMongo(c)
	}
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
