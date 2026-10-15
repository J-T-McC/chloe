package mcpserver

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// The socket serves the same MCP endpoints as HTTP to `chloe mcp`, a stdio bridge
// that MCP clients launch. It lives in Chloe's config folder (only the user can
// reach it) and checks the peer's user, so no other program can pose as Chloe
// the way one could by taking the TCP port first.
//
// A connection starts with one line, {"token": "..."}, answered with {"ok": true}
// or {"error": "..."}; after that it carries newline-delimited MCP messages.

// Hello is the bridge's first line.
type Hello struct {
	Token string `json:"token"`
}

// Welcome is the server's answer to Hello.
type Welcome struct {
	OK    bool   `json:"ok,omitempty"`
	Error string `json:"error,omitempty"`
}

// maxSocketPath is the macOS limit on a Unix socket path (sun_path).
const maxSocketPath = 103

type socketServer struct {
	ln    net.Listener
	path  string
	mu    sync.Mutex
	conns map[net.Conn]string // endpoint ID by connection
}

func (s *Server) listenSocket(path string) (*socketServer, error) {
	if len(path) > maxSocketPath {
		return nil, fmt.Errorf("the socket path is too long for the system: %s", path)
	}
	// A socket left by a crash would block the listen; only a live Chloe answers.
	if c, err := net.DialTimeout("unix", path, 200*time.Millisecond); err == nil {
		c.Close()
		return nil, errors.New("another Chloe is already serving MCP from this config folder")
	}
	_ = os.Remove(path)
	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, fmt.Errorf("couldn't listen on %s: %w", path, err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		ln.Close()
		return nil, err
	}
	ss := &socketServer{ln: ln, path: path, conns: map[net.Conn]string{}}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go s.serveConn(ss, c)
		}
	}()
	return ss, nil
}

func (s *Server) serveConn(ss *socketServer, c net.Conn) {
	defer c.Close()
	if err := samePeer(c); err != nil {
		writeWelcome(c, Welcome{Error: err.Error()})
		return
	}
	r := bufio.NewReader(c)
	_ = c.SetReadDeadline(time.Now().Add(10 * time.Second))
	line, err := r.ReadBytes('\n')
	if err != nil {
		return
	}
	_ = c.SetReadDeadline(time.Time{})
	var h Hello
	if json.Unmarshal(line, &h) != nil {
		writeWelcome(c, Welcome{Error: "expected a hello line"})
		return
	}
	e, ok := s.endpointForToken(h.Token)
	if !ok {
		writeWelcome(c, Welcome{Error: "unknown or disabled endpoint token"})
		return
	}
	if !writeWelcome(c, Welcome{OK: true}) {
		return
	}
	ss.mu.Lock()
	ss.conns[c] = e.ID
	ss.mu.Unlock()
	defer func() {
		ss.mu.Lock()
		delete(ss.conns, c)
		ss.mu.Unlock()
	}()
	// The reader keeps whatever followed the hello line in its buffer.
	t := &mcp.IOTransport{Reader: readCloser{r, c}, Writer: c}
	sess, err := s.serverFor(e.ID).Connect(context.Background(), t, nil)
	if err != nil {
		return
	}
	_ = sess.Wait()
}

func writeWelcome(c net.Conn, w Welcome) bool {
	data, _ := json.Marshal(w)
	_, err := c.Write(append(data, '\n'))
	return err == nil
}

type readCloser struct {
	io.Reader
	io.Closer
}

// drop closes the connections of endpoints that are gone or disabled.
func (ss *socketServer) drop(keep func(id string) bool) {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	for c, id := range ss.conns {
		if !keep(id) {
			c.Close()
		}
	}
}

func (ss *socketServer) close() {
	ss.ln.Close()
	ss.drop(func(string) bool { return false })
	_ = os.Remove(ss.path)
}
