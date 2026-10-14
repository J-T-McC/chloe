package services

import (
	"context"

	"chloe/internal/core"
	"chloe/internal/privs"
)

// UserService backs user/role management.
type UserService struct{ M *core.Manager }

func (s *UserService) List(ctx context.Context, connID string) ([]privs.UserInfo, error) {
	return s.M.ListUsers(ctx, connID)
}

func (s *UserService) Load(ctx context.Context, connID, name, host, database string) (privs.User, error) {
	return s.M.LoadUser(ctx, connID, name, host, database)
}

func (s *UserService) Meta(ctx context.Context, connID string) (privs.Meta, error) {
	return s.M.UserMeta(ctx, connID)
}

func (s *UserService) Preview(connID string, old *privs.User, u privs.User) ([]privs.Stmt, error) {
	return s.M.UserSQL(connID, old, u)
}

func (s *UserService) Apply(ctx context.Context, connID string, old *privs.User, u privs.User) core.ApplyResult {
	return s.M.ApplyUser(ctx, connID, old, u)
}

func (s *UserService) Drop(ctx context.Context, connID string, users []privs.UserInfo) core.ApplyResult {
	return s.M.DropUsers(ctx, connID, users)
}

// ObjectGrants lists every account's privileges on one object (Set Privileges).
func (s *UserService) ObjectGrants(ctx context.Context, connID string, target privs.Grant) ([]privs.ObjectGrant, error) {
	return s.M.ObjectGrants(ctx, connID, target)
}
