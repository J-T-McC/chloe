package mdb

import (
	"context"
	"strings"
	"testing"
)

func TestUserCommands(t *testing.T) {
	cmds, err := UserCommands(nil, User{User: "app", DB: "shop", Password: "pw", Roles: []RoleRef{{Role: "readWrite", DB: "shop"}, {Role: ""}}, CustomData: "{ team: 'web' }"})
	if err != nil || len(cmds) != 1 || cmds[0].Shell != "db.getSiblingDB('shop').runCommand({ createUser: 'app', pwd: 'pw', roles: [ { role: 'readWrite', db: 'shop' } ], customData: { team: 'web' } })" {
		t.Fatalf("%v %+v", err, cmds)
	}
	old := User{User: "app", DB: "shop", Roles: []RoleRef{{Role: "readWrite", DB: "shop"}}}
	if cmds, _ := UserCommands(&old, old); len(cmds) != 0 {
		t.Errorf("no change should give no command: %+v", cmds)
	}
	cmds, _ = UserCommands(&old, User{User: "app", DB: "shop", Roles: []RoleRef{{Role: "read", DB: "shop"}}, Password: "new"})
	if len(cmds) != 1 || !strings.Contains(cmds[0].Shell, "updateUser: 'app', pwd: 'new', roles: [ { role: 'read', db: 'shop' } ]") {
		t.Errorf("update: %+v", cmds)
	}
	if _, err := UserCommands(&old, User{User: "other", DB: "shop"}); err == nil {
		t.Error("rename allowed")
	}
	if _, err := UserCommands(nil, User{User: "x", DB: "shop"}); err == nil {
		t.Error("new user without a password")
	}
	cmds, _ = RoleCommands(nil, Role{Role: "reporter", DB: "shop", Privileges: []Privilege{{DB: "shop", Collection: "orders", Actions: []string{"find", " "}}, {Cluster: true, Actions: []string{"serverStatus"}}}})
	if len(cmds) != 1 || cmds[0].Shell != "db.getSiblingDB('shop').runCommand({ createRole: 'reporter', privileges: [ { resource: { db: 'shop', collection: 'orders' }, actions: [ 'find' ] }, { resource: { cluster: true }, actions: [ 'serverStatus' ] } ], roles: [] })" {
		t.Errorf("role: %+v", cmds)
	}
}

func TestUsersAndRoles(t *testing.T) {
	c := open(t)
	ctx := context.Background()
	const db = "chloe_it_users"
	_ = c.Run(ctx, []Command{DropUserCommand(db, "app"), DropRoleCommand(db, "reporter")})
	defer c.DropDatabase(ctx, db)
	role := Role{Role: "reporter", DB: db, Privileges: []Privilege{{DB: db, Collection: "orders", Actions: []string{"find"}}}, Roles: []RoleRef{}}
	cmds, err := RoleCommands(nil, role)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Run(ctx, cmds); err != nil {
		t.Fatal(err)
	}
	defer c.Run(ctx, []Command{DropRoleCommand(db, "reporter")})
	user := User{User: "app", DB: db, Password: "secret", Roles: []RoleRef{{Role: "reporter", DB: db}, {Role: "read", DB: "shop"}}, CustomData: "{ team: 'web' }"}
	cmds, _ = UserCommands(nil, user)
	if err := c.Run(ctx, cmds); err != nil {
		t.Fatal(err)
	}
	defer c.Run(ctx, []Command{DropUserCommand(db, "app")})

	users, err := c.Users(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var got *User
	for i := range users {
		if users[i].User == "app" && users[i].DB == db {
			got = &users[i]
		}
	}
	if got == nil || len(got.Roles) != 2 || got.CustomData != "{ team: 'web' }" || len(got.Mechanisms) == 0 {
		t.Fatalf("listed user: %+v", got)
	}
	changed := *got
	changed.Roles = []RoleRef{{Role: "readWrite", DB: db}}
	changed.CustomData = ""
	cmds, _ = UserCommands(got, changed)
	if err := c.Run(ctx, cmds); err != nil {
		t.Fatal(err)
	}
	roles, err := c.Roles(ctx, db, true)
	if err != nil {
		t.Fatal(err)
	}
	if roles[0].Role != "reporter" || roles[0].Builtin || len(roles[0].Privileges) != 1 || roles[0].Privileges[0].Collection != "orders" {
		t.Errorf("roles: %+v", roles[0])
	}
	var builtin bool
	for _, r := range roles {
		builtin = builtin || r.Role == "readWrite" && r.Builtin
	}
	if !builtin {
		t.Error("built-in roles missing")
	}
	if _, err := RoleCommands(&roles[len(roles)-1], roles[len(roles)-1]); err == nil {
		t.Error("changing a built-in role allowed")
	}
}
