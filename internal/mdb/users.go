package mdb

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// RoleRef names a role in a database.
type RoleRef struct {
	Role string `json:"role"`
	DB   string `json:"db"`
}

// User is a database user. Password is only sent when it changes.
type User struct {
	User       string    `json:"user"`
	DB         string    `json:"db"`
	Roles      []RoleRef `json:"roles"`
	Mechanisms []string  `json:"mechanisms"`
	// CustomData is a document in shell syntax ("" for none).
	CustomData string `json:"customData"`
	Password   string `json:"password,omitempty"`
}

// Privilege allows actions on a resource: a collection (empty for every collection),
// a database (empty for every database), or the cluster.
type Privilege struct {
	DB         string   `json:"db"`
	Collection string   `json:"collection"`
	Cluster    bool     `json:"cluster"`
	Actions    []string `json:"actions"`
}

// Role is a user-defined or built-in role.
type Role struct {
	Role       string      `json:"role"`
	DB         string      `json:"db"`
	Builtin    bool        `json:"builtin"`
	Roles      []RoleRef   `json:"roles"`
	Privileges []Privilege `json:"privileges"`
}

// Command is one command to run in a database, with its shell text for previews.
type Command struct {
	DB    string `json:"db"`
	Shell string `json:"shell"`
	cmd   bson.D
}

func command(db string, cmd bson.D) Command {
	return Command{DB: db, cmd: cmd, Shell: "db.getSiblingDB(" + Format(Val{T: "string", V: db}, "", false) + ").runCommand(" + Format(ToVal(cmd), "", false) + ")"}
}

// Users lists the users of every database (or, without the privilege, of the
// authentication database).
func (c *Client) Users(ctx context.Context) ([]User, error) {
	r, err := c.RunCommand(ctx, "admin", bson.D{{Key: "usersInfo", Value: bson.D{{Key: "forAllDBs", Value: true}}}})
	if err != nil && c.authDB != "" {
		r, err = c.RunCommand(ctx, c.authDB, bson.D{{Key: "usersInfo", Value: 1}})
	}
	if err != nil {
		return nil, err
	}
	var res struct {
		Users []struct {
			User       string    `bson:"user"`
			DB         string    `bson:"db"`
			Roles      []RoleRef `bson:"roles"`
			Mechanisms []string  `bson:"mechanisms"`
			CustomData bson.D    `bson:"customData"`
		} `bson:"users"`
	}
	if err := decodeD(r, &res); err != nil {
		return nil, err
	}
	out := make([]User, 0, len(res.Users))
	for _, u := range res.Users {
		x := User{User: u.User, DB: u.DB, Roles: u.Roles, Mechanisms: u.Mechanisms}
		if x.Roles == nil {
			x.Roles = []RoleRef{}
		}
		if len(u.CustomData) > 0 {
			x.CustomData = Format(ToVal(u.CustomData), "", false)
		}
		out = append(out, x)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].DB != out[j].DB {
			return out[i].DB < out[j].DB
		}
		return out[i].User < out[j].User
	})
	return out, nil
}

func decodeD(d bson.D, v any) error {
	raw, err := bson.Marshal(d)
	if err != nil {
		return err
	}
	return bson.Unmarshal(raw, v)
}

// Roles lists the roles of db, with the built-in ones when builtin is set.
func (c *Client) Roles(ctx context.Context, db string, builtin bool) ([]Role, error) {
	r, err := c.RunCommand(ctx, db, bson.D{{Key: "rolesInfo", Value: 1}, {Key: "showPrivileges", Value: true}, {Key: "showBuiltinRoles", Value: builtin}})
	if err != nil {
		return nil, err
	}
	var res struct {
		Roles []struct {
			Role       string    `bson:"role"`
			DB         string    `bson:"db"`
			IsBuiltin  bool      `bson:"isBuiltin"`
			Roles      []RoleRef `bson:"roles"`
			Privileges []struct {
				Resource struct {
					DB         *string `bson:"db"`
					Collection *string `bson:"collection"`
					Cluster    bool    `bson:"cluster"`
				} `bson:"resource"`
				Actions []string `bson:"actions"`
			} `bson:"privileges"`
		} `bson:"roles"`
	}
	if err := decodeD(r, &res); err != nil {
		return nil, err
	}
	out := make([]Role, 0, len(res.Roles))
	for _, x := range res.Roles {
		role := Role{Role: x.Role, DB: x.DB, Builtin: x.IsBuiltin, Roles: x.Roles, Privileges: []Privilege{}}
		if role.Roles == nil {
			role.Roles = []RoleRef{}
		}
		for _, p := range x.Privileges {
			pv := Privilege{Cluster: p.Resource.Cluster, Actions: p.Actions}
			if p.Resource.DB != nil {
				pv.DB = *p.Resource.DB
			}
			if p.Resource.Collection != nil {
				pv.Collection = *p.Resource.Collection
			}
			role.Privileges = append(role.Privileges, pv)
		}
		out = append(out, role)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Builtin != out[j].Builtin {
			return !out[i].Builtin
		}
		return out[i].Role < out[j].Role
	})
	return out, nil
}

func roleArray(rs []RoleRef) bson.A {
	a := bson.A{}
	for _, r := range rs {
		if strings.TrimSpace(r.Role) == "" {
			continue
		}
		a = append(a, bson.D{{Key: "role", Value: r.Role}, {Key: "db", Value: r.DB}})
	}
	return a
}

// UserCommands creates u, or with old, updates it to u.
func UserCommands(old *User, u User) ([]Command, error) {
	if strings.TrimSpace(u.User) == "" || strings.TrimSpace(u.DB) == "" {
		return nil, fmt.Errorf("a user needs a name and a database")
	}
	var custom bson.D
	if strings.TrimSpace(u.CustomData) != "" {
		var err error
		if custom, err = EvalDoc(u.CustomData); err != nil {
			return nil, fmt.Errorf("custom data: %w", err)
		}
	}
	if old == nil {
		if u.Password == "" && u.DB != "$external" {
			return nil, fmt.Errorf("a new user needs a password")
		}
		cmd := bson.D{{Key: "createUser", Value: u.User}}
		if u.DB != "$external" {
			cmd = append(cmd, bson.E{Key: "pwd", Value: u.Password})
		}
		cmd = append(cmd, bson.E{Key: "roles", Value: roleArray(u.Roles)})
		if len(u.Mechanisms) > 0 {
			cmd = append(cmd, bson.E{Key: "mechanisms", Value: u.Mechanisms})
		}
		if custom != nil {
			cmd = append(cmd, bson.E{Key: "customData", Value: custom})
		}
		return []Command{command(u.DB, cmd)}, nil
	}
	if old.User != u.User || old.DB != u.DB {
		return nil, fmt.Errorf("MongoDB can't rename users; create a new one instead")
	}
	cmd := bson.D{{Key: "updateUser", Value: u.User}}
	if u.Password != "" {
		cmd = append(cmd, bson.E{Key: "pwd", Value: u.Password})
		if len(u.Mechanisms) > 0 {
			cmd = append(cmd, bson.E{Key: "mechanisms", Value: u.Mechanisms})
		}
	}
	if !sameRoles(old.Roles, u.Roles) {
		cmd = append(cmd, bson.E{Key: "roles", Value: roleArray(u.Roles)})
	}
	if old.CustomData != u.CustomData {
		if custom == nil {
			custom = bson.D{}
		}
		cmd = append(cmd, bson.E{Key: "customData", Value: custom})
	}
	if len(cmd) == 1 {
		return nil, nil
	}
	return []Command{command(u.DB, cmd)}, nil
}

func sameRoles(a, b []RoleRef) bool {
	key := func(rs []RoleRef) string {
		s := make([]string, 0, len(rs))
		for _, r := range rs {
			if strings.TrimSpace(r.Role) != "" {
				s = append(s, r.Role+"@"+r.DB)
			}
		}
		sort.Strings(s)
		return strings.Join(s, ",")
	}
	return key(a) == key(b)
}

func privilegeArray(ps []Privilege) bson.A {
	a := bson.A{}
	for _, p := range ps {
		var actions bson.A
		for _, x := range p.Actions {
			if x = strings.TrimSpace(x); x != "" {
				actions = append(actions, x)
			}
		}
		if len(actions) == 0 {
			continue
		}
		res := bson.D{{Key: "db", Value: p.DB}, {Key: "collection", Value: p.Collection}}
		if p.Cluster {
			res = bson.D{{Key: "cluster", Value: true}}
		}
		a = append(a, bson.D{{Key: "resource", Value: res}, {Key: "actions", Value: actions}})
	}
	return a
}

// RoleCommands creates r, or with old, updates it to r.
func RoleCommands(old *Role, r Role) ([]Command, error) {
	if strings.TrimSpace(r.Role) == "" || strings.TrimSpace(r.DB) == "" {
		return nil, fmt.Errorf("a role needs a name and a database")
	}
	if old == nil {
		return []Command{command(r.DB, bson.D{{Key: "createRole", Value: r.Role},
			{Key: "privileges", Value: privilegeArray(r.Privileges)}, {Key: "roles", Value: roleArray(r.Roles)}})}, nil
	}
	if old.Builtin {
		return nil, fmt.Errorf("built-in roles can't be changed")
	}
	if old.Role != r.Role || old.DB != r.DB {
		return nil, fmt.Errorf("MongoDB can't rename roles; create a new one instead")
	}
	cmd := bson.D{{Key: "updateRole", Value: r.Role}}
	if a, b := privilegeArray(old.Privileges), privilegeArray(r.Privileges); Format(ToVal(a), "", false) != Format(ToVal(b), "", false) {
		cmd = append(cmd, bson.E{Key: "privileges", Value: b})
	}
	if !sameRoles(old.Roles, r.Roles) {
		cmd = append(cmd, bson.E{Key: "roles", Value: roleArray(r.Roles)})
	}
	if len(cmd) == 1 {
		return nil, nil
	}
	return []Command{command(r.DB, cmd)}, nil
}

// Run executes commands in order, stopping at the first error.
func (c *Client) Run(ctx context.Context, cmds []Command) error {
	for _, cmd := range cmds {
		if _, err := c.RunCommand(ctx, cmd.DB, cmd.cmd); err != nil {
			return fmt.Errorf("%s: %w", cmd.Shell, err)
		}
	}
	return nil
}

func DropUserCommand(db, user string) Command {
	return command(db, bson.D{{Key: "dropUser", Value: user}})
}

func DropRoleCommand(db, role string) Command {
	return command(db, bson.D{{Key: "dropRole", Value: role}})
}

// Actions lists privilege actions for the role editor.
var Actions = []string{
	"find", "insert", "update", "remove", "bypassDocumentValidation", "useUUID",
	"changeStream", "collMod", "collStats", "compact", "convertToCapped", "createCollection", "createIndex",
	"dbHash", "dbStats", "dropCollection", "dropDatabase", "dropIndex", "enableProfiler", "killCursors",
	"listCollections", "listIndexes", "planCacheRead", "planCacheWrite", "reIndex", "renameCollectionSameDB",
	"storageDetails", "validate",
	"changeCustomData", "changeOwnCustomData", "changeOwnPassword", "changePassword", "createRole", "createUser",
	"dropRole", "dropUser", "grantRole", "revokeRole", "setAuthenticationRestriction", "viewRole", "viewUser",
	"listDatabases", "listSessions", "killop", "killAnySession", "inprog", "serverStatus", "hostInfo",
	"getParameter", "setParameter", "getCmdLineOpts", "getLog", "logRotate", "top", "netstat", "connPoolStats",
	"replSetGetStatus", "replSetGetConfig", "replSetConfigure", "replSetStateChange", "shutdown", "fsync",
	"listShards", "addShard", "removeShard", "enableSharding", "splitChunk", "moveChunk", "flushRouterConfig",
}
