//go:build server

package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestServerGuard(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})
	h := serverGuard(ok)
	call := func(method, host, path string, hdr map[string]string) int {
		r := httptest.NewRequest(method, "http://"+host+path, nil)
		r.Host = host
		for k, v := range hdr {
			r.Header.Set(k, v)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code
	}
	app := map[string]string{"x-wails-client-id": "c", "Origin": "http://localhost:8080"}
	for _, c := range []struct {
		name, method, host, path string
		hdr                      map[string]string
		want                     int
	}{
		{"page", "GET", "localhost:8080", "/", nil, 200},
		{"runtime call", "POST", "localhost:8080", "/wails/runtime", app, 200},
		{"ipv4 and ipv6 loopback", "POST", "[::1]:8080", "/wails/runtime", map[string]string{"x-wails-client-id": "c"}, 200},
		{"runtime.js", "GET", "127.0.0.1:8080", "/wails/runtime.js", nil, 200},
		{"img GET call", "GET", "localhost:8080", "/wails/runtime?object=0&method=0", nil, 403},
		{"form POST without the header", "POST", "localhost:8080", "/wails/runtime", nil, 403},
		{"other origin", "POST", "localhost:8080", "/wails/runtime", map[string]string{"x-wails-client-id": "c", "Origin": "https://evil.example"}, 403},
		{"DNS rebinding", "POST", "evil.example:8080", "/wails/runtime", map[string]string{"x-wails-client-id": "c", "Origin": "http://evil.example:8080"}, 403},
		{"cross-site subresource", "GET", "localhost:8080", "/assets/x.js", map[string]string{"Sec-Fetch-Site": "cross-site", "Sec-Fetch-Mode": "no-cors"}, 403},
		{"link from another site", "GET", "localhost:8080", "/", map[string]string{"Sec-Fetch-Site": "cross-site", "Sec-Fetch-Mode": "navigate"}, 200},
	} {
		if got := call(c.method, c.host, c.path, c.hdr); got != c.want {
			t.Errorf("%s: got %d, want %d", c.name, got, c.want)
		}
	}
}
