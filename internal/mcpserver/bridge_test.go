package mcpserver_test

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"chloe/internal/mcpbridge"
	"chloe/internal/store"
)

// bridged connects an MCP client to the fixture's endpoint through `chloe mcp`.
func (f *fixture) bridged(t *testing.T, token string) (*mcp.ClientSession, chan error) {
	t.Helper()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	done := make(chan error, 1)
	go func() {
		err := mcpbridge.Run(f.srv.Status().Socket, token, inR, outW)
		outW.CloseWithError(io.EOF)
		done <- err
	}()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "bridge-test"}, nil).Connect(context.Background(), &mcp.IOTransport{Reader: outR, Writer: inW}, nil)
	if err != nil {
		select {
		case e := <-done:
			t.Fatalf("connect: %v (bridge: %v)", err, e)
		case <-time.After(2 * time.Second):
			t.Fatalf("connect: %v", err)
		}
	}
	t.Cleanup(func() { cs.Close() })
	return cs, done
}

func TestBridge(t *testing.T) {
	f := setup(t, store.MCPConnAccess{Level: store.MCPWrite})
	st := f.srv.Settings()
	st.Enabled, st.NoHTTP = true, true // the socket only: no TCP port in tests
	if err := f.srv.Save(st); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(f.srv.Close)
	status := f.srv.Status()
	if !status.Running || status.HTTP || status.Socket == "" || status.Bridge == "" {
		t.Fatalf("status: %+v", status)
	}
	if info, err := os.Stat(status.Socket); err != nil || info.Mode().Perm() != 0o600 || filepath.Dir(status.Socket) != f.m.Store.Dir() {
		t.Fatalf("socket file: %v %v", info, err)
	}

	if err := mcpbridge.Run(status.Socket, "chloe_wrong", strings.NewReader(""), io.Discard); err == nil || !strings.Contains(err.Error(), "unknown or disabled") {
		t.Fatalf("wrong token: %v", err)
	}
	if err := mcpbridge.Run(status.Socket, "", strings.NewReader(""), io.Discard); err == nil || !strings.Contains(err.Error(), mcpbridge.TokenEnv) {
		t.Fatalf("no token: %v", err)
	}

	cs, done := f.bridged(t, f.ep.Token)
	ctx := context.Background()
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "select_rows", Arguments: map[string]any{"connection": "shop", "table": "customers", "where": where(1)}})
	if err != nil || res.IsError {
		t.Fatalf("select over the bridge: %v %+v", err, res)
	}
	// Writes still wait for approval.
	asked := len(f.appr.asked)
	res, err = cs.CallTool(ctx, &mcp.CallToolParams{Name: "update_rows", Arguments: map[string]any{"connection": "shop", "table": "customers", "where": where(1), "set": map[string]any{"name": "Bridged"}}})
	if err != nil || res.IsError || len(f.appr.asked) != asked+1 {
		t.Fatalf("update over the bridge: %v %+v", err, res)
	}
	if acts := f.srv.Activity(); acts[0].Tool != "update_rows" || acts[0].Outcome != "approved" {
		t.Fatalf("activity: %+v", acts[0])
	}

	// Disabling the endpoint ends its bridged session.
	st = f.srv.Settings()
	st.Endpoints[0].Enabled = false
	if err := f.srv.Save(st); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("the bridge kept running after its endpoint was disabled")
	}

	// Switching the server off removes the socket.
	st.Enabled = false
	if err := f.srv.Save(st); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(status.Socket); !os.IsNotExist(err) {
		t.Fatal("socket left behind")
	}
	if err := mcpbridge.Run(status.Socket, f.ep.Token, strings.NewReader(""), io.Discard); err == nil || !strings.Contains(err.Error(), "switch on") {
		t.Fatalf("server off: %v", err)
	}
}
