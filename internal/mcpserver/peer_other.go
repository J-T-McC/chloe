//go:build !darwin && !linux

package mcpserver

import "net"

// samePeer relies on the socket file's permissions where peer credentials aren't available.
func samePeer(net.Conn) error { return nil }
