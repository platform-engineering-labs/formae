// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package auth

import (
	"encoding/json"
	"io"
	"net/rpc"
	"testing"
	"time"
)

// fakePlugin is a test double implementing the AuthPlugin interface.
type fakePlugin struct {
	initCalled       bool
	receivedCfg      json.RawMessage
	validateUsers    map[string]string // username -> password (plaintext for testing)
	lastForceRefresh bool
}

func (f *fakePlugin) Init(req *InitRequest, resp *InitResponse) error {
	f.initCalled = true
	f.receivedCfg = req.Config
	return nil
}

func (f *fakePlugin) Validate(req *ValidateRequest, resp *ValidateResponse) error {
	// Simple validation: check for "X-Api-Key" header
	keys := req.Headers["X-Api-Key"]
	if len(keys) > 0 && keys[0] == "secret-key" {
		resp.Valid = true
		resp.CacheKey = "key:secret-key"
		resp.CacheTTL = 60 * time.Second
	} else {
		resp.Valid = false
		resp.Error = "invalid api key"
	}
	return nil
}

func (f *fakePlugin) GetAuthHeader(req *GetAuthHeaderRequest, resp *GetAuthHeaderResponse) error {
	f.lastForceRefresh = req.ForceRefresh
	resp.Headers = map[string][]string{
		"X-Api-Key": {"secret-key"},
	}
	return nil
}

func (f *fakePlugin) LoginStart(req *LoginStartRequest, resp *LoginStartResponse) error {
	resp.Status = "started"
	resp.SessionID = "s-1"
	return nil
}

func (f *fakePlugin) LoginWait(req *LoginWaitRequest, resp *LoginWaitResponse) error {
	resp.Subject = "11111111-1111-4111-8111-111111111111"
	resp.SubjectName = "dpanders"
	return nil
}

func (f *fakePlugin) Logout(req *LogoutRequest, resp *LogoutResponse) error {
	return nil
}

// pipeConn creates a pair of io.ReadWriteCloser connected via io.Pipe,
// suitable for testing net/rpc client↔server communication.
func pipeConn() (clientConn, serverConn io.ReadWriteCloser) {
	// client writes to serverReader, server writes to clientReader
	clientReader, serverWriter := io.Pipe()
	serverReader, clientWriter := io.Pipe()

	clientConn = &stdioConn{Reader: clientReader, WriteCloser: clientWriter}
	serverConn = &stdioConn{Reader: serverReader, WriteCloser: serverWriter}
	return
}

func TestServerAndClient_Init(t *testing.T) {
	plugin := &fakePlugin{}
	clientConn, serverConn := pipeConn()

	// Start server in background
	go Serve(plugin, serverConn)

	// Create rpc client
	client := rpc.NewClient(clientConn)
	defer client.Close()

	cfg := json.RawMessage(`{"users":[{"username":"admin"}]}`)
	var resp InitResponse
	err := client.Call("AuthPlugin.Init", &InitRequest{Config: cfg}, &resp)
	if err != nil {
		t.Fatalf("Init call failed: %v", err)
	}

	if resp.Error != "" {
		t.Fatalf("Init returned error: %s", resp.Error)
	}

	if !plugin.initCalled {
		t.Fatal("expected Init to be called on plugin")
	}

	if string(plugin.receivedCfg) != string(cfg) {
		t.Fatalf("expected config %s, got %s", cfg, plugin.receivedCfg)
	}
}

func TestServerAndClient_Validate(t *testing.T) {
	plugin := &fakePlugin{}
	clientConn, serverConn := pipeConn()

	go Serve(plugin, serverConn)

	client := rpc.NewClient(clientConn)
	defer client.Close()

	t.Run("valid request", func(t *testing.T) {
		var resp ValidateResponse
		err := client.Call("AuthPlugin.Validate", &ValidateRequest{
			Headers: map[string][]string{"X-Api-Key": {"secret-key"}},
		}, &resp)
		if err != nil {
			t.Fatalf("Validate call failed: %v", err)
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

	t.Run("invalid request", func(t *testing.T) {
		var resp ValidateResponse
		err := client.Call("AuthPlugin.Validate", &ValidateRequest{
			Headers: map[string][]string{"X-Api-Key": {"wrong-key"}},
		}, &resp)
		if err != nil {
			t.Fatalf("Validate call failed: %v", err)
		}
		if resp.Valid {
			t.Fatal("expected Valid=false")
		}
		if resp.Error != "invalid api key" {
			t.Fatalf("expected error 'invalid api key', got %q", resp.Error)
		}
	})
}

func TestServerAndClient_GetAuthHeader(t *testing.T) {
	plugin := &fakePlugin{}
	clientConn, serverConn := pipeConn()

	go Serve(plugin, serverConn)

	client := rpc.NewClient(clientConn)
	defer client.Close()

	var resp GetAuthHeaderResponse
	err := client.Call("AuthPlugin.GetAuthHeader", &GetAuthHeaderRequest{ForceRefresh: true}, &resp)
	if err != nil {
		t.Fatalf("GetAuthHeader call failed: %v", err)
	}

	keys := resp.Headers["X-Api-Key"]
	if len(keys) != 1 || keys[0] != "secret-key" {
		t.Fatalf("expected X-Api-Key header with 'secret-key', got %v", resp.Headers)
	}

	if !plugin.lastForceRefresh {
		t.Fatal("expected ForceRefresh to round-trip to the plugin")
	}
}

func TestServerAndClient_LoginStart(t *testing.T) {
	plugin := &fakePlugin{}
	clientConn, serverConn := pipeConn()

	go Serve(plugin, serverConn)

	client := rpc.NewClient(clientConn)
	defer client.Close()

	var resp LoginStartResponse
	err := client.Call("AuthPlugin.LoginStart", &LoginStartRequest{Mode: "browser"}, &resp)
	if err != nil {
		t.Fatalf("LoginStart call failed: %v", err)
	}
	if resp.Status != "started" {
		t.Fatalf("expected Status 'started', got %q", resp.Status)
	}
	if resp.SessionID != "s-1" {
		t.Fatalf("expected SessionID 's-1', got %q", resp.SessionID)
	}
}

func TestServerAndClient_LoginWait(t *testing.T) {
	plugin := &fakePlugin{}
	clientConn, serverConn := pipeConn()

	go Serve(plugin, serverConn)

	client := rpc.NewClient(clientConn)
	defer client.Close()

	var resp LoginWaitResponse
	err := client.Call("AuthPlugin.LoginWait", &LoginWaitRequest{SessionID: "s-1"}, &resp)
	if err != nil {
		t.Fatalf("LoginWait call failed: %v", err)
	}
	if resp.Subject != "11111111-1111-4111-8111-111111111111" {
		t.Fatalf("expected Subject '11111111-1111-4111-8111-111111111111', got %q", resp.Subject)
	}
	if resp.SubjectName != "dpanders" {
		t.Fatalf("expected SubjectName 'dpanders', got %q", resp.SubjectName)
	}
}

func TestServerAndClient_Logout(t *testing.T) {
	plugin := &fakePlugin{}
	clientConn, serverConn := pipeConn()

	go Serve(plugin, serverConn)

	client := rpc.NewClient(clientConn)
	defer client.Close()

	var resp LogoutResponse
	err := client.Call("AuthPlugin.Logout", &LogoutRequest{}, &resp)
	if err != nil {
		t.Fatalf("Logout call failed: %v", err)
	}
	if resp.Error != "" {
		t.Fatalf("expected no error, got %q", resp.Error)
	}
	if resp.ErrorCode != "" {
		t.Fatalf("expected no error code, got %q", resp.ErrorCode)
	}
}
