// Package mdb is Chloe's MongoDB client: databases and collections, documents as
// typed values (Val), indexes and validation, server information, and a JavaScript
// shell (goja) for the query editor and the console.
package mdb

import (
	"context"
	"errors"
	"fmt"
	"net"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/event"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/readpref"

	"chloe/internal/model"
	"chloe/internal/tlsconf"
	"chloe/internal/tunnel"
)

// Client is an open MongoDB connection profile.
type Client struct {
	cl      *mongo.Client
	version string
	// authDB is shown when the user may not list databases.
	authDB string
	conns  atomic.Int64
}

// srvName is a DNS name: letters, digits, hyphens and dots.
var srvName = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9.-]*[A-Za-z0-9])?$`)

// Options builds driver options from a connection profile.
func Options(c model.Connection, d tunnel.Dialer) (*options.ClientOptions, error) {
	m := c.Mongo
	o := options.Client().SetAppName("Chloe")
	if d != nil {
		o.SetDialer(d)
	}
	switch {
	case m.SRV:
		// The host goes into a URI, so only a DNS name may: a bad one is reported by Connect.
		if !srvName.MatchString(c.Host) {
			return nil, fmt.Errorf("%q isn't a DNS name for an SRV lookup", c.Host)
		}
		o.ApplyURI("mongodb+srv://" + c.Host + "/")
	case m.Topology == "replicaset" || m.Topology == "shard":
		hosts := make([]string, 0, len(m.Members))
		for _, h := range m.Members {
			if h = strings.TrimSpace(h); h != "" {
				if _, _, err := net.SplitHostPort(h); err != nil {
					h = net.JoinHostPort(h, "27017")
				}
				hosts = append(hosts, h)
			}
		}
		if len(hosts) == 0 {
			hosts = []string{net.JoinHostPort(c.Host, strconv.Itoa(c.Port))}
		}
		o.SetHosts(hosts)
	default:
		o.SetHosts([]string{net.JoinHostPort(c.Host, strconv.Itoa(c.Port))})
		// A standalone profile talks to the named server even if it is a replica set member.
		o.SetDirect(true)
	}
	if m.ReplicaSet != "" && m.Topology == "replicaset" {
		o.SetReplicaSet(m.ReplicaSet)
	}
	if m.ReadPreference != "" {
		mode, err := readpref.ModeFromString(m.ReadPreference)
		if err != nil {
			return nil, err
		}
		rp, err := readpref.New(mode)
		if err != nil {
			return nil, err
		}
		o.SetReadPreference(rp)
	}
	auth := m.Auth
	if auth == "" && c.User != "" {
		auth = "password"
	}
	switch auth {
	case "password":
		o.SetAuth(options.Credential{Username: c.User, Password: c.Password, PasswordSet: true,
			AuthSource: m.AuthSource, AuthMechanism: m.Mechanism})
	case "ldap":
		o.SetAuth(options.Credential{Username: c.User, Password: c.Password, PasswordSet: true,
			AuthSource: "$external", AuthMechanism: "PLAIN"})
	case "x509":
		o.SetAuth(options.Credential{Username: c.User, AuthSource: "$external", AuthMechanism: "MONGODB-X509"})
	}
	if c.SSL.Enabled || auth == "x509" {
		// The driver names each server for SNI and verification; c.Host may be
		// an SRV name or a list of hosts.
		tc, err := tlsconf.Config(c.SSL, "")
		if err != nil {
			return nil, err
		}
		o.SetTLSConfig(tc)
	}
	ct := secs(c.Advanced.ConnectTimeoutSec, 15)
	o.SetConnectTimeout(ct).SetServerSelectionTimeout(ct)
	if c.Advanced.SessionLimit > 0 {
		o.SetMaxPoolSize(uint64(c.Advanced.SessionLimit))
	}
	return o, nil
}

func secs(n, def int) time.Duration {
	if n <= 0 {
		n = def
	}
	return time.Duration(n) * time.Second
}

// Open connects and checks the server answers.
func Open(ctx context.Context, c model.Connection, d tunnel.Dialer) (*Client, error) {
	o, err := Options(c, d)
	if err != nil {
		return nil, err
	}
	cl := &Client{authDB: c.Mongo.AuthSource}
	if cl.authDB == "" && c.Mongo.Auth != "none" && c.User != "" {
		cl.authDB = "admin"
	}
	o.SetPoolMonitor(&event.PoolMonitor{Event: func(e *event.PoolEvent) {
		switch e.Type {
		case event.ConnectionCreated:
			cl.conns.Add(1)
		case event.ConnectionClosed:
			cl.conns.Add(-1)
		}
	}})
	mc, err := mongo.Connect(o)
	if err != nil {
		return nil, err
	}
	cl.cl = mc
	var info struct {
		Version string `bson:"version"`
	}
	if err := mc.Database("admin").RunCommand(ctx, bson.D{{Key: "buildInfo", Value: 1}}).Decode(&info); err != nil {
		mc.Disconnect(context.Background())
		return nil, err
	}
	cl.version = "MongoDB " + info.Version
	return cl, nil
}

func (c *Client) ServerVersion() string { return c.version }

func (c *Client) OpenConnections() int { return int(c.conns.Load()) }

func (c *Client) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return c.cl.Disconnect(ctx)
}

// Mongo returns the underlying driver client.
func (c *Client) Mongo() *mongo.Client { return c.cl }

// ---- databases ----------------------------------------------------------------------

type DBInfo struct {
	Name  string `json:"name"`
	Size  int64  `json:"size"` // on disk, bytes
	Empty bool   `json:"empty"`
}

// Databases lists the databases the user can see. A user without the
// listDatabases privilege still sees its authentication database.
func (c *Client) Databases(ctx context.Context) ([]DBInfo, error) {
	res, err := c.cl.ListDatabases(ctx, bson.D{}, options.ListDatabases().SetAuthorizedDatabases(true))
	if err != nil {
		if c.authDB != "" && isUnauthorized(err) {
			return []DBInfo{{Name: c.authDB}}, nil
		}
		return nil, err
	}
	out := make([]DBInfo, 0, len(res.Databases))
	for _, d := range res.Databases {
		out = append(out, DBInfo{Name: d.Name, Size: d.SizeOnDisk, Empty: d.Empty})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func isUnauthorized(err error) bool {
	var ce mongo.CommandError
	return errors.As(err, &ce) && (ce.Code == 13 || ce.Name == "Unauthorized")
}

// DropDatabase drops a database and all its collections.
func (c *Client) DropDatabase(ctx context.Context, db string) error {
	return c.cl.Database(db).Drop(ctx)
}

// CreateDatabase creates a database by creating its first collection, since
// MongoDB has no empty databases.
func (c *Client) CreateDatabase(ctx context.Context, db, collection string) error {
	if strings.TrimSpace(collection) == "" {
		return fmt.Errorf("a new database needs a first collection")
	}
	return c.cl.Database(db).CreateCollection(ctx, collection)
}

// RunCommand runs a database command and returns the reply.
func (c *Client) RunCommand(ctx context.Context, db string, cmd bson.D) (bson.D, error) {
	var out bson.D
	err := c.cl.Database(db).RunCommand(ctx, cmd).Decode(&out)
	return out, err
}

// ---- collections ----------------------------------------------------------------

// CollInfo describes a collection or view.
type CollInfo struct {
	Name string `json:"name"`
	// Type is collection, view or timeseries.
	Type        string `json:"type"`
	Count       int64  `json:"count"`
	Size        int64  `json:"size"`
	StorageSize int64  `json:"storageSize"`
	AvgObjSize  int64  `json:"avgObjSize"`
	Indexes     int    `json:"indexes"`
	IndexSize   int64  `json:"indexSize"`
	Capped      bool   `json:"capped"`
	ViewOn      string `json:"viewOn,omitempty"`
	// Options is the full options document from listCollections.
	Options Val `json:"options"`
}

// Collections lists a database's collections and views. With stats, each
// collection also reports its document count and sizes.
func (c *Client) Collections(ctx context.Context, db string, stats bool) ([]CollInfo, error) {
	cur, err := c.cl.Database(db).ListCollections(ctx, bson.D{})
	if err != nil {
		return nil, err
	}
	var specs []struct {
		Name    string `bson:"name"`
		Type    string `bson:"type"`
		Options bson.D `bson:"options"`
	}
	if err := cur.All(ctx, &specs); err != nil {
		return nil, err
	}
	out := make([]CollInfo, 0, len(specs))
	for _, s := range specs {
		if strings.HasPrefix(s.Name, "system.") {
			continue
		}
		ci := CollInfo{Name: s.Name, Type: s.Type, Options: ToVal(s.Options)}
		for _, e := range s.Options {
			switch e.Key {
			case "capped":
				ci.Capped, _ = e.Value.(bool)
			case "viewOn":
				ci.ViewOn, _ = e.Value.(string)
			}
		}
		out = append(out, ci)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	if stats {
		var wg sync.WaitGroup
		sem := make(chan struct{}, 8)
		for i := range out {
			if out[i].Type == "view" {
				continue
			}
			ci := &out[i]
			wg.Go(func() {
				sem <- struct{}{}
				defer func() { <-sem }()
				c.collStats(ctx, db, ci)
			})
		}
		wg.Wait()
	}
	return out, nil
}

func (c *Client) collStats(ctx context.Context, db string, ci *CollInfo) {
	cur, err := c.cl.Database(db).Collection(ci.Name).Aggregate(ctx,
		bson.A{bson.D{{Key: "$collStats", Value: bson.D{{Key: "storageStats", Value: bson.D{}}}}}})
	if err != nil {
		return
	}
	defer cur.Close(ctx)
	if !cur.Next(ctx) {
		return
	}
	var st struct {
		S struct {
			Count          int64            `bson:"count"`
			Size           int64            `bson:"size"`
			StorageSize    int64            `bson:"storageSize"`
			AvgObjSize     int64            `bson:"avgObjSize"`
			NIndexes       int              `bson:"nindexes"`
			TotalIndexSize int64            `bson:"totalIndexSize"`
			IndexSizes     map[string]int64 `bson:"indexSizes"`
		} `bson:"storageStats"`
	}
	if err := bson.Unmarshal(cur.Current, &st); err != nil {
		return
	}
	ci.Count, ci.Size, ci.StorageSize, ci.AvgObjSize = st.S.Count, st.S.Size, st.S.StorageSize, st.S.AvgObjSize
	ci.Indexes, ci.IndexSize = st.S.NIndexes, st.S.TotalIndexSize
}

// CollOptions are the settings of a new collection or view.
type CollOptions struct {
	Capped bool  `json:"capped"`
	Size   int64 `json:"size"` // capped: maximum bytes
	Max    int64 `json:"max"`  // capped: maximum documents
	// Validator is a query document in shell syntax.
	Validator        string `json:"validator"`
	ValidationLevel  string `json:"validationLevel"`  // off, strict, moderate
	ValidationAction string `json:"validationAction"` // error, warn
	// Collation is a collation document in shell syntax.
	Collation string `json:"collation"`
	// Views: the source collection and the pipeline in shell syntax.
	ViewOn   string `json:"viewOn"`
	Pipeline string `json:"pipeline"`
	// Time series collections.
	TimeField   string `json:"timeField"`
	MetaField   string `json:"metaField"`
	Granularity string `json:"granularity"`
	ExpireAfter int64  `json:"expireAfterSeconds"`
}

// CreateCommand returns the create command for a collection or view.
func CreateCommand(name string, o CollOptions) (bson.D, error) {
	cmd := bson.D{{Key: "create", Value: name}}
	if o.ViewOn != "" {
		cmd = append(cmd, bson.E{Key: "viewOn", Value: o.ViewOn})
		p := bson.A{}
		if strings.TrimSpace(o.Pipeline) != "" {
			v, err := EvalShell(o.Pipeline)
			if err != nil {
				return nil, fmt.Errorf("pipeline: %w", err)
			}
			a, ok := v.(bson.A)
			if !ok {
				return nil, fmt.Errorf("pipeline: expected an array of stages")
			}
			p = a
		}
		cmd = append(cmd, bson.E{Key: "pipeline", Value: p})
	}
	if o.Capped {
		cmd = append(cmd, bson.E{Key: "capped", Value: true}, bson.E{Key: "size", Value: o.Size})
		if o.Max > 0 {
			cmd = append(cmd, bson.E{Key: "max", Value: o.Max})
		}
	}
	if o.TimeField != "" {
		ts := bson.D{{Key: "timeField", Value: o.TimeField}}
		if o.MetaField != "" {
			ts = append(ts, bson.E{Key: "metaField", Value: o.MetaField})
		}
		if o.Granularity != "" {
			ts = append(ts, bson.E{Key: "granularity", Value: o.Granularity})
		}
		cmd = append(cmd, bson.E{Key: "timeseries", Value: ts})
		if o.ExpireAfter > 0 {
			cmd = append(cmd, bson.E{Key: "expireAfterSeconds", Value: o.ExpireAfter})
		}
	}
	vcmd, err := validationFields(o)
	if err != nil {
		return nil, err
	}
	cmd = append(cmd, vcmd...)
	if strings.TrimSpace(o.Collation) != "" {
		v, err := EvalDoc(o.Collation)
		if err != nil {
			return nil, fmt.Errorf("collation: %w", err)
		}
		cmd = append(cmd, bson.E{Key: "collation", Value: v})
	}
	return cmd, nil
}

func validationFields(o CollOptions) (bson.D, error) {
	var out bson.D
	if strings.TrimSpace(o.Validator) != "" {
		v, err := EvalDoc(o.Validator)
		if err != nil {
			return nil, fmt.Errorf("validator: %w", err)
		}
		out = append(out, bson.E{Key: "validator", Value: v})
	}
	if o.ValidationLevel != "" {
		out = append(out, bson.E{Key: "validationLevel", Value: o.ValidationLevel})
	}
	if o.ValidationAction != "" {
		out = append(out, bson.E{Key: "validationAction", Value: o.ValidationAction})
	}
	return out, nil
}

// withValidationDefaults spells out the defaults, so choosing "(default)" in the
// designer resets a level or action set before.
func withValidationDefaults(o CollOptions) CollOptions {
	if o.ValidationLevel == "" {
		o.ValidationLevel = "strict"
	}
	if o.ValidationAction == "" {
		o.ValidationAction = "error"
	}
	return o
}

// CreateCollection creates a collection or, with ViewOn set, a view.
func (c *Client) CreateCollection(ctx context.Context, db, name string, o CollOptions) error {
	cmd, err := CreateCommand(name, o)
	if err != nil {
		return err
	}
	_, err = c.RunCommand(ctx, db, cmd)
	return err
}

// SetValidation replaces a collection's validator, level and action (collMod).
// An empty validator removes validation.
func (c *Client) SetValidation(ctx context.Context, db, name string, o CollOptions) error {
	cmd := bson.D{{Key: "collMod", Value: name}}
	v, err := validationFields(withValidationDefaults(o))
	if err != nil {
		return err
	}
	if strings.TrimSpace(o.Validator) == "" {
		cmd = append(cmd, bson.E{Key: "validator", Value: bson.D{}})
	}
	cmd = append(cmd, v...)
	_, err = c.RunCommand(ctx, db, cmd)
	return err
}

// SetViewPipeline changes the source and pipeline of a view (collMod).
func (c *Client) SetViewPipeline(ctx context.Context, db, name, viewOn, pipeline string) error {
	create, err := CreateCommand(name, CollOptions{ViewOn: viewOn, Pipeline: pipeline})
	if err != nil {
		return err
	}
	create[0].Key = "collMod"
	_, err = c.RunCommand(ctx, db, create)
	return err
}

func (c *Client) DropCollection(ctx context.Context, db, name string) error {
	return c.cl.Database(db).Collection(name).Drop(ctx)
}

// EmptyCollection deletes every document but keeps the collection and its indexes.
func (c *Client) EmptyCollection(ctx context.Context, db, name string) (int64, error) {
	r, err := c.cl.Database(db).Collection(name).DeleteMany(ctx, bson.D{})
	if err != nil {
		return 0, err
	}
	return r.DeletedCount, nil
}

func (c *Client) RenameCollection(ctx context.Context, db, from, to string) error {
	_, err := c.RunCommand(ctx, "admin", bson.D{
		{Key: "renameCollection", Value: db + "." + from}, {Key: "to", Value: db + "." + to}})
	return err
}

// DuplicateCollection copies a collection's indexes and, with data, its documents.
func (c *Client) DuplicateCollection(ctx context.Context, db, from, to string, data bool) error {
	d := c.cl.Database(db)
	if err := d.CreateCollection(ctx, to); err != nil {
		return err
	}
	idx, err := c.Indexes(ctx, db, from)
	if err != nil {
		return err
	}
	for _, ix := range idx {
		if ix.Name == "_id_" {
			continue
		}
		if _, err := c.RunCommand(ctx, db, bson.D{{Key: "createIndexes", Value: to},
			{Key: "indexes", Value: bson.A{ix.spec}}}); err != nil {
			return err
		}
	}
	if !data {
		return nil
	}
	cur, err := d.Collection(from).Aggregate(ctx, bson.A{bson.D{{Key: "$merge", Value: bson.D{{Key: "into", Value: to}}}}})
	if err != nil {
		return err
	}
	return cur.Close(ctx)
}
