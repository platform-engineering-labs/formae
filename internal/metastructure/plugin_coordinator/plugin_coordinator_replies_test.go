// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package plugin_coordinator

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/metastructure/messages"
)

// A node lookup for a namespace no plugin serves is a request-scoped failure:
// the caller must get an answer and the coordinator must keep serving.
// Terminating instead drops every registered plugin with the actor.
func TestPluginCoordinator_UnknownNamespaceNodeLookup_RepliesAndStaysAlive(t *testing.T) {
	listener, sender := newCoordinatorForTest(t)

	result := listener.Call(sender, messages.GetPluginNode{Namespace: "nonexistent"})

	require.NoError(t, result.Error, "an unknown namespace must be answered, not terminate the coordinator")
	node, ok := result.Response.(messages.PluginNode)
	require.True(t, ok, "the caller must receive a typed reply, got %T", result.Response)
	assert.Contains(t, node.Error, "plugin not found")

	// The same actor instance must keep serving requests.
	next := listener.Call(sender, messages.GetRegisteredPlugins{})
	require.NoError(t, next.Error, "the coordinator must still serve after a failed lookup")
	_, ok = next.Response.(messages.GetRegisteredPluginsResult)
	assert.True(t, ok, "a valid request after a failure must succeed, got %T", next.Response)
}
