// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package auth

import (
	"net/rpc"
	"testing"
)

// onlyInit embeds UnimplementedAuthPlugin and implements only Init itself,
// showing that the embed alone satisfies the widened AuthPlugin interface.
type onlyInit struct {
	UnimplementedAuthPlugin
}

func (o *onlyInit) Init(req *InitRequest, resp *InitResponse) error {
	return nil
}

var _ AuthPlugin = (*onlyInit)(nil)

// TestUnimplementedAuthPlugin_StubsReturnUnsupported covers the four verbs
// that report "unsupported" through the response's ErrorCode field with a
// nil RPC error: Validate, LoginStart, LoginWait, Logout. These verbs did
// not exist (or, for Validate, already fail closed via Valid=false) on
// hosts predating the widened interface, so a typed field in the response
// is safe.
func TestUnimplementedAuthPlugin_StubsReturnUnsupported(t *testing.T) {
	plugin := &onlyInit{}
	clientConn, serverConn := pipeConn()

	go Serve(plugin, serverConn)

	client := rpc.NewClient(clientConn)
	defer client.Close()

	tests := []struct {
		name string
		run  func(t *testing.T)
	}{
		{
			name: "Validate",
			run: func(t *testing.T) {
				var resp ValidateResponse
				if err := client.Call("AuthPlugin.Validate", &ValidateRequest{}, &resp); err != nil {
					t.Fatalf("Validate call failed: %v", err)
				}
				if resp.Valid {
					t.Fatal("expected Valid=false")
				}
				if resp.ErrorCode != ErrorCodeUnsupported {
					t.Fatalf("expected ErrorCode %q, got %q", ErrorCodeUnsupported, resp.ErrorCode)
				}
			},
		},
		{
			name: "LoginStart",
			run: func(t *testing.T) {
				var resp LoginStartResponse
				if err := client.Call("AuthPlugin.LoginStart", &LoginStartRequest{}, &resp); err != nil {
					t.Fatalf("LoginStart call failed: %v", err)
				}
				if resp.ErrorCode != ErrorCodeUnsupported {
					t.Fatalf("expected ErrorCode %q, got %q", ErrorCodeUnsupported, resp.ErrorCode)
				}
			},
		},
		{
			name: "LoginWait",
			run: func(t *testing.T) {
				var resp LoginWaitResponse
				if err := client.Call("AuthPlugin.LoginWait", &LoginWaitRequest{}, &resp); err != nil {
					t.Fatalf("LoginWait call failed: %v", err)
				}
				if resp.ErrorCode != ErrorCodeUnsupported {
					t.Fatalf("expected ErrorCode %q, got %q", ErrorCodeUnsupported, resp.ErrorCode)
				}
			},
		},
		{
			name: "Logout",
			run: func(t *testing.T) {
				var resp LogoutResponse
				if err := client.Call("AuthPlugin.Logout", &LogoutRequest{}, &resp); err != nil {
					t.Fatalf("Logout call failed: %v", err)
				}
				if resp.ErrorCode != ErrorCodeUnsupported {
					t.Fatalf("expected ErrorCode %q, got %q", ErrorCodeUnsupported, resp.ErrorCode)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, tt.run)
	}
}

// TestUnimplementedAuthPlugin_GetAuthHeaderFailsClosed exercises the one
// stub that must signal unsupported through the RPC error channel instead
// of a response field: a host built against the pre-widening
// GetAuthHeaderResponse (Headers only) cannot see ErrorCode, so a nil-error
// response with empty Headers would read as successful, unauthenticated
// access. The call must come back with a non-nil error over the real
// net/rpc round trip, not merely from the Go method in isolation.
func TestUnimplementedAuthPlugin_GetAuthHeaderFailsClosed(t *testing.T) {
	plugin := &onlyInit{}
	clientConn, serverConn := pipeConn()

	go Serve(plugin, serverConn)

	client := rpc.NewClient(clientConn)
	defer client.Close()

	var resp GetAuthHeaderResponse
	err := client.Call("AuthPlugin.GetAuthHeader", &GetAuthHeaderRequest{}, &resp)
	if err == nil {
		t.Fatal("expected a non-nil error from GetAuthHeader")
	}
}
