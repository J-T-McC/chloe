package tunnel

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"

	"chloe/internal/model"
)

// A host that accepts TCP but never sends an SSH banner (wrong port, stuck sshd,
// middlebox) must fail within the timeout instead of hanging the caller.
func TestSSHSilentHostTimesOut(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			defer c.Close()
		}
	}()
	addr := ln.Addr().(*net.TCPAddr)
	cfg := model.SSHConfig{Enabled: true, Host: "127.0.0.1", Port: addr.Port, User: "u", Password: "p"}

	done := make(chan error, 1)
	go func() {
		_, err := newSSH(context.Background(), cfg, 300*time.Millisecond)
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected an error")
		}
		t.Log(err)
	case <-time.After(5 * time.Second):
		t.Fatal("SSH connect hung on a silent host")
	}
}

// Cancelling the context aborts a handshake in progress.
func TestSSHHandshakeHonoursContext(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			defer c.Close()
		}
	}()
	addr := ln.Addr().(*net.TCPAddr)
	cfg := model.SSHConfig{Enabled: true, Host: "127.0.0.1", Port: addr.Port, User: "u", Password: "p"}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := newSSH(ctx, cfg, time.Minute); err == nil {
		t.Fatal("expected an error")
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Fatalf("took %v after the context was cancelled", d)
	}
}

// testServer runs an SSH server offering the given host keys and accepting password "pw".
func testServer(t *testing.T, hostKeys ...ssh.Signer) int {
	t.Helper()
	conf := &ssh.ServerConfig{PasswordCallback: func(_ ssh.ConnMetadata, pw []byte) (*ssh.Permissions, error) {
		if string(pw) == "pw" {
			return nil, nil
		}
		return nil, errors.New("denied")
	}}
	for _, k := range hostKeys {
		conf.AddHostKey(k)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				_, chans, reqs, err := ssh.NewServerConn(c, conf)
				if err != nil {
					c.Close()
					return
				}
				go ssh.DiscardRequests(reqs)
				for ch := range chans {
					ch.Reject(ssh.Prohibited, "no")
				}
			}()
		}
	}()
	return ln.Addr().(*net.TCPAddr).Port
}

func signer(t *testing.T, k crypto.Signer) ssh.Signer {
	t.Helper()
	s, err := ssh.NewSignerFromSigner(k)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// A known_hosts holding only the server's ed25519 key must still verify a server that
// also offers ECDSA (which Go would otherwise negotiate first and call a mismatch).
func TestSSHKnownHostsOtherKeyType(t *testing.T) {
	_, edPriv, _ := ed25519.GenerateKey(rand.Reader)
	ecPriv, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	ed, ec := signer(t, edPriv), signer(t, ecPriv)
	port := testServer(t, ec, ed)
	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))

	_, otherPriv, _ := ed25519.GenerateKey(rand.Reader)
	for _, tc := range []struct {
		name   string
		known  ssh.PublicKey
		wantOK bool
	}{
		{"known ed25519 only", ed.PublicKey(), true},
		{"unknown host", nil, true},
		{"changed key", signer(t, otherPriv).PublicKey(), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "known_hosts")
			line := ""
			if tc.known != nil {
				line = knownhosts.Line([]string{addr}, tc.known) + "\n"
			}
			if err := os.WriteFile(path, []byte(line), 0o600); err != nil {
				t.Fatal(err)
			}
			old := knownHostsPath
			knownHostsPath = path
			defer func() { knownHostsPath = old }()

			cfg := model.SSHConfig{Enabled: true, Host: "127.0.0.1", Port: port, User: "u", Password: "pw"}
			s, err := newSSH(context.Background(), cfg, 5*time.Second)
			if tc.wantOK && err != nil {
				t.Fatal(err)
			}
			if !tc.wantOK && (err == nil || !strings.Contains(err.Error(), "mismatch")) {
				t.Fatalf("want a key mismatch, got %v", err)
			}
			if s != nil {
				s.Close()
			}
		})
	}
}
