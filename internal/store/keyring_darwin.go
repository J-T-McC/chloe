//go:build darwin

package store

import (
	"errors"

	"github.com/keybase/go-keychain"
	"github.com/zalando/go-keyring"
)

// Secrets are kept through the Security framework, so the Keychain only hands
// them to Chloe itself (by its code signature) and asks before giving them to
// anything else. Releases before this stored them through /usr/bin/security,
// which any process running as the user could ask for them without a prompt;
// those items are moved over on first read.
const keychainService = "app.chloe.desktop.secrets"

func keychainItem(key string) keychain.Item {
	item := keychain.NewItem()
	item.SetSecClass(keychain.SecClassGenericPassword)
	item.SetService(keychainService)
	item.SetAccount(key)
	return item
}

func osGet(key string) (string, error) {
	data, err := keychain.GetGenericPassword(keychainService, key, "", "")
	if err != nil {
		return "", err
	}
	if data != nil {
		return string(data), nil
	}
	return migrateLegacy(key)
}

// migrateLegacy moves a secret saved by an older release into Chloe's own item.
func migrateLegacy(key string) (string, error) {
	v, err := keyring.Get(keyringService, key)
	if err != nil {
		return "", keyring.ErrNotFound
	}
	if err := osSet(key, v); err != nil {
		return v, nil // still usable; try moving it again next time
	}
	_ = keyring.Delete(keyringService, key)
	return v, nil
}

func osSet(key, value string) error {
	item := keychainItem(key)
	item.SetLabel("Chloe")
	item.SetDescription("Chloe connection secret")
	item.SetData([]byte(value))
	err := keychain.AddItem(item)
	if errors.Is(err, keychain.ErrorDuplicateItem) {
		update := keychain.NewItem()
		update.SetData([]byte(value))
		err = keychain.UpdateItem(keychainItem(key), update)
	}
	return err
}

func osDelete(key string) error {
	err := keychain.DeleteItem(keychainItem(key))
	if err != nil && !errors.Is(err, keychain.ErrorItemNotFound) {
		return err
	}
	if err := keyring.Delete(keyringService, key); err != nil && !errors.Is(err, keyring.ErrNotFound) {
		return err
	}
	return nil
}
