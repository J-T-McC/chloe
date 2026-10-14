package connurl

import (
	"strings"

	"chloe/internal/model"
)

// Preset is a hosted service shown in the New Connection picker. It uses an
// existing engine driver and only pre-fills settings and help text.
type Preset struct {
	ID          string       `json:"id"`
	Name        string       `json:"name"`
	Type        model.DBType `json:"type"`
	Description string       `json:"description"`
	// Help explains where to find the connection details in the service's console.
	Help string `json:"help"`
	// URLExample is shown as the placeholder of the "paste connection string" box.
	URLExample string           `json:"urlExample"`
	Defaults   model.Connection `json:"defaults"`
	hostSuffix []string
}

var presets = []Preset{
	{
		ID: "supabase", Name: "Supabase", Type: model.PostgreSQL,
		Description: "Supabase Postgres (direct or pooled)",
		Help: "Project → Connect → Connection string. Direct: db.<ref>.supabase.co:5432, user postgres. " +
			"Pooler: aws-0-<region>.pooler.supabase.com, port 5432 (session) or 6543 (transaction), user postgres.<ref>.",
		URLExample: "postgresql://postgres.<ref>:<password>@aws-0-us-east-1.pooler.supabase.com:5432/postgres",
		Defaults: model.Connection{Port: 5432, User: "postgres", InitialDatabase: "postgres",
			SSL: model.SSLConfig{Enabled: true, Mode: "require"}},
		hostSuffix: []string{".supabase.co", ".supabase.com"},
	},
	{
		ID: "neon", Name: "Neon", Type: model.PostgreSQL,
		Description: "Neon serverless Postgres",
		Help:        "Project dashboard → Connect. Use the endpoint host ep-…neon.tech; the database is usually neondb.",
		URLExample:  "postgresql://<user>:<password>@ep-xxxx-123456.us-east-2.aws.neon.tech/neondb?sslmode=require",
		Defaults: model.Connection{Port: 5432, InitialDatabase: "neondb",
			SSL: model.SSLConfig{Enabled: true, Mode: "require"}},
		hostSuffix: []string{".neon.tech"},
	},
	{
		ID: "rds-postgres", Name: "Amazon RDS for PostgreSQL", Type: model.PostgreSQL,
		Description: "RDS / Aurora PostgreSQL",
		Help:        "RDS console → Databases → Connectivity & security → Endpoint. Download the RDS CA bundle to verify certificates.",
		URLExample:  "postgresql://postgres:<password>@mydb.xxxxxxxx.us-east-1.rds.amazonaws.com:5432/postgres",
		Defaults: model.Connection{Port: 5432, User: "postgres", InitialDatabase: "postgres",
			SSL: model.SSLConfig{Enabled: true, Mode: "require"}},
		hostSuffix: []string{".rds.amazonaws.com"},
	},
	{
		ID: "rds-mysql", Name: "Amazon RDS for MySQL", Type: model.MySQL,
		Description: "RDS / Aurora MySQL",
		Help:        "RDS console → Databases → Connectivity & security → Endpoint.",
		URLExample:  "mysql://admin:<password>@mydb.xxxxxxxx.us-east-1.rds.amazonaws.com:3306",
		Defaults:    model.Connection{Port: 3306, User: "admin", SSL: model.SSLConfig{Enabled: true}},
	},
	{
		ID: "digitalocean-postgres", Name: "DigitalOcean Postgres", Type: model.PostgreSQL,
		Description: "DigitalOcean Managed PostgreSQL",
		Help:        "Databases → your cluster → Connection Details. Port 25060, or 25061 for the connection pool.",
		URLExample:  "postgresql://doadmin:<password>@db-postgresql-nyc1-12345-do-user-0.b.db.ondigitalocean.com:25060/defaultdb?sslmode=require",
		Defaults: model.Connection{Port: 25060, User: "doadmin", InitialDatabase: "defaultdb",
			SSL: model.SSLConfig{Enabled: true, Mode: "require"}},
		hostSuffix: []string{".db.ondigitalocean.com"},
	},
	{
		ID: "digitalocean-mysql", Name: "DigitalOcean MySQL", Type: model.MySQL,
		Description: "DigitalOcean Managed MySQL",
		Help:        "Databases → your cluster → Connection Details (port 25060).",
		URLExample:  "mysql://doadmin:<password>@db-mysql-nyc1-12345-do-user-0.b.db.ondigitalocean.com:25060/defaultdb?ssl-mode=REQUIRED",
		Defaults:    model.Connection{Port: 25060, User: "doadmin", SSL: model.SSLConfig{Enabled: true}},
	},
	{
		ID: "planetscale", Name: "PlanetScale", Type: model.MySQL,
		Description: "PlanetScale (MySQL-compatible)",
		Help:        "Database → Connect → Create password. Connections require TLS with certificate verification.",
		URLExample:  "mysql://<user>:<password>@aws.connect.psdb.cloud/<database>?sslaccept=strict",
		Defaults:    model.Connection{Port: 3306, SSL: model.SSLConfig{Enabled: true, VerifyHost: true}},
		hostSuffix:  []string{".psdb.cloud"},
	},
	{
		ID: "tidb-cloud", Name: "TiDB Cloud", Type: model.MySQL,
		Description: "TiDB Cloud (MySQL-compatible)",
		Help:        "Cluster → Connect. Serverless clusters use port 4000 and require TLS.",
		URLExample:  "mysql://<user>.root:<password>@gateway01.us-west-2.prod.aws.tidbcloud.com:4000/test",
		Defaults:    model.Connection{Port: 4000, SSL: model.SSLConfig{Enabled: true, VerifyHost: true}},
		hostSuffix:  []string{".tidbcloud.com"},
	},
}

// Presets lists the hosted-service presets.
func Presets() []Preset { return presets }

// PresetByID returns a preset or nil.
func PresetByID(id string) *Preset {
	for i := range presets {
		if presets[i].ID == id {
			return &presets[i]
		}
	}
	return nil
}

// DetectPreset recognises a hosted service from its hostname.
func DetectPreset(host string) string {
	h := strings.ToLower(host)
	for _, p := range presets {
		for _, s := range p.hostSuffix {
			if strings.HasSuffix(h, s) {
				return p.ID
			}
		}
	}
	// RDS hostnames don't say which engine; the caller's type decides.
	return ""
}
