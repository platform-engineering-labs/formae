// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package plugin

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"ergo.services/ergo/gen"
	"github.com/google/uuid"

	"github.com/platform-engineering-labs/formae/pkg/credential"
)

// oidcBrokerCallTimeoutSeconds bounds a call to the paired broker. It matches
// the broker's maximum request budget. The bounded request makes the receiver
// stop earlier using the enclosing call reference deadline and response margin.
const oidcBrokerCallTimeoutSeconds = 10

// OidcTokenSource mints short-lived OIDC identity tokens for the audience a
// resource plugin needs to authenticate to. A plugin receives one via
// OidcAware and calls it with the context of the operation it is serving.
// Concurrent calls within one operation are safe but serialized, so a fan-out
// that needs several tokens pays for them one at a time. A deadline must leave
// the full 10s synchronous broker allowance after serialization; shorter budgets
// fail with context.DeadlineExceeded before a mint is started.
// The paired broker must support bounded requests; older receivers fail closed.
type OidcTokenSource interface {
	IdentityToken(ctx context.Context, audience string) (string, error)
}

// OidcAware is implemented by resource plugins that mint OIDC identity tokens.
// The SDK installs the source once at startup; the source resolves the paired
// broker per call from the operation's context, so the plugin holds no
// per-operation state.
type OidcAware interface {
	SetOidcTokenSource(src OidcTokenSource)
}

// ErrNoOidcBroker reports that no oidc-credential broker serves this plugin's
// namespace, or that the call was made outside an operation and so carries no
// broker at all.
var ErrNoOidcBroker = errors.New("no oidc-credential broker paired")

// brokerCallFunc carries an OidcIdentityTokenRequest to the broker and returns
// its IdentityTokenResponse. Both are EDF-registered message types, so the
// transport serializes them through their own marshaler hooks.
type brokerCallFunc func(req credential.OidcIdentityTokenRequest) (credential.IdentityTokenResponse, error)

// oidcBrokerClient is the broker an operation may call, put on the operation's
// context by the PluginOperator. namespace is the resource plugin's namespace,
// carried for diagnostics.
type oidcBrokerClient struct {
	namespace string
	call      brokerCallFunc

	// gate serializes synchronous Ergo calls. Waiting callers can cancel, but
	// an in-flight call completes synchronously within the broker call ceiling.
	gateOnce sync.Once
	gate     chan struct{}
}

func (c *oidcBrokerClient) invoke(ctx context.Context, req credential.OidcIdentityTokenRequest) (credential.IdentityTokenResponse, error) {
	c.gateOnce.Do(func() { c.gate = make(chan struct{}, 1) })
	select {
	case <-ctx.Done():
		return credential.IdentityTokenResponse{}, ctx.Err()
	case c.gate <- struct{}{}:
	}
	defer func() { <-c.gate }()
	// Cancellation can race acquisition when both select branches are ready.
	if err := ctx.Err(); err != nil {
		return credential.IdentityTokenResponse{}, err
	}
	// Reserve the entire synchronous call. The bounded wire request also makes
	// the receiver include mailbox waiting in its allowance, ending before this
	// call's timeout instead of starting a fresh mint budget after dequeue.
	if deadline, ok := ctx.Deadline(); ok && time.Until(deadline) < oidcBrokerCallTimeoutSeconds*time.Second {
		return credential.IdentityTokenResponse{}, fmt.Errorf("insufficient remaining time for the 10s OIDC broker call: %w", context.DeadlineExceeded)
	}
	response, err := c.call(req)
	if canceled := ctx.Err(); canceled != nil {
		return credential.IdentityTokenResponse{}, canceled
	}
	return response, err
}

// newOidcBrokerClient builds the client for the broker the coordinator paired
// with this operator, calling its registered process on the broker's node.
func newOidcBrokerClient(proc gen.Process, namespace, brokerNode, brokerName string) *oidcBrokerClient {
	target := gen.ProcessID{Name: gen.Atom(brokerName), Node: gen.Atom(brokerNode)}
	return &oidcBrokerClient{
		namespace: namespace,
		call: func(req credential.OidcIdentityTokenRequest) (credential.IdentityTokenResponse, error) {
			response, err := proc.CallWithTimeout(target, credential.OidcBoundedIdentityTokenRequest{Request: req}, oidcBrokerCallTimeoutSeconds)
			if err != nil {
				return credential.IdentityTokenResponse{}, err
			}
			// Ergo decodes a registered marshaler type into a value, never a
			// pointer, so the envelope arrives as one. Anything else is a
			// protocol error, not a mint failure, so it never reaches
			// ResponseError's fail-closed mapping.
			resp, ok := response.(credential.IdentityTokenResponse)
			if !ok {
				return credential.IdentityTokenResponse{}, fmt.Errorf("oidc-credential broker %s answered with %T, want credential.IdentityTokenResponse", target, response)
			}
			return resp, nil
		},
	}
}

// Environment keys the plugin coordinator injects, together or not at all,
// naming the oidc-credential broker paired with the operator's namespace.
const (
	envOidcBrokerNode = gen.Env("OidcCredentialBrokerNode")
	envOidcBrokerName = gen.Env("OidcCredentialBrokerName")
)

// oidcBrokerClientFromEnv reads the broker pairing off the process
// environment. It returns a nil client when neither key is present - the plugin
// simply has no broker - and an error when the pairing is incomplete or
// unusable, since serving operations as if unpaired would turn a broken pairing
// into silent credential failures at call time.
func oidcBrokerClientFromEnv(proc gen.Process, namespace string) (*oidcBrokerClient, error) {
	nodeValue, hasNode := proc.Env(envOidcBrokerNode)
	nameValue, hasName := proc.Env(envOidcBrokerName)

	switch {
	case !hasNode && !hasName:
		return nil, nil
	case hasNode != hasName:
		return nil, fmt.Errorf("pluginOperator: incomplete oidc-credential broker pairing: %s present=%t, %s present=%t",
			envOidcBrokerNode, hasNode, envOidcBrokerName, hasName)
	}

	brokerNode, nodeIsString := nodeValue.(string)
	brokerName, nameIsString := nameValue.(string)
	if !nodeIsString || !nameIsString || brokerNode == "" || brokerName == "" {
		return nil, fmt.Errorf("pluginOperator: unusable oidc-credential broker pairing: %s=%v, %s=%v",
			envOidcBrokerNode, nodeValue, envOidcBrokerName, nameValue)
	}

	return newOidcBrokerClient(proc, namespace, brokerNode, brokerName), nil
}

type oidcBrokerClientKey struct{}

// withOidcBrokerClient returns a context carrying the broker client every
// operation derived from it may call.
func withOidcBrokerClient(ctx context.Context, client *oidcBrokerClient) context.Context {
	return context.WithValue(ctx, oidcBrokerClientKey{}, client)
}

// oidcBrokerClientFrom returns the broker client on ctx, if any.
func oidcBrokerClientFrom(ctx context.Context) (*oidcBrokerClient, bool) {
	client, ok := ctx.Value(oidcBrokerClientKey{}).(*oidcBrokerClient)
	return client, ok && client != nil
}

// ctxOidcTokenSource is the OidcTokenSource the SDK installs: it reads the
// broker client off the context of the operation it is called from, so a
// single installed source serves every operation and every pairing change.
type ctxOidcTokenSource struct{}

// NewOidcTokenSource returns the token source the SDK installs on OidcAware
// plugins. It resolves the paired broker from the per-call context. Plugin
// authors never call this: the SDK hands the source to SetOidcTokenSource at
// startup.
func NewOidcTokenSource() OidcTokenSource {
	return &ctxOidcTokenSource{}
}

// IdentityToken mints a token for audience through the broker on ctx. A
// context without a broker client - a call made outside an operation, or an
// operation whose namespace has no broker paired - fails with ErrNoOidcBroker.
func (s *ctxOidcTokenSource) IdentityToken(ctx context.Context, audience string) (string, error) {
	client, ok := oidcBrokerClientFrom(ctx)
	if !ok {
		return "", fmt.Errorf("%w: no broker is configured for this plugin's namespace, or the call was made outside an operation", ErrNoOidcBroker)
	}

	response, err := client.invoke(ctx, credential.OidcIdentityTokenRequest{
		Audience:  audience,
		RequestID: uuid.NewString(),
	})
	if err != nil {
		return "", fmt.Errorf("calling the oidc-credential broker paired with namespace %s: %w", client.namespace, err)
	}

	result, err := credential.ResponseError(response)
	if err != nil {
		return "", err
	}

	return result.Token, nil
}
