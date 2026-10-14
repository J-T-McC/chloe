//go:build darwin

package store

import (
	"os"
	"os/exec"
	"testing"

	"github.com/zalando/go-keyring"
)

// Touches the login keychain, so it only runs with CHLOE_KEYCHAIN_IT=1.
func TestKeychain(t *testing.T) {
	if os.Getenv("CHLOE_KEYCHAIN_IT") != "1" {
		t.Skip("set CHLOE_KEYCHAIN_IT=1 to use the login keychain")
	}
	k := Keyring{}
	key := "chloe-test:" + t.Name()
	t.Cleanup(func() { _ = k.Delete(key) })

	if err := k.Set(key, "one"); err != nil {
		t.Fatal(err)
	}
	if err := k.Set(key, "two"); err != nil {
		t.Fatal("update:", err)
	}
	if v, err := k.Get(key); err != nil || v != "two" {
		t.Fatalf("get %q %v", v, err)
	}
	// Not stored where the security tool's own items live.
	if out, err := exec.Command("security", "find-generic-password", "-s", keyringService, "-a", key).CombinedOutput(); err == nil {
		t.Fatalf("secret stored as a legacy item: %s", out)
	}
	if err := k.Delete(key); err != nil {
		t.Fatal(err)
	}
	if _, err := k.Get(key); err == nil {
		t.Fatal("deleted secret still readable")
	}

	// A secret saved by an older release moves to Chloe's item on first read.
	if err := keyring.Set(keyringService, key, "legacy"); err != nil {
		t.Fatal(err)
	}
	if v, err := k.Get(key); err != nil || v != "legacy" {
		t.Fatalf("legacy get %q %v", v, err)
	}
	if _, err := keyring.Get(keyringService, key); err == nil {
		t.Fatal("legacy item left behind after moving it")
	}
	if v, err := k.Get(key); err != nil || v != "legacy" {
		t.Fatalf("moved get %q %v", v, err)
	}
}
