// Package tunnel provides network dialers for reaching databases, directly or through SSH.
package tunnel

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
	"golang.org/x/crypto/ssh/knownhosts"

	"chloe/internal/model"
)

// Dialer is a closable source of connections to the database host.
type Dialer interface {
	DialContext(ctx context.Context, network, addr string) (net.Conn, error)
	Close() error
}

// Direct dials without a tunnel.
type Direct struct{ d net.Dialer }

func NewDirect() *Direct {
	return &Direct{d: net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}}
}

func (d *Direct) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	return d.d.DialContext(ctx, network, addr)
}
func (d *Direct) Close() error { return nil }

// SSH dials through an SSH jump host. One SSH client is shared by all of a
// connection's database sessions; it reconnects lazily if the link drops.
type SSH struct {
	cfg    model.SSHConfig
	mu     sync.Mutex
	client *ssh.Client
	stopKA chan struct{}
}

// NewSSH validates the configuration and opens the SSH connection.
func NewSSH(ctx context.Context, cfg model.SSHConfig) (*SSH, error) {
	t := &SSH{cfg: cfg}
	if _, err := t.getClient(ctx); err != nil {
		return nil, err
	}
	return t, nil
}

func (t *SSH) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	c, err := t.getClient(ctx)
	if err != nil {
		return nil, err
	}
	conn, err := c.DialContext(ctx, network, addr)
	if err != nil {
		// The link may have died; drop it and retry once with a fresh client.
		t.reset(c)
		if c, err2 := t.getClient(ctx); err2 == nil {
			return c.DialContext(ctx, network, addr)
		}
		return nil, fmt.Errorf("ssh tunnel to %s: %w", addr, err)
	}
	return conn, nil
}

func (t *SSH) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.stopKA != nil {
		close(t.stopKA)
		t.stopKA = nil
	}
	if t.client != nil {
		err := t.client.Close()
		t.client = nil
		return err
	}
	return nil
}

func (t *SSH) reset(c *ssh.Client) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.client == c {
		_ = c.Close()
		t.client = nil
		if t.stopKA != nil {
			close(t.stopKA)
			t.stopKA = nil
		}
	}
}

func (t *SSH) getClient(ctx context.Context) (*ssh.Client, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.client != nil {
		return t.client, nil
	}
	auth, err := authMethods(t.cfg)
	if err != nil {
		return nil, err
	}
	port := t.cfg.Port
	if port == 0 {
		port = 22
	}
	addr := net.JoinHostPort(t.cfg.Host, fmt.Sprint(port))
	conf := &ssh.ClientConfig{
		User:            t.cfg.User,
		Auth:            auth,
		HostKeyCallback: hostKeyCallback(),
		Timeout:         15 * time.Second,
	}
	var d net.Dialer
	raw, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("ssh connect %s: %w", addr, err)
	}
	sc, chans, reqs, err := ssh.NewClientConn(raw, addr, conf)
	if err != nil {
		raw.Close()
		return nil, fmt.Errorf("ssh handshake %s: %w", addr, err)
	}
	t.client = ssh.NewClient(sc, chans, reqs)
	t.stopKA = make(chan struct{})
	go keepAlive(t.client, t.stopKA)
	return t.client, nil
}

func keepAlive(c *ssh.Client, stop chan struct{}) {
	tk := time.NewTicker(30 * time.Second)
	defer tk.Stop()
	for {
		select {
		case <-stop:
			return
		case <-tk.C:
			if _, _, err := c.SendRequest("keepalive@openssh.com", true, nil); err != nil {
				return
			}
		}
	}
}

func authMethods(cfg model.SSHConfig) ([]ssh.AuthMethod, error) {
	switch cfg.Auth {
	case model.SSHPublicKey:
		path := expandHome(cfg.PrivateKeyPath)
		key, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read private key: %w", err)
		}
		var signer ssh.Signer
		if cfg.Passphrase != "" {
			signer, err = ssh.ParsePrivateKeyWithPassphrase(key, []byte(cfg.Passphrase))
		} else {
			signer, err = ssh.ParsePrivateKey(key)
		}
		if err != nil {
			var missing *ssh.PassphraseMissingError
			if errors.As(err, &missing) {
				return nil, errors.New("private key is encrypted: passphrase required")
			}
			return nil, fmt.Errorf("parse private key: %w", err)
		}
		return []ssh.AuthMethod{ssh.PublicKeys(signer)}, nil
	case model.SSHAgent:
		sock := os.Getenv("SSH_AUTH_SOCK")
		if sock == "" {
			return nil, errors.New("SSH agent not available (SSH_AUTH_SOCK is not set)")
		}
		conn, err := net.Dial("unix", sock)
		if err != nil {
			return nil, fmt.Errorf("connect to SSH agent: %w", err)
		}
		return []ssh.AuthMethod{ssh.PublicKeysCallback(agent.NewClient(conn).Signers)}, nil
	default:
		pw := cfg.Password
		return []ssh.AuthMethod{
			ssh.Password(pw),
			ssh.KeyboardInteractive(func(_, _ string, qs []string, _ []bool) ([]string, error) {
				ans := make([]string, len(qs))
				for i := range ans {
					ans[i] = pw
				}
				return ans, nil
			}),
		}, nil
	}
}

// hostKeyCallback verifies against ~/.ssh/known_hosts when present. Unknown hosts are
// accepted (trust on first use, like Navicat); mismatched keys are rejected.
func hostKeyCallback() ssh.HostKeyCallback {
	path := expandHome("~/.ssh/known_hosts")
	cb, err := knownhosts.New(path)
	if err != nil {
		return ssh.InsecureIgnoreHostKey()
	}
	return func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		err := cb(hostname, remote, key)
		var ke *knownhosts.KeyError
		if errors.As(err, &ke) && len(ke.Want) == 0 {
			return nil // host not in known_hosts
		}
		return err
	}
}

func expandHome(p string) string {
	if strings.HasPrefix(p, "~/") {
		if h, err := os.UserHomeDir(); err == nil {
			return filepath.Join(h, p[2:])
		}
	}
	return p
}
