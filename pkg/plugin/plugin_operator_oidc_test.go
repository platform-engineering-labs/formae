// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package plugin

import (
	"context"
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
		"OidcCredentialBrokerNode": "fai@localhost",
		"OidcCredentialBrokerName": "oidc_credential_server",
	}), nil)
	proc.behavior = operator

	require.NoError(t, operator.ProcessInit(proc))

	client, ok := oidcBrokerClientFrom(operator.Data().context)
	require.True(t, ok, "a paired operator must carry a broker client on its context")
	assert.Equal(t, deadlineTestNamespace, client.namespace)

	// Every per-call context parents from the operator's, so the client reaches
	// watched operations and discovery's long-lived list context alike.
	plugin := newRecordingPlugin()
	data := deadlineTestData(plugin, 90*time.Second)
	data.context = operator.Data().context

	callProc := newOperatorProcess(nil, nil)
	read(gen.PID{}, StateNotStarted, data, ReadResource{Namespace: deadlineTestNamespace, NativeID: "resource-1"}, callProc)
	create(gen.PID{}, StateNotStarted, data, CreateResource{Namespace: deadlineTestNamespace, ResourceType: "Test::Resource"}, callProc)
	update(gen.PID{}, StateNotStarted, data, UpdateResource{Namespace: deadlineTestNamespace, NativeID: "resource-1"}, callProc)
	delete(gen.PID{}, StateNotStarted, data, DeleteResource{Namespace: deadlineTestNamespace, NativeID: "resource-1"}, callProc)
	status(gen.PID{}, StateWaitingForResource, data, PluginOperatorCheckStatus{Namespace: deadlineTestNamespace, RequestID: "request-1"}, callProc)
	_, _, _, err := list(gen.PID{}, StateNotStarted, data, ListResources{Namespace: deadlineTestNamespace, ResourceType: "Test::Resource"}, callProc)
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
