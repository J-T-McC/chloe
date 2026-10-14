package services

import (
	"context"

	"chloe/internal/core"
	"chloe/internal/rdb"
)

// RedisService backs the Redis key browser, value editors and console.
type RedisService struct{ M *core.Manager }

func (s *RedisService) Databases(ctx context.Context, connID string) ([]rdb.DBInfo, error) {
	kv, err := s.M.Redis(connID)
	if err != nil {
		return nil, err
	}
	return kv.Databases(ctx)
}

// ScanKeys continues a key scan from cursor ("0" to start).
func (s *RedisService) ScanKeys(ctx context.Context, connID string, db int, cursor, pattern, keyType string, count int) (rdb.ScanResult, error) {
	kv, err := s.M.Redis(connID)
	if err != nil {
		return rdb.ScanResult{}, err
	}
	return kv.ScanKeys(ctx, db, cursor, pattern, keyType, count)
}

func (s *RedisService) GetValue(ctx context.Context, connID string, db int, key rdb.Value, cursor string, count int) (rdb.KeyValue, error) {
	kv, err := s.M.Redis(connID)
	if err != nil {
		return rdb.KeyValue{}, err
	}
	return kv.GetValue(ctx, db, key, cursor, count)
}

func (s *RedisService) CreateKey(ctx context.Context, connID string, db int, nk rdb.NewKey) error {
	kv, err := s.M.Redis(connID)
	if err != nil {
		return err
	}
	return kv.CreateKey(ctx, db, nk)
}

func (s *RedisService) DeleteKeys(ctx context.Context, connID string, db int, keys []rdb.Value) (int64, error) {
	kv, err := s.M.Redis(connID)
	if err != nil {
		return 0, err
	}
	return kv.DeleteKeys(ctx, db, keys)
}

func (s *RedisService) RenameKey(ctx context.Context, connID string, db int, key, to rdb.Value) error {
	kv, err := s.M.Redis(connID)
	if err != nil {
		return err
	}
	return kv.RenameKey(ctx, db, key, to)
}

// SetTTL sets a key's expiry in seconds; 0 or less removes it.
func (s *RedisService) SetTTL(ctx context.Context, connID string, db int, key rdb.Value, ttl int64) error {
	kv, err := s.M.Redis(connID)
	if err != nil {
		return err
	}
	return kv.SetTTL(ctx, db, key, ttl)
}

func (s *RedisService) SetString(ctx context.Context, connID string, db int, key, value rdb.Value) error {
	kv, err := s.M.Redis(connID)
	if err != nil {
		return err
	}
	return kv.SetString(ctx, db, key, value)
}

func (s *RedisService) EditItem(ctx context.Context, connID string, db int, key rdb.Value, keyType string, e rdb.ItemEdit) error {
	kv, err := s.M.Redis(connID)
	if err != nil {
		return err
	}
	return kv.EditItem(ctx, db, key, keyType, e)
}

func (s *RedisService) DeleteItems(ctx context.Context, connID string, db int, key rdb.Value, keyType string, items []rdb.ItemRef) error {
	kv, err := s.M.Redis(connID)
	if err != nil {
		return err
	}
	return kv.DeleteItems(ctx, db, key, keyType, items)
}

func (s *RedisService) PushList(ctx context.Context, connID string, db int, key rdb.Value, values []rdb.Value, head bool) error {
	kv, err := s.M.Redis(connID)
	if err != nil {
		return err
	}
	return kv.PushList(ctx, db, key, values, head)
}

func (s *RedisService) AddStreamEntry(ctx context.Context, connID string, db int, key rdb.Value, id string, fields []rdb.Field) (string, error) {
	kv, err := s.M.Redis(connID)
	if err != nil {
		return "", err
	}
	return kv.AddStreamEntry(ctx, db, key, id, fields)
}

func (s *RedisService) FlushDB(ctx context.Context, connID string, db int) error {
	kv, err := s.M.Redis(connID)
	if err != nil {
		return err
	}
	return kv.FlushDB(ctx, db)
}

func (s *RedisService) Info(ctx context.Context, connID string) ([]rdb.InfoSection, error) {
	kv, err := s.M.Redis(connID)
	if err != nil {
		return nil, err
	}
	return kv.Info(ctx)
}

// Commands lists command names for console completion.
func (s *RedisService) Commands(ctx context.Context, connID string) ([]string, error) {
	kv, err := s.M.Redis(connID)
	if err != nil {
		return nil, err
	}
	return kv.Commands(ctx)
}

func (s *RedisService) OpenConsole(ctx context.Context, connID string, db int) (string, error) {
	return s.M.OpenRedisConsole(ctx, connID, db)
}

func (s *RedisService) ConsoleExec(ctx context.Context, id, line string) (core.RedisConsoleResult, error) {
	return s.M.RedisConsoleExec(ctx, id, line)
}

func (s *RedisService) CloseConsole(id string) { s.M.CloseRedisConsole(id) }
