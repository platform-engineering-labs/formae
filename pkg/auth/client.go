// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package auth

import (
	"encoding/json"
	"fmt"
	"io"
	"net/rpc"
	"os/exec"
)

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
	cmd.Stderr = nil // let plugin stderr go to parent stderr

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

	// Call Init to configure the plugin
	var resp InitResponse
	if err := rpcClient.Call("AuthPlugin.Init", &InitRequest{Config: config}, &resp); err != nil {
		c.Close()
		return nil, fmt.Errorf("auth client: init call: %w", err)
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
func (c *Client) GetAuthHeader() (*GetAuthHeaderResponse, error) {
	var resp GetAuthHeaderResponse
	if err := c.rpcClient.Call("AuthPlugin.GetAuthHeader", &GetAuthHeaderRequest{}, &resp); err != nil {
		return nil, fmt.Errorf("auth client: get auth header: %w", err)
	}
	return &resp, nil
}

// Close shuts down the RPC client, closes the connection, and kills the subprocess.
func (c *Client) Close() error {
	if c.rpcClient != nil {
		c.rpcClient.Close()
	}
	if c.cmd != nil && c.cmd.Process != nil {
		c.cmd.Process.Kill()
		c.cmd.Wait()
	}
	return nil
}
