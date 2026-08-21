// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package plugin

import (
	"context"
	"errors"
	"fmt"

	"ergo.services/ergo/gen"
	"github.com/google/uuid"

	"github.com/platform-engineering-labs/formae/pkg/credential"
)

// oidcBrokerCallTimeoutSeconds bounds a call to the paired broker. It matches
// the broker's own request budget; Process.Call's fixed 5s default would give
// up before the broker does, so every call names this timeout explicitly.
const oidcBrokerCallTimeoutSeconds = 10

// OidcTokenSource mints short-lived OIDC identity tokens for the audience a
// resource plugin needs to authenticate to. A plugin receives one via
// OidcAware and calls it with the context of the operation it is serving.
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

// brokerCallFunc carries an encoded OidcIdentityTokenRequest to the broker and
// returns its encoded IdentityTokenResponse.
type brokerCallFunc func(payload []byte) ([]byte, error)

// oidcBrokerClient is the broker an operation may call, put on the operation's
// context by the PluginOperator. namespace is the resource plugin's namespace,
// carried for diagnostics.
type oidcBrokerClient struct {
	namespace string
	call      brokerCallFunc
}

// newOidcBrokerClient builds the client for the broker the coordinator paired
// with this operator, calling its registered process on the broker's node.
func newOidcBrokerClient(proc gen.Process, namespace, brokerNode, brokerName string) *oidcBrokerClient {
	target := gen.ProcessID{Name: gen.Atom(brokerName), Node: gen.Atom(brokerNode)}
	return &oidcBrokerClient{
		namespace: namespace,
		call: func(payload []byte) ([]byte, error) {
			response, err := proc.CallWithTimeout(target, payload, oidcBrokerCallTimeoutSeconds)
			if err != nil {
				return nil, err
			}
			data, ok := response.([]byte)
			if !ok {
				return nil, fmt.Errorf("oidc-credential broker %s answered with %T, want []byte", target, response)
			}
			return data, nil
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
// plugins. It resolves the paired broker from the per-call context.
func NewOidcTokenSource() OidcTokenSource {
	return &ctxOidcTokenSource{}
}

// IdentityToken mints a token for audience through the broker on ctx. A
// context without a broker client - a call made outside an operation, or an
// operation whose namespace has no broker paired - fails with ErrNoOidcBroker.
func (s *ctxOidcTokenSource) IdentityToken(ctx context.Context, audience string) (string, error) {
	client, ok := oidcBrokerClientFrom(ctx)
	if !ok {
		return "", fmt.Errorf("%w (unavailable outside an operation)", ErrNoOidcBroker)
	}

	payload, err := credential.Encode(&credential.OidcIdentityTokenRequest{
		Audience:  audience,
		RequestID: uuid.NewString(),
	})
	if err != nil {
		return "", fmt.Errorf("encoding the identity token request for namespace %s: %w", client.namespace, err)
	}

	response, err := client.call(payload)
	if err != nil {
		return "", fmt.Errorf("calling the oidc-credential broker paired with namespace %s: %w", client.namespace, err)
	}

	result, err := credential.DecodeResponse(response)
	if err != nil {
		return "", err
	}

	return result.Token, nil
}
