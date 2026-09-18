// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package plugin

import (
	"context"
	"fmt"
	"github.com/platform-engineering-labs/formae/pkg/credential"
	"testing"
	"time"

	"ergo.services/ergo/gen"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

func operatorEnvWithBroker(extra map[gen.Env]any) map[gen.Env]any {
	env := map[gen.Env]any{
		"Plugin":      newRecordingPlugin(),
		"Context":     context.Background(),
		"RetryConfig": pkgmodel.RetryConfig{MaxRetries: 3},
	}
	for k, v := range extra {
		env[k] = v
	}
	return env
}

func TestOperatorInit_PairedBrokerReachesEveryCallPath(t *testing.T) {
	operator := &PluginOperator{}
	proc := newOperatorProcess(operatorEnvWithBroker(map[gen.Env]any{
		"OidcCredentialBrokerNode":      "fai@localhost",
		"OidcCredentialBrokerName":      "oidc_credential_server",
		"OidcOperationBindingID":        "opaque-binding",
		"OidcOperationPollInterval":     7 * time.Second,
		"OidcOperationCallTimeout":      60 * time.Second,
		"OidcOperationRetryDelay":       11 * time.Second,
		"OidcOperationThrottleMaxDelay": 30 * time.Second,
	}), nil)
	proc.behavior = operator

	require.NoError(t, operator.ProcessInit(proc))

	client, ok := oidcBrokerClientFrom(operator.Data().context)
	require.True(t, ok, "a paired operator must carry a broker client on its context")
	assert.Equal(t, operatorTestNamespace, client.namespace)

	// Every operation receives the operator context, so the client reaches
	// watched operations and discovery alike.
	plugin := newRecordingPlugin()
	data := operatorTestData(plugin)
	data.context = operator.Data().context

	callProc := newOperatorProcess(nil, nil)
	read(gen.PID{}, StateNotStarted, data, ReadResource{Namespace: operatorTestNamespace, NativeID: "resource-1"}, callProc)
	create(gen.PID{}, StateNotStarted, data, CreateResource{Namespace: operatorTestNamespace, ResourceType: "Test::Resource"}, callProc)
	update(gen.PID{}, StateNotStarted, data, UpdateResource{Namespace: operatorTestNamespace, NativeID: "resource-1"}, callProc)
	delete(gen.PID{}, StateNotStarted, data, DeleteResource{Namespace: operatorTestNamespace, NativeID: "resource-1"}, callProc)
	status(gen.PID{}, StateWaitingForResource, data, PluginOperatorCheckStatus{Namespace: operatorTestNamespace, RequestID: "request-1"}, callProc)
	_, _, _, err := list(gen.PID{}, StateNotStarted, data, ListResources{Namespace: operatorTestNamespace, ResourceType: "Test::Resource"}, callProc)
	require.NoError(t, err)

	for _, operation := range []resource.Operation{
		resource.OperationRead,
		resource.OperationCreate,
		resource.OperationUpdate,
		resource.OperationDelete,
		resource.OperationCheckStatus,
		resource.OperationList,
	} {
		_, ok := oidcBrokerClientFrom(plugin.contextFor(t, operation))
		assert.True(t, ok, "the %s call context must carry the broker client", operation)
		info, ok := OidcOperationMetadata(plugin.contextFor(t, operation))
		require.True(t, ok)
		assert.Equal(t, OidcOperationInfo{"opaque-binding", 7 * time.Second, 60 * time.Second, 11 * time.Second, 30 * time.Second}, info)
	}
}

func TestOperatorInit_NoBrokerEnvCarriesNoClient(t *testing.T) {
	operator := &PluginOperator{}
	proc := newOperatorProcess(operatorEnvWithBroker(nil), nil)
	proc.behavior = operator

	require.NoError(t, operator.ProcessInit(proc))

	_, ok := oidcBrokerClientFrom(operator.Data().context)
	assert.False(t, ok, "an unpaired operator must carry no broker client")
}

// The pairing is injected atomically, so exactly one key present is a broken
// pairing: refusing to start beats running as if no broker were paired.
func TestOperatorInit_PartialEnvPairFailsInit(t *testing.T) {
	tests := []struct {
		name string
		env  map[gen.Env]any
	}{
		{name: "node without name", env: map[gen.Env]any{"OidcCredentialBrokerNode": "fai@localhost"}},
		{name: "name without node", env: map[gen.Env]any{"OidcCredentialBrokerName": "oidc_credential_server"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			operator := &PluginOperator{}
			proc := newOperatorProcess(operatorEnvWithBroker(tt.env), nil)
			proc.behavior = operator

			err := operator.ProcessInit(proc)

			require.Error(t, err)
			assert.Contains(t, err.Error(), "oidc-credential broker")
		})
	}
}

func TestOperatorInit_UnusableBrokerPairFailsInit(t *testing.T) {
	tests := []struct {
		name string
		env  map[gen.Env]any
	}{
		{name: "empty node", env: map[gen.Env]any{
			"OidcCredentialBrokerNode": "",
			"OidcCredentialBrokerName": "oidc_credential_server",
		}},
		{name: "empty name", env: map[gen.Env]any{
			"OidcCredentialBrokerNode": "fai@localhost",
			"OidcCredentialBrokerName": "",
		}},
		{name: "wrong type", env: map[gen.Env]any{
			"OidcCredentialBrokerNode": gen.Atom("fai@localhost"),
			"OidcCredentialBrokerName": "oidc_credential_server",
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			operator := &PluginOperator{}
			proc := newOperatorProcess(operatorEnvWithBroker(tt.env), nil)
			proc.behavior = operator

			err := operator.ProcessInit(proc)

			require.Error(t, err)
			assert.Contains(t, err.Error(), "oidc-credential broker")
		})
	}
}

func TestOperatorMetadata_RequiresCompleteProcessEnv(t *testing.T) {
	valid := map[gen.Env]any{
		"OidcOperationBindingID": "opaque-binding", "OidcOperationPollInterval": 7 * time.Second,
		"OidcOperationCallTimeout": 60 * time.Second, "OidcOperationRetryDelay": 0 * time.Second,
		"OidcOperationThrottleMaxDelay": 30 * time.Second,
	}
	for _, key := range []gen.Env{"OidcOperationBindingID", "OidcOperationPollInterval", "OidcOperationCallTimeout", "OidcOperationRetryDelay", "OidcOperationThrottleMaxDelay"} {
		invalid := []any{nil, "wrong type", -time.Second}
		if key == "OidcOperationCallTimeout" {
			invalid = append(invalid, time.Duration(0))
		}
		for i, bad := range invalid {
			t.Run(fmt.Sprintf("%s-invalid-%d", key, i), func(t *testing.T) {
				env := map[gen.Env]any{"OidcCredentialBrokerNode": "broker@localhost", "OidcCredentialBrokerName": "broker"}
				for k, v := range valid {
					if !(bad == nil && k == key) {
						env[k] = v
					}
				}
				if bad != nil {
					if key == "OidcOperationBindingID" && i == 1 {
						env[key] = ""
					} else {
						env[key] = bad
					}
				}
				// Node environment must never repair missing or malformed process metadata.
				o := &PluginOperator{}
				p := newOperatorProcess(operatorEnvWithBroker(env), valid)
				p.behavior = o
				require.NoError(t, o.ProcessInit(p))
				_, ok := OidcOperationMetadata(o.Data().context)
				require.False(t, ok)
				_, ok = oidcBrokerClientFrom(o.Data().context)
				require.True(t, ok, "ordinary OIDC remains available")
			})
		}
	}
	for _, env := range []map[gen.Env]any{nil, valid} {
		o := &PluginOperator{}
		p := newOperatorProcess(operatorEnvWithBroker(env), valid)
		p.behavior = o
		require.NoError(t, o.ProcessInit(p))
		_, ok := OidcOperationMetadata(o.Data().context)
		require.False(t, ok, "metadata requires pairing and process env")
	}
	_, ok := OidcOperationMetadata(context.WithValue(context.Background(), "OidcOperationBindingID", "forged"))
	require.False(t, ok)
}

func TestOperatorMetadata_OlderAgentStillMints(t *testing.T) {
	o := &PluginOperator{}
	proc := newOperatorProcess(operatorEnvWithBroker(map[gen.Env]any{"OidcCredentialBrokerNode": "broker@localhost", "OidcCredentialBrokerName": "broker"}), nil)
	proc.behavior = o
	require.NoError(t, o.ProcessInit(proc))
	_, ok := OidcOperationMetadata(o.Data().context)
	require.False(t, ok)
	client, ok := oidcBrokerClientFrom(o.Data().context)
	require.True(t, ok)
	client.call = func(credential.OidcIdentityTokenRequest) (credential.IdentityTokenResponse, error) {
		return credential.IdentityTokenResponse{Result: &credential.OidcIdentityTokenResult{Token: "jwt", ExpiresAt: time.Now().Add(time.Minute)}}, nil
	}
	_, err := NewOidcTokenSource().IdentityToken(o.Data().context, "audience")
	require.NoError(t, err)
}
