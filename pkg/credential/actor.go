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
// call. Request and response are the registered wire types themselves
// (RegisterEDFTypes), each carrying its own MarshalEDF/UnmarshalEDF, so the
// transport does the serializing and this actor only ever sees typed values.
type CredentialActor struct {
	act.Actor

	plugin OidcCredentialPlugin

	// log is the actor's own gen.Log, captured in Init. Holding it as the
	// narrow errorLogger seam keeps HandleCall callable without a running
	// Ergo node, which act.Actor.Log() requires.
	log errorLogger
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
	a.log = a.Log()
	return nil
}

// HandleCall mints (or fails to mint) the token and always answers a known
// request with an IdentityTokenResponse - a mint failure is carried in that
// envelope, never as the second return value, so the caller always gets an
// envelope to run through ResponseError. An unknown request type is the
// only case answered with an error, following the coordinator's arm-plus-
// default shape.
//
// Ergo delivers a registered marshaler type as a VALUE (net/edf/register.go
// builds the decode target with reflect.Indirect(reflect.New(T))), which is
// why the announcement arm in the plugin coordinator matches on the value
// type too.
func (a *CredentialActor) HandleCall(from gen.PID, ref gen.Ref, request any) (any, error) {
	switch req := request.(type) {
	case OidcIdentityTokenRequest:
		return handle(context.Background(), a.plugin, req, requestTimeout, a.log), nil

	default:
		return nil, fmt.Errorf("unknown request: %T", request)
	}
}

// errorLogger is the minimal logging surface handle needs. gen.Log satisfies
// it structurally, so the actor stores a.Log() as one while tests supply a
// small stub instead of implementing all of gen.Log.
type errorLogger interface {
	Error(format string, args ...any)
}

// handle is the pure request/response mapping at the heart of HandleCall,
// factored out so it's testable without a running Ergo node.
//
// Error mapping is fail-closed:
//   - errors.Is(err, ErrInvalidAudience)                         -> ErrCodeInvalidAudience
//   - any other plugin error                                    -> ErrCodeMintFailed
//   - nil result and nil error (including a timeout, which
//     callWithTimeout reports as nil/nil since the plugin never
//     actually answered)                                        -> ErrCodeInternal
//
// An undecodable payload is not a case at this layer: UnmarshalEDF runs
// before HandleCall, so a request that doesn't decode never reaches the
// actor and Ergo fails the caller's call outright.
//
// On a plugin error, the failure is logged broker-side via log (naming the
// requestId, audience, and mapped code) but the plugin's error text is never
// placed on the wire: the failure envelope only ever carries ErrorCode,
// never ErrorMessage, so nothing token-shaped can leak through an error
// string to the caller.
func handle(ctx context.Context, plugin OidcCredentialPlugin, req OidcIdentityTokenRequest, timeout time.Duration, log errorLogger) IdentityTokenResponse {
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
		return IdentityTokenResponse{ErrorCode: code}
	case result == nil:
		return IdentityTokenResponse{ErrorCode: ErrCodeInternal}
	default:
		return IdentityTokenResponse{Result: result}
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
