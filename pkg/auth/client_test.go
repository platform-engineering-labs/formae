// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package auth

import (
	"errors"
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

	resp, err := client.GetAuthHeader(false)
	if err != nil {
		t.Fatalf("GetAuthHeader failed: %v", err)
	}

	keys := resp.Headers["X-Api-Key"]
	if len(keys) != 1 || keys[0] != "secret-key" {
		t.Fatalf("expected X-Api-Key='secret-key', got %v", resp.Headers)
	}
}

func TestClient_GetAuthHeader_ForceRefresh(t *testing.T) {
	plugin := &fakePlugin{}
	client := newTestClient(t, plugin)
	defer client.Close()

	if _, err := client.GetAuthHeader(true); err != nil {
		t.Fatalf("GetAuthHeader failed: %v", err)
	}

	if !plugin.lastForceRefresh {
		t.Fatal("expected ForceRefresh=true to transmit to the plugin")
	}
}

func TestClient_LoginStart(t *testing.T) {
	plugin := &fakePlugin{}
	client := newTestClient(t, plugin)
	defer client.Close()

	resp, err := client.LoginStart(&LoginStartRequest{Mode: "browser"})
	if err != nil {
		t.Fatalf("LoginStart failed: %v", err)
	}
	if resp.Status != "started" {
		t.Fatalf("expected Status 'started', got %q", resp.Status)
	}
	if resp.SessionID != "s-1" {
		t.Fatalf("expected SessionID 's-1', got %q", resp.SessionID)
	}
}

func TestClient_LoginWait(t *testing.T) {
	plugin := &fakePlugin{}
	client := newTestClient(t, plugin)
	defer client.Close()

	resp, err := client.LoginWait(&LoginWaitRequest{SessionID: "s-1"})
	if err != nil {
		t.Fatalf("LoginWait failed: %v", err)
	}
	if resp.Subject != "11111111-1111-4111-8111-111111111111" {
		t.Fatalf("expected Subject '11111111-1111-4111-8111-111111111111', got %q", resp.Subject)
	}
	if resp.SubjectName != "dpanders" {
		t.Fatalf("expected SubjectName 'dpanders', got %q", resp.SubjectName)
	}
}

func TestClient_Logout(t *testing.T) {
	plugin := &fakePlugin{}
	client := newTestClient(t, plugin)
	defer client.Close()

	resp, err := client.Logout()
	if err != nil {
		t.Fatalf("Logout failed: %v", err)
	}
	if resp.Error != "" {
		t.Fatalf("expected no error, got %q", resp.Error)
	}
	if resp.ErrorCode != "" {
		t.Fatalf("expected no error code, got %q", resp.ErrorCode)
	}
}

// legacyAuthPlugin implements only the pre-login-verb surface (Init,
// Validate, GetAuthHeader), modelling an already-built plugin binary that
// predates the LoginStart/LoginWait/Logout verbs.
type legacyAuthPlugin struct{}

func (legacyAuthPlugin) Init(req *InitRequest, resp *InitResponse) error {
	return nil
}

func (legacyAuthPlugin) Validate(req *ValidateRequest, resp *ValidateResponse) error {
	resp.Valid = true
	return nil
}

func (legacyAuthPlugin) GetAuthHeader(req *GetAuthHeaderRequest, resp *GetAuthHeaderResponse) error {
	resp.Headers = map[string][]string{"X-Api-Key": {"legacy-key"}}
	return nil
}

// newLegacyTestClient wires a Client to an RPC server that registers only
// the given legacy-shaped plugin, bypassing Serve (which requires the full
// AuthPlugin interface) so the server genuinely lacks the login verbs.
func newLegacyTestClient(t *testing.T, plugin any) *Client {
	t.Helper()

	clientConn, serverConn := pipeConn()

	srv := rpc.NewServer()
	if err := srv.RegisterName("AuthPlugin", plugin); err != nil {
		t.Fatalf("RegisterName: %v", err)
	}
	go srv.ServeConn(serverConn)

	rpcClient := rpc.NewClient(clientConn)

	return &Client{
		rpcClient: rpcClient,
		conn:      clientConn,
	}
}

func TestClient_UnsupportedVerbTranslation(t *testing.T) {
	client := newLegacyTestClient(t, &legacyAuthPlugin{})
	defer client.Close()

	cases := []struct {
		name     string
		call     func() (ErrorCode, error)
		wantCode ErrorCode
	}{
		{
			name: "GetAuthHeader",
			call: func() (ErrorCode, error) {
				resp, err := client.GetAuthHeader(false)
				if err != nil {
					return "", err
				}
				return resp.ErrorCode, nil
			},
			wantCode: "",
		},
		{
			name: "LoginStart",
			call: func() (ErrorCode, error) {
				resp, err := client.LoginStart(&LoginStartRequest{Mode: "browser"})
				if err != nil {
					return "", err
				}
				return resp.ErrorCode, nil
			},
			wantCode: ErrorCodeUnsupported,
		},
		{
			name: "LoginWait",
			call: func() (ErrorCode, error) {
				resp, err := client.LoginWait(&LoginWaitRequest{SessionID: "s-1"})
				if err != nil {
					return "", err
				}
				return resp.ErrorCode, nil
			},
			wantCode: ErrorCodeUnsupported,
		},
		{
			name: "Logout",
			call: func() (ErrorCode, error) {
				resp, err := client.Logout()
				if err != nil {
					return "", err
				}
				return resp.ErrorCode, nil
			},
			wantCode: ErrorCodeUnsupported,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, err := tc.call()
			if err != nil {
				t.Fatalf("expected nil error, got %v", err)
			}
			if code != tc.wantCode {
				t.Fatalf("expected ErrorCode %q, got %q", tc.wantCode, code)
			}
		})
	}
}

// confusingErrorPlugin implements the full AuthPlugin interface, but its
// LoginStart method itself fails, returning an error whose text happens to
// share the "rpc: can't find method" prefix net/rpc uses for its own
// method-not-found error — for a different service and method than the one
// invoked, as could occur when a plugin propagates a downstream RPC failure
// verbatim (e.g. its own call to an issuer's RPC surface).
type confusingErrorPlugin struct {
	UnimplementedAuthPlugin
}

func (confusingErrorPlugin) Init(req *InitRequest, resp *InitResponse) error {
	return nil
}

func (confusingErrorPlugin) LoginStart(req *LoginStartRequest, resp *LoginStartResponse) error {
	return errors.New("rpc: can't find method Issuer.Refresh")
}

// TestClient_Call_DoesNotMisclassifyDownstreamError exercises a method that
// IS registered but whose own implementation fails with an error string that
// happens to start with the same text net/rpc uses for a genuinely absent
// method. call must surface this as a real, non-nil error rather than
// translating it into ErrorCodeUnsupported.
func TestClient_Call_DoesNotMisclassifyDownstreamError(t *testing.T) {
	plugin := &confusingErrorPlugin{}
	client := newTestClient(t, plugin)
	defer client.Close()

	var resp LoginStartResponse
	err := client.call("LoginStart", &LoginStartRequest{Mode: "browser"}, &resp)
	if err == nil {
		t.Fatal("expected a non-nil error, got nil")
	}
	if resp.ErrorCode == ErrorCodeUnsupported {
		t.Fatal("expected the downstream error not to be misclassified as ErrorCodeUnsupported")
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
