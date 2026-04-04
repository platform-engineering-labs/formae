// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package auth

import (
	"encoding/json"
	"io"
	"net"
	"net/rpc"
	"testing"
	"time"

	"ergo.services/ergo/gen"
	pkgauth "github.com/platform-engineering-labs/formae/pkg/auth"
)

// --- Test helpers ---

// mockAuthPlugin implements pkgauth.AuthPlugin for testing.
type mockAuthPlugin struct {
	validResponse bool
}

func (m *mockAuthPlugin) Init(req *pkgauth.InitRequest, resp *pkgauth.InitResponse) error {
	return nil
}

func (m *mockAuthPlugin) Validate(req *pkgauth.ValidateRequest, resp *pkgauth.ValidateResponse) error {
	resp.Valid = m.validResponse
	resp.CacheKey = "test-key"
	resp.CacheTTL = 60 * time.Second
	return nil
}

func (m *mockAuthPlugin) GetAuthHeader(req *pkgauth.GetAuthHeaderRequest, resp *pkgauth.GetAuthHeaderResponse) error {
	return nil
}

// pipeRPC creates an RPC client connected to a mock auth plugin via net.Pipe.
func pipeRPC(t *testing.T, plugin pkgauth.AuthPlugin) *rpc.Client {
	t.Helper()
	clientConn, serverConn := net.Pipe()
	srv := rpc.NewServer()
	_ = srv.RegisterName("AuthPlugin", plugin)
	go srv.ServeConn(serverConn)
	client := rpc.NewClient(clientConn)
	t.Cleanup(func() { _ = client.Close() })
	return client
}

// dummyConn returns a MetaPortConn suitable for tests that don't exercise Feed.
func dummyConn() *MetaPortConn {
	return NewMetaPortConn(gen.Alias{}, func(gen.Alias, any) error { return nil })
}

// --- AuthPluginHandle tests ---

func TestAuthPluginHandle_NewHandle(t *testing.T) {
	cfg := json.RawMessage(`{"key":"value"}`)
	h := NewAuthPluginHandle("basic", "/usr/local/bin/auth-basic", cfg)

	if h.Name() != "basic" {
		t.Fatalf("expected name 'basic', got %q", h.Name())
	}
	if h.BinaryPath() != "/usr/local/bin/auth-basic" {
		t.Fatalf("expected binaryPath, got %q", h.BinaryPath())
	}
	if string(h.ConfigJSON()) != `{"key":"value"}` {
		t.Fatalf("expected configJSON, got %q", h.ConfigJSON())
	}
}

func TestAuthPluginHandle_NotConnectedInitially(t *testing.T) {
	h := NewAuthPluginHandle("basic", "/path", nil)

	_, err := h.Validate(&pkgauth.ValidateRequest{})
	if err == nil {
		t.Fatal("expected error when not connected")
	}
}

func TestAuthPluginHandle_ValidateAfterConnect(t *testing.T) {
	mock := &mockAuthPlugin{validResponse: true}
	client := pipeRPC(t, mock)

	h := NewAuthPluginHandle("basic", "/path", nil)
	h.Connect(dummyConn(), client)

	resp, err := h.Validate(&pkgauth.ValidateRequest{
		Headers: map[string][]string{"Authorization": {"Bearer token"}},
	})
	if err != nil {
		t.Fatalf("Validate failed: %v", err)
	}
	if !resp.Valid {
		t.Fatal("expected valid=true")
	}
	if resp.CacheKey != "test-key" {
		t.Fatalf("expected CacheKey 'test-key', got %q", resp.CacheKey)
	}
}

func TestAuthPluginHandle_Reconnect(t *testing.T) {
	mock1 := &mockAuthPlugin{validResponse: true}
	mock2 := &mockAuthPlugin{validResponse: false}

	client1 := pipeRPC(t, mock1)
	client2 := pipeRPC(t, mock2)

	h := NewAuthPluginHandle("basic", "/path", nil)

	// First connection: valid=true
	h.Connect(dummyConn(), client1)
	resp1, err := h.Validate(&pkgauth.ValidateRequest{})
	if err != nil {
		t.Fatalf("Validate with client1 failed: %v", err)
	}
	if !resp1.Valid {
		t.Fatal("expected valid=true from client1")
	}

	// Reconnect with second client: valid=false
	h.Connect(dummyConn(), client2)
	resp2, err := h.Validate(&pkgauth.ValidateRequest{})
	if err != nil {
		t.Fatalf("Validate with client2 failed: %v", err)
	}
	if resp2.Valid {
		t.Fatal("expected valid=false from client2")
	}
}

func TestAuthPluginHandle_Feed(t *testing.T) {
	h := NewAuthPluginHandle("basic", "/path", nil)

	sender := &capturingSender{}
	conn := NewMetaPortConn(gen.Alias{}, sender.send)
	h.Connect(conn, pipeRPC(t, &mockAuthPlugin{}))

	h.Feed([]byte("hello"))

	buf := make([]byte, 10)
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatalf("Read failed: %v", err)
	}
	if string(buf[:n]) != "hello" {
		t.Fatalf("expected 'hello', got %q", string(buf[:n]))
	}
}

func TestAuthPluginHandle_FeedWithNilConn(t *testing.T) {
	h := NewAuthPluginHandle("basic", "/path", nil)
	// Should not panic
	h.Feed([]byte("hello"))
}

func TestAuthPluginHandle_Close(t *testing.T) {
	h := NewAuthPluginHandle("basic", "/path", nil)

	sender := &capturingSender{}
	conn := NewMetaPortConn(gen.Alias{}, sender.send)
	h.Connect(conn, pipeRPC(t, &mockAuthPlugin{validResponse: true}))

	h.Close()

	// Validate should fail with "not connected"
	_, err := h.Validate(&pkgauth.ValidateRequest{})
	if err == nil {
		t.Fatal("expected error after Close")
	}

	// Conn should be closed (Read returns EOF)
	buf := make([]byte, 10)
	_, err = conn.Read(buf)
	if err != io.EOF {
		t.Fatalf("expected io.EOF after Close, got %v", err)
	}
}

func TestAuthPluginHandle_RestartReconnects(t *testing.T) {
	// Simulate restart: old client becomes unusable, Close + Connect with new client
	mock1 := &mockAuthPlugin{validResponse: true}
	mock2 := &mockAuthPlugin{validResponse: true}

	// Create first client manually so we can close its connection
	clientConn1, serverConn1 := net.Pipe()
	srv1 := rpc.NewServer()
	_ = srv1.RegisterName("AuthPlugin", mock1)
	go srv1.ServeConn(serverConn1)
	client1 := rpc.NewClient(clientConn1)

	h := NewAuthPluginHandle("basic", "/path", nil)
	h.Connect(dummyConn(), client1)

	// Verify first client works
	resp, err := h.Validate(&pkgauth.ValidateRequest{})
	if err != nil {
		t.Fatalf("first Validate failed: %v", err)
	}
	if !resp.Valid {
		t.Fatal("expected valid=true")
	}

	// Close connection (simulating process crash)
	_ = clientConn1.Close()

	// First client should now fail
	_, err = h.Validate(&pkgauth.ValidateRequest{})
	if err == nil {
		t.Fatal("expected error from closed client")
	}

	// Reconnect with new client (simulating supervisor restart)
	client2 := pipeRPC(t, mock2)
	h.Connect(dummyConn(), client2)

	// New client should work
	resp, err = h.Validate(&pkgauth.ValidateRequest{})
	if err != nil {
		t.Fatalf("Validate after reconnect failed: %v", err)
	}
	if !resp.Valid {
		t.Fatal("expected valid=true after reconnect")
	}
}

// --- Tag helper tests ---

func TestAuthTag(t *testing.T) {
	tag := AuthTag("basic")
	if tag != "auth:basic" {
		t.Fatalf("expected 'auth:basic', got %q", tag)
	}
}

func TestIsAuthTag(t *testing.T) {
	if !IsAuthTag("auth:basic") {
		t.Fatal("expected IsAuthTag('auth:basic') to be true")
	}
	if IsAuthTag("AWS") {
		t.Fatal("expected IsAuthTag('AWS') to be false")
	}
	if IsAuthTag("") {
		t.Fatal("expected IsAuthTag('') to be false")
	}
}

func TestAuthTagName(t *testing.T) {
	name := AuthTagName("auth:basic")
	if name != "basic" {
		t.Fatalf("expected 'basic', got %q", name)
	}
}
