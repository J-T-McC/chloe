//go:build server

package main

import (
	"fmt"
	"net"
	"os"
)

// checkServerMode keeps the browser build (used by the e2e tests) away from real
// credentials. It has no login, so anything that can reach its port can use every
// saved connection: it only runs with in-memory secrets, and only on loopback
// unless CHLOE_ALLOW_REMOTE=1 says otherwise (a container publishing its port).
func checkServerMode() error {
	if os.Getenv("CHLOE_SECRETS") != "memory" {
		return fmt.Errorf("the browser build has no login, so it doesn't use the keychain: run it with CHLOE_SECRETS=memory (and CHLOE_CONFIG_DIR set to a test directory)")
	}
	host := os.Getenv("WAILS_SERVER_HOST")
	if host == "" || host == "localhost" || os.Getenv("CHLOE_ALLOW_REMOTE") == "1" {
		return nil
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		return nil
	}
	return fmt.Errorf("the browser build has no login; it only listens on loopback (WAILS_SERVER_HOST=%s refused, set CHLOE_ALLOW_REMOTE=1 to override)", host)
}
