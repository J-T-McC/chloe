//go:build !darwin

package store

import (
	"errors"

	"github.com/zalando/go-keyring"
)

func osGet(key string) (string, error) { return keyring.Get(keyringService, key) }
func osSet(key, value string) error    { return keyring.Set(keyringService, key, value) }
func osDelete(key string) error {
	err := keyring.Delete(keyringService, key)
	if errors.Is(err, keyring.ErrNotFound) {
		return nil
	}
	return err
}
