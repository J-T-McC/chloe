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
	cfg     model.SSHConfig
	timeout time.Duration
	mu      sync.Mutex
	client  *ssh.Client
	stopKA  chan struct{}
}

// sshTimeout bounds the TCP connect plus the SSH handshake and authentication.
const sshTimeout = 15 * time.Second

// NewSSH validates the configuration and opens the SSH connection.
func NewSSH(ctx context.Context, cfg model.SSHConfig) (*SSH, error) {
	return newSSH(ctx, cfg, sshTimeout)
}

func newSSH(ctx context.Context, cfg model.SSHConfig, timeout time.Duration) (*SSH, error) {
	t := &SSH{cfg: cfg, timeout: timeout}
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
	}
	d := net.Dialer{Timeout: t.timeout}
	raw, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("ssh connect %s: %w", addr, err)
	}
	// ssh.NewClientConn has no timeout of its own (ClientConfig.Timeout only applies to
	// ssh.Dial), so a host that accepts TCP but never answers would block forever.
	// Bound the handshake with a deadline and abort it if ctx is cancelled.
	_ = raw.SetDeadline(time.Now().Add(t.timeout))
	stop := context.AfterFunc(ctx, func() { _ = raw.SetDeadline(time.Unix(1, 0)) })
	sc, chans, reqs, err := ssh.NewClientConn(raw, addr, conf)
	stop()
	if err != nil {
		raw.Close()
		if ctx.Err() != nil {
			return nil, fmt.Errorf("ssh handshake %s: %w", addr, ctx.Err())
		}
		var ne net.Error
		if errors.As(err, &ne) && ne.Timeout() {
			return nil, fmt.Errorf("ssh handshake %s: no response from the SSH server after %v (is this the SSH port?)", addr, t.timeout)
		}
		return nil, fmt.Errorf("ssh handshake %s: %w", addr, err)
	}
	_ = raw.SetDeadline(time.Time{})
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
		key, err := publicKey(cfg)
		if err != nil {
			return nil, err
		}
		return []ssh.AuthMethod{key}, nil
	case model.SSHPasswordPublicKey:
		// Servers configured with "AuthenticationMethods publickey,password" need both.
		key, err := publicKey(cfg)
		if err != nil {
			return nil, err
		}
		return append([]ssh.AuthMethod{key}, passwordAuth(cfg.Password)...), nil
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
		return passwordAuth(cfg.Password), nil
	}
}

func publicKey(cfg model.SSHConfig) (ssh.AuthMethod, error) {
	key, err := os.ReadFile(expandHome(cfg.PrivateKeyPath))
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
	return ssh.PublicKeys(signer), nil
}

// passwordAuth answers both plain password and keyboard-interactive prompts.
func passwordAuth(pw string) []ssh.AuthMethod {
	return []ssh.AuthMethod{
		ssh.Password(pw),
		ssh.KeyboardInteractive(func(_, _ string, qs []string, _ []bool) ([]string, error) {
			ans := make([]string, len(qs))
			for i := range ans {
				ans[i] = pw
			}
			return ans, nil
		}),
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
