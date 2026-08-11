// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package auth

import (
	"encoding/json"
	"fmt"
	"io"
	"net/rpc"
	"os"
	"os/exec"
	"time"
)

const initTimeout = 30 * time.Second

// Client manages an auth plugin subprocess and provides typed RPC methods.
// Used by the CLI to communicate with the auth plugin binary.
type Client struct {
	rpcClient *rpc.Client
	conn      io.ReadWriteCloser
	cmd       *exec.Cmd // nil when created via pipe (testing)
}

// NewClient spawns the auth plugin binary and establishes an RPC connection.
// It calls Init with the provided config before returning.
func NewClient(binaryPath string, config json.RawMessage) (*Client, error) {
	cmd := exec.Command(binaryPath)
	cmd.Stderr = os.Stderr // connect plugin stderr to the host's stderr
	setSysProcAttr(cmd)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("auth client: stdin pipe: %w", err)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("auth client: stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("auth client: start: %w", err)
	}

	// Consume the ready signal the plugin writes before starting its RPC
	// server. Without this the byte would be interpreted as gob data and
	// corrupt the RPC stream.
	var ready [1]byte
	if _, err := io.ReadFull(stdout, ready[:]); err != nil {
		cmd.Process.Kill()
		cmd.Wait()
		return nil, fmt.Errorf("auth client: waiting for ready signal: %w", err)
	}

	conn := &stdioConn{
		Reader:      stdout,
		WriteCloser: stdin,
	}

	rpcClient := rpc.NewClient(conn)

	c := &Client{
		rpcClient: rpcClient,
		conn:      conn,
		cmd:       cmd,
	}

	// Call Init to configure the plugin with a timeout to prevent hanging
	// if the plugin is unresponsive.
	type initResult struct {
		resp InitResponse
		err  error
	}
	ch := make(chan initResult, 1)
	go func() {
		var r initResult
		r.err = rpcClient.Call("AuthPlugin.Init", &InitRequest{Config: config}, &r.resp)
		ch <- r
	}()

	var resp InitResponse
	select {
	case r := <-ch:
		if r.err != nil {
			c.Close()
			return nil, fmt.Errorf("auth client: init call: %w", r.err)
		}
		resp = r.resp
	case <-time.After(initTimeout):
		c.Close()
		return nil, fmt.Errorf("auth client: init timed out after %s", initTimeout)
	}
	if resp.Error != "" {
		c.Close()
		return nil, fmt.Errorf("auth client: init: %s", resp.Error)
	}

	return c, nil
}

// Validate sends a validation request to the auth plugin.
func (c *Client) Validate(req *ValidateRequest) (*ValidateResponse, error) {
	var resp ValidateResponse
	if err := c.rpcClient.Call("AuthPlugin.Validate", req, &resp); err != nil {
		return nil, fmt.Errorf("auth client: validate: %w", err)
	}
	return &resp, nil
}

// GetAuthHeader requests auth headers from the plugin for outgoing requests.
// forceRefresh asks the plugin to skip freshness checks and refresh the
// credential now.
func (c *Client) GetAuthHeader(forceRefresh bool) (*GetAuthHeaderResponse, error) {
	var resp GetAuthHeaderResponse
	if err := c.call("GetAuthHeader", &GetAuthHeaderRequest{ForceRefresh: forceRefresh}, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// LoginStart begins an interactive login flow on the plugin.
func (c *Client) LoginStart(req *LoginStartRequest) (*LoginStartResponse, error) {
	var resp LoginStartResponse
	if err := c.call("LoginStart", req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// LoginWait polls the plugin for completion of a login flow started by
// LoginStart. It carries no client-side timeout: the plugin owns the bound
// on how long the flow may take.
func (c *Client) LoginWait(req *LoginWaitRequest) (*LoginWaitResponse, error) {
	var resp LoginWaitResponse
	if err := c.call("LoginWait", req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// Logout ends the current session on the plugin.
func (c *Client) Logout() (*LogoutResponse, error) {
	var resp LogoutResponse
	if err := c.call("Logout", &LogoutRequest{}, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// errorCoded is implemented by response types that carry an ErrorCode field,
// letting call set it uniformly when a verb turns out to be unsupported.
type errorCoded interface {
	setUnsupported()
}

func (r *GetAuthHeaderResponse) setUnsupported() { r.ErrorCode = ErrorCodeUnsupported }
func (r *LoginStartResponse) setUnsupported()    { r.ErrorCode = ErrorCodeUnsupported }
func (r *LoginWaitResponse) setUnsupported()     { r.ErrorCode = ErrorCodeUnsupported }
func (r *LogoutResponse) setUnsupported()        { r.ErrorCode = ErrorCodeUnsupported }

// call invokes an AuthPlugin RPC method and translates net/rpc's
// method-not-found transport error into the typed unsupported contract, so
// an old already-built plugin binary that lacks a verb is indistinguishable
// from a new one that declines it.
//
// The match is against the exact canonical error text net/rpc's server
// produces for the specific method being called ("rpc: can't find method " +
// serviceMethod), not a bare prefix. net/rpc surfaces both its own dispatch
// failures and errors returned by an invoked method through the same
// unstructured error channel, so a registered method that fails with an
// error string that happens to start with that same prefix (e.g. a plugin
// propagating a downstream RPC failure verbatim) must NOT be mistaken for an
// absent method.
func (c *Client) call(method string, req any, resp errorCoded) error {
	serviceMethod := "AuthPlugin." + method
	err := c.rpcClient.Call(serviceMethod, req, resp)
	if err == nil {
		return nil
	}
	if err.Error() == "rpc: can't find method "+serviceMethod {
		resp.setUnsupported()
		return nil
	}
	return fmt.Errorf("auth client: %s: %w", method, err)
}

// Close shuts down the RPC client, closes the connection, and kills the subprocess.
func (c *Client) Close() error {
	var firstErr error
	if c.rpcClient != nil {
		if err := c.rpcClient.Close(); err != nil {
			firstErr = err
		}
	}
	if c.cmd != nil && c.cmd.Process != nil {
		_ = c.cmd.Process.Kill()
		_ = c.cmd.Wait()
	}
	return firstErr
}
