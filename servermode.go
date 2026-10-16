//go:build server

package main

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
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

// serverGuard keeps other websites away from the bindings: a page open in any
// browser tab could otherwise call them (an <img> GET, a form POST) or, by DNS
// rebinding a name of its own to 127.0.0.1, read what they return. Requests must
// name a loopback host (unless remote use is allowed), come from the page's own
// origin, and runtime calls must be the runtime's own POST with its header,
// which a cross-site page can't send without a CORS preflight this never grants.
func serverGuard(next http.Handler) http.Handler {
	remote := os.Getenv("CHLOE_ALLOW_REMOTE") == "1"
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !remote && !loopbackHost(r.Host) {
			http.Error(w, "forbidden host", http.StatusForbidden)
			return
		}
		if o := r.Header.Get("Origin"); o != "" {
			if u, err := url.Parse(o); err != nil || u.Host != r.Host {
				http.Error(w, "cross-origin request refused", http.StatusForbidden)
				return
			}
		}
		if r.Header.Get("Sec-Fetch-Site") == "cross-site" && r.Header.Get("Sec-Fetch-Mode") != "navigate" {
			http.Error(w, "cross-site request refused", http.StatusForbidden)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/wails/runtime") && r.URL.Path != "/wails/runtime.js" &&
			(r.Method != http.MethodPost || r.Header.Get("x-wails-client-id") == "") {
			http.Error(w, "runtime calls are POSTs from the app", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func loopbackHost(hostport string) bool {
	host := hostport
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		host = h
	}
	host = strings.Trim(host, "[]")
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
