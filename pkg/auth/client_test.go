// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package auth

import (
	"net/rpc"
	"testing"
	"time"
)

// newTestClient creates a Client backed by an in-process server via io.Pipe,
// bypassing os/exec. This lets us test Client methods without a real binary.
func newTestClient(t *testing.T, plugin AuthPlugin) *Client {
	t.Helper()

	clientConn, serverConn := pipeConn()

	go Serve(plugin, serverConn)

	rpcClient := rpc.NewClient(clientConn)

	return &Client{
		rpcClient: rpcClient,
		conn:      clientConn,
	}
}

func TestClient_Validate(t *testing.T) {
	plugin := &fakePlugin{}
	client := newTestClient(t, plugin)
	defer client.Close()

	t.Run("valid headers", func(t *testing.T) {
		resp, err := client.Validate(&ValidateRequest{
			Headers: map[string][]string{"X-Api-Key": {"secret-key"}},
		})
		if err != nil {
			t.Fatalf("Validate failed: %v", err)
		}
		if !resp.Valid {
			t.Fatal("expected Valid=true")
		}
		if resp.CacheKey != "key:secret-key" {
			t.Fatalf("expected CacheKey 'key:secret-key', got %q", resp.CacheKey)
		}
		if resp.CacheTTL != 60*time.Second {
			t.Fatalf("expected CacheTTL 60s, got %v", resp.CacheTTL)
		}
	})

	t.Run("invalid headers", func(t *testing.T) {
		resp, err := client.Validate(&ValidateRequest{
			Headers: map[string][]string{"X-Api-Key": {"bad"}},
		})
		if err != nil {
			t.Fatalf("Validate failed: %v", err)
		}
		if resp.Valid {
			t.Fatal("expected Valid=false")
		}
		if resp.Error != "invalid api key" {
			t.Fatalf("expected error 'invalid api key', got %q", resp.Error)
		}
	})
}

func TestClient_GetAuthHeader(t *testing.T) {
	plugin := &fakePlugin{}
	client := newTestClient(t, plugin)
	defer client.Close()

	resp, err := client.GetAuthHeader()
	if err != nil {
		t.Fatalf("GetAuthHeader failed: %v", err)
	}

	keys := resp.Headers["X-Api-Key"]
	if len(keys) != 1 || keys[0] != "secret-key" {
		t.Fatalf("expected X-Api-Key='secret-key', got %v", resp.Headers)
	}
}

func TestClient_Close(t *testing.T) {
	plugin := &fakePlugin{}
	clientConn, serverConn := pipeConn()
	go Serve(plugin, serverConn)

	rpcClient := rpc.NewClient(clientConn)
	client := &Client{
		rpcClient: rpcClient,
		conn:      clientConn,
	}

	err := client.Close()
	if err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	// After close, calls should fail
	_, err = client.Validate(&ValidateRequest{})
	if err == nil {
		t.Fatal("expected error after Close")
	}
}