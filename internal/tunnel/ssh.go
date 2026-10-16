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
	"sync/atomic"
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
	auth, closeAuth, err := authMethods(t.cfg)
	if err != nil {
		return nil, err
	}
	defer closeAuth()
	port := t.cfg.Port
	if port == 0 {
		port = 22
	}
	addr := net.JoinHostPort(t.cfg.Host, fmt.Sprint(port))
	d := net.Dialer{Timeout: t.timeout}
	raw, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("ssh connect %s: %w", addr, err)
	}
	// Asking about an unknown host lifts the handshake deadline: the user may take longer.
	ask := func(hostname string, key ssh.PublicKey) bool {
		if ConfirmHostKey == nil {
			return false
		}
		_ = raw.SetDeadline(time.Time{})
		defer raw.SetDeadline(time.Now().Add(t.timeout))
		return ConfirmHostKey(ctx, HostKeyPrompt{
			ID: fmt.Sprintf("hostkey-%d", promptSeq.Add(1)), Host: hostname, KeyType: key.Type(), Fingerprint: ssh.FingerprintSHA256(key),
		})
	}
	cb, algos := hostKeyCheck(addr, raw.RemoteAddr(), ask)
	conf := &ssh.ClientConfig{
		User:              t.cfg.User,
		Auth:              auth,
		HostKeyCallback:   cb,
		HostKeyAlgorithms: algos,
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

// authMethods returns the methods to offer and a func that releases what they hold (the
// agent socket), to call once the handshake is over.
func authMethods(cfg model.SSHConfig) ([]ssh.AuthMethod, func(), error) {
	none := func() {}
	switch cfg.Auth {
	case model.SSHPublicKey:
		key, err := publicKey(cfg)
		if err != nil {
			return nil, nil, err
		}
		return []ssh.AuthMethod{key}, none, nil
	case model.SSHPasswordPublicKey:
		// Servers configured with "AuthenticationMethods publickey,password" need both.
		key, err := publicKey(cfg)
		if err != nil {
			return nil, nil, err
		}
		return append([]ssh.AuthMethod{key}, passwordAuth(cfg.Password)...), none, nil
	case model.SSHAgent:
		sock := os.Getenv("SSH_AUTH_SOCK")
		if sock == "" {
			return nil, nil, errors.New("SSH agent not available (SSH_AUTH_SOCK is not set)")
		}
		conn, err := net.Dial("unix", sock)
		if err != nil {
			return nil, nil, fmt.Errorf("connect to SSH agent: %w", err)
		}
		return []ssh.AuthMethod{ssh.PublicKeysCallback(agent.NewClient(conn).Signers)}, func() { conn.Close() }, nil
	default:
		return passwordAuth(cfg.Password), none, nil
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

// knownHostsPath is a variable so tests can point it elsewhere.
var knownHostsPath = "~/.ssh/known_hosts"

// PinnedHostsPath is Chloe's own known_hosts, where the key of a host seen for the first
// time is recorded. main sets it under the config directory; empty disables SSH.
var PinnedHostsPath string

var pinMu sync.Mutex

// HostKeyPrompt asks the user whether to trust a host seen for the first time.
type HostKeyPrompt struct {
	ID          string `json:"id"`
	Host        string `json:"host"` // host:port as dialled
	KeyType     string `json:"keyType"`
	Fingerprint string `json:"fingerprint"` // SHA256:…
}

// ConfirmHostKey asks the user about an unknown host and reports whether they trust it.
// main connects it to a dialog; nil refuses unknown hosts.
var ConfirmHostKey func(ctx context.Context, p HostKeyPrompt) bool

var promptSeq atomic.Int64

// hostKeyCheck verifies the server's key against ~/.ssh/known_hosts and Chloe's pinned
// hosts. For a host in neither, ask shows the user its fingerprint; a trusted key is
// pinned, and from then on a different key is rejected.
//
// When the host is known it also returns the host key algorithms to ask for, limited to
// the key types on record. Otherwise the server may present a key type we have no entry
// for (Go prefers ECDSA, ssh-keyscan often records only ed25519), which knownhosts
// reports as a mismatch. OpenSSH orders algorithms the same way.
func hostKeyCheck(addr string, remote net.Addr, ask func(hostname string, key ssh.PublicKey) bool) (ssh.HostKeyCallback, []string) {
	if PinnedHostsPath == "" {
		return func(string, net.Addr, ssh.PublicKey) error {
			return errors.New("ssh: no file to record host keys in")
		}, nil
	}
	var files []string
	for _, f := range []string{expandHome(knownHostsPath), PinnedHostsPath} {
		if _, err := os.Stat(f); err == nil {
			files = append(files, f)
		}
	}
	var cb ssh.HostKeyCallback
	if len(files) > 0 {
		var err error
		if cb, err = knownhosts.New(files...); err != nil {
			return func(string, net.Addr, ssh.PublicKey) error {
				return fmt.Errorf("ssh: read known hosts: %w", err)
			}, nil
		}
	}
	trust := func(hostname string, key ssh.PublicKey) error {
		if !ask(hostname, key) {
			return fmt.Errorf("ssh: the host key of %s (%s) wasn't trusted", hostname, ssh.FingerprintSHA256(key))
		}
		return pin(hostname, key)
	}
	check := func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		if cb == nil {
			return trust(hostname, key)
		}
		err := cb(hostname, remote, key)
		var ke *knownhosts.KeyError
		if !errors.As(err, &ke) {
			return err
		}
		if len(ke.Want) == 0 {
			return trust(hostname, key)
		}
		w := ke.Want[0]
		return fmt.Errorf("ssh: host key mismatch for %s: the server sent %s, but %s:%d records another key. "+
			"If the server's key was changed on purpose, delete that line and connect again", hostname, ssh.FingerprintSHA256(key), w.Filename, w.Line)
	}
	if cb == nil {
		return check, nil
	}
	return check, knownAlgorithms(cb, addr, remote)
}

// pin records a first-seen host key in PinnedHostsPath.
func pin(hostname string, key ssh.PublicKey) error {
	pinMu.Lock()
	defer pinMu.Unlock()
	if err := os.MkdirAll(filepath.Dir(PinnedHostsPath), 0o700); err != nil {
		return fmt.Errorf("ssh: record host key: %w", err)
	}
	f, err := os.OpenFile(PinnedHostsPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("ssh: record host key: %w", err)
	}
	defer f.Close()
	if _, err := f.WriteString(knownhosts.Line([]string{knownhosts.Normalize(hostname)}, key) + "\n"); err != nil {
		return fmt.Errorf("ssh: record host key: %w", err)
	}
	return nil
}

// knownAlgorithms lists the host key algorithms matching the key types known_hosts holds
// for addr, or nil (the client defaults) if it holds none.
func knownAlgorithms(cb ssh.HostKeyCallback, addr string, remote net.Addr) []string {
	var ke *knownhosts.KeyError
	if err := cb(addr, remote, probeKey{}); !errors.As(err, &ke) {
		return nil
	}
	var algos []string
	seen := map[string]bool{}
	for _, k := range ke.Want {
		names := []string{k.Key.Type()}
		if k.Key.Type() == ssh.KeyAlgoRSA {
			names = []string{ssh.KeyAlgoRSASHA512, ssh.KeyAlgoRSASHA256, ssh.KeyAlgoRSA}
		}
		for _, n := range names {
			if !seen[n] {
				seen[n] = true
				algos = append(algos, n)
			}
		}
	}
	return algos
}

// probeKey matches no known_hosts entry, so the callback's KeyError lists every key on
// record for the host.
type probeKey struct{}

func (probeKey) Type() string                        { return "chloe-probe" }
func (probeKey) Marshal() []byte                     { return []byte("chloe-probe") }
func (probeKey) Verify([]byte, *ssh.Signature) error { return errors.New("probe key") }

func expandHome(p string) string {
	if strings.HasPrefix(p, "~/") {
		if h, err := os.UserHomeDir(); err == nil {
			return filepath.Join(h, p[2:])
		}
	}
	return p
}
