//go:build !server

package main

import "net/http"

func checkServerMode() error { return nil }

// serverGuard only matters in the browser build: the desktop app's webview is the
// only thing that can reach its asset server.
func serverGuard(next http.Handler) http.Handler { return next }
