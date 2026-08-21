// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package credential

import (
	"context"
	"errors"
	"fmt"
	"time"

	"ergo.services/ergo/act"
	"ergo.services/ergo/gen"
)

// ServerActorName is the registered process name the agent calls into for
// IdentityToken requests.
const ServerActorName = "oidc_credential_server"

// requestTimeout bounds how long the server waits for the plugin to answer
// an IdentityToken call before giving up on it.
const requestTimeout = 10 * time.Second

// CredentialActor serves IdentityToken requests over Ergo's synchronous
// call. The carrier is []byte in, []byte out - both Encode'd wire types -
// which is EDF-native and needs no type registration.
type CredentialActor struct {
	act.Actor

	plugin OidcCredentialPlugin
}

func factoryCredentialActor() gen.ProcessBehavior {
	return &CredentialActor{}
}

// Init pulls the already-configured plugin out of the process environment,
// mirroring pkg/plugin.PluginActor.Init's "Plugin" env lookup.
func (a *CredentialActor) Init(args ...any) error {
	pluginVal, ok := a.Env("Plugin")
	if !ok {
		return fmt.Errorf("CredentialActor: missing 'Plugin' in environment")
	}
	plugin, ok := pluginVal.(OidcCredentialPlugin)
	if !ok {
		return fmt.Errorf("CredentialActor: 'Plugin' has wrong type (expected OidcCredentialPlugin)")
	}
	a.plugin = plugin
	return nil
}

// HandleCall decodes the request, mints (or fails to mint) the token, and
// always answers with an Encode'd IdentityTokenResponse - errors are
// carried in that envelope, never as the second return value, so a caller
// always gets bytes back to run through DecodeResponse.
func (a *CredentialActor) HandleCall(from gen.PID, ref gen.Ref, request any) (any, error) {
	data, _ := request.([]byte)
	return handle(context.Background(), a.plugin, data, requestTimeout, a.Log()), nil
}

// errorLogger is the minimal logging surface handle needs. gen.Log satisfies
// it structurally, so the actor can pass a.Log() straight through while
// tests supply a small stub instead of implementing all of gen.Log.
type errorLogger interface {
	Error(format string, args ...any)
}

// handle is the pure request/response mapping at the heart of HandleCall,
// factored out so it's testable without a running Ergo node.
//
// Error mapping is fail-closed:
//   - the request doesn't decode                                -> ErrCodeInternal
//   - errors.Is(err, ErrInvalidAudience)                         -> ErrCodeInvalidAudience
//   - any other plugin error                                    -> ErrCodeMintFailed
//   - nil result and nil error (including a timeout, which
//     callWithTimeout reports as nil/nil since the plugin never
//     actually answered)                                        -> ErrCodeInternal
//
// On a plugin error, the failure is logged broker-side via log (naming the
// requestId, audience, and mapped code) but the plugin's error text is never
// placed on the wire: encodeErrorResponse only ever carries ErrorCode, never
// ErrorMessage, so nothing token-shaped can leak through an error string to
// the caller.
func handle(ctx context.Context, plugin OidcCredentialPlugin, data []byte, timeout time.Duration, log errorLogger) []byte {
	var req OidcIdentityTokenRequest
	if err := Decode(data, &req); err != nil {
		return encodeErrorResponse(ErrCodeInternal)
	}

	result, err := callWithTimeout(ctx, plugin, &req, timeout)

	switch {
	case err != nil:
		code := ErrCodeMintFailed
		if errors.Is(err, ErrInvalidAudience) {
			code = ErrCodeInvalidAudience
		}
		if log != nil {
			log.Error("oidc-credential mint failed: requestId=%s audience=%s code=%s err=%q", req.RequestID, req.Audience, code, err)
		}
		return encodeErrorResponse(code)
	case result == nil:
		return encodeErrorResponse(ErrCodeInternal)
	default:
		encoded, encErr := Encode(IdentityTokenResponse{Result: result})
		if encErr != nil {
			return encodeErrorResponse(ErrCodeInternal)
		}
		return encoded
	}
}

// callWithTimeout runs the plugin's IdentityToken call, giving up after
// timeout. On timeout it reports (nil, nil): the plugin never answered
// within the deadline, so there is no plugin error to report - the
// abandoned call's eventual result (if the plugin ignores ctx cancellation)
// is discarded. handle's nil/nil branch turns that into ErrCodeInternal.
func callWithTimeout(ctx context.Context, plugin OidcCredentialPlugin, req *OidcIdentityTokenRequest, timeout time.Duration) (*OidcIdentityTokenResult, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	type outcome struct {
		result *OidcIdentityTokenResult
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		result, err := plugin.IdentityToken(ctx, req)
		done <- outcome{result, err}
	}()

	select {
	case o := <-done:
		return o.result, o.err
	case <-ctx.Done():
		return nil, nil
	}
}

// encodeErrorResponse builds the wire envelope for a failed request. Encode
// of this small, static struct is not expected to fail; if it somehow does,
// there's nothing more fail-closed to return than empty bytes, which
// DecodeResponse's Decode call will itself reject.
func encodeErrorResponse(code string) []byte {
	data, err := Encode(IdentityTokenResponse{ErrorCode: code})
	if err != nil {
		return nil
	}
	return data
}
