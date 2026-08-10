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
			name: "GetAuthHeader",
			run: func(t *testing.T) {
				var resp GetAuthHeaderResponse
				if err := client.Call("AuthPlugin.GetAuthHeader", &GetAuthHeaderRequest{}, &resp); err != nil {
					t.Fatalf("GetAuthHeader call failed: %v", err)
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
