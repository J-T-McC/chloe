// Package mcpbridge is `chloe mcp`: a stdio MCP server that MCP clients launch,
// relaying to the running Chloe app over the socket in its config folder.
package mcpbridge

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"

	"chloe/internal/mcpserver"
)

// TokenEnv names the variable holding the endpoint token (kept out of the
// command line, where other processes could read it).
const TokenEnv = "CHLOE_MCP_TOKEN"

// Run relays MCP messages between in/out and Chloe until either side closes.
func Run(socket, token string, in io.Reader, out io.Writer) error {
	if token == "" {
		return fmt.Errorf("%s isn't set: copy the endpoint's config from Chloe's MCP Server window", TokenEnv)
	}
	c, err := net.Dial("unix", socket)
	if err != nil {
		return fmt.Errorf("can't reach Chloe at %s: open Chloe and switch on its MCP server", socket)
	}
	defer c.Close()
	hello, _ := json.Marshal(mcpserver.Hello{Token: token})
	if _, err := c.Write(append(hello, '\n')); err != nil {
		return err
	}
	r := bufio.NewReader(c)
	line, err := r.ReadBytes('\n')
	if err != nil {
		return errors.New("Chloe closed the connection")
	}
	var w mcpserver.Welcome
	if json.Unmarshal(line, &w) != nil || !w.OK {
		if w.Error == "" {
			w.Error = "unexpected answer from Chloe"
		}
		return errors.New(w.Error)
	}
	go func() {
		_, _ = io.Copy(c, in)
		// The client is done: let Chloe finish and close its side.
		_ = c.(*net.UnixConn).CloseWrite()
	}()
	_, err = io.Copy(out, r)
	return err
}
