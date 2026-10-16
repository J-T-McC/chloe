package tunnel

import (
	"context"
	"net"
	"testing"
	"time"

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
