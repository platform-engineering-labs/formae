// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package changeset

import (
	"testing"
	"time"

	"ergo.services/ergo/gen"
	"ergo.services/ergo/testing/unit"
	"github.com/platform-engineering-labs/formae/internal/metastructure/testutil"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRateLimiter_GrantsTokensWhenAvailable(t *testing.T) {
	listener, sender, err := newRateLimiterForTest(t)
	require.NoError(t, err)

	// FakeAWS has a rate limit of 5, so the 2 requested tokens should be readily available
	res := listener.Call(sender, RequestTokens{Namespace: "FakeAWS", N: 2})
	require.Nil(t, res.Error)

	grant, ok := res.Response.(TokensGranted)
	require.True(t, ok)

	assert.Equal(t, int(2), grant.N)
}

func TestRateLimiter_GrantsNoMoreTokensThanAvailable(t *testing.T) {
	listener, sender, err := newRateLimiterForTest(t)
	require.NoError(t, err)

	// FakeAWS has a rate limit of 5, so from the 9 requested tokens, 5 should be granted
	res := listener.Call(sender, RequestTokens{Namespace: "FakeAWS", N: 9})
	require.Nil(t, res.Error)

	grant, ok := res.Response.(TokensGranted)
	require.True(t, ok)

	assert.Equal(t, int(5), grant.N)

	// If we make a subsequent request within the same second, no more tokens should be granted
	res = listener.Call(sender, RequestTokens{Namespace: "FakeAWS", N: 1})
	require.Nil(t, res.Error)

	grant, ok = res.Response.(TokensGranted)
	require.True(t, ok)

	assert.Equal(t, int(0), grant.N)
}

func TestRateLimited_RefillsTokensEverySecond(t *testing.T) {
	listener, sender, err := newRateLimiterForTest(t)
	require.NoError(t, err)

	// FakeAWS has a rate limit of 5, so all 5 should be granted, depleting the bucket
	res := listener.Call(sender, RequestTokens{Namespace: "FakeAWS", N: 5})
	require.Nil(t, res.Error)

	grant, ok := res.Response.(TokensGranted)
	require.True(t, ok)

	assert.Equal(t, int(5), grant.N)

	// If we wait a second before the next request, the bucket should be replenished
	time.Sleep(1 * time.Second)

	res = listener.Call(sender, RequestTokens{Namespace: "FakeAWS", N: 1})
	require.Nil(t, res.Error)

	grant, ok = res.Response.(TokensGranted)
	require.True(t, ok)

	assert.Equal(t, int(1), grant.N)
}

func TestExtractRequesterName(t *testing.T) {
	tests := []struct {
		name         string
		processName  string
		expectedName string
	}{
		// Simple constant actor names
		{
			name:         "Discovery actor",
			processName:  "Discovery",
			expectedName: "Discovery",
		},
		{
			name:         "Synchronizer actor",
			processName:  "Synchronizer",
			expectedName: "Synchronizer",
		},
		{
			name:         "RateLimiter actor",
			processName:  "RateLimiter",
			expectedName: "RateLimiter",
		},

		// ChangesetExecutor with command ID
		{
			name:         "ChangesetExecutor with commandID",
			processName:  "formae://changeset/executor/2DNQHgRwgr7w5pErNFLpkLJWnjE",
			expectedName: "ChangesetExecutor",
		},

		// ResolveCache with command ID
		{
			name:         "ResolveCache with commandID",
			processName:  "formae://changeset/resolve-cache/2DNQHgRwgr7w5pErNFLpkLJWnjE",
			expectedName: "ResolveCache",
		},

		// ResourceUpdater with resource URI
		{
			name:         "ResourceUpdater with full URI",
			processName:  "formae://37IqT4myGQobI13AuWIhJlPud3V#/resource-updater/read/37IqT3mCvdUZ7FPoRoaD9nTsJiX",
			expectedName: "ResourceUpdater",
		},
		{
			name:         "ResourceUpdater with create operation",
			processName:  "formae://37IqaJzrXmg0O7TrQhVfTAAXQjJ#/resource-updater/create/37IqaFhsYH0VEU1I5JuDZPfkMw2",
			expectedName: "ResourceUpdater",
		},
		{
			name:         "ResourceUpdater with update operation",
			processName:  "formae://2DNQHgRwgr7w5pErNFLpkLJWnjE#/resource-updater/update/2DNQHgRwgr7w5pErNFLpkLJWnjE",
			expectedName: "ResourceUpdater",
		},

		// PluginOperator (rare, runs remotely but test for completeness)
		{
			name:         "PluginOperator with resource URI",
			processName:  "formae://37IqT4myGQobI13AuWIhJlPud3V#read/2DNQHgRwgr7w5pErNFLpkLJWnjE",
			expectedName: "PluginOperator",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractRequesterName(tt.processName)
			assert.Equal(t, tt.expectedName, result,
				"extractRequesterName(%q) should return %q, got %q",
				tt.processName, tt.expectedName, result)
		})
	}
}

// TestExtractRequesterName_AllKnownActors ensures we handle all actors that can make rate limiter requests
func TestExtractRequesterName_AllKnownActors(t *testing.T) {
	// These are the only actors that should be making rate limiter requests
	knownRequesters := []struct {
		actorType   string
		exampleName string
	}{
		{"Discovery", "Discovery"},
		{"Synchronizer", "Synchronizer"},
		{"ChangesetExecutor", "formae://changeset/executor/2DNQHgRwgr7w5pErNFLpkLJWnjE"},
		{"ResolveCache", "formae://changeset/resolve-cache/2DNQHgRwgr7w5pErNFLpkLJWnjE"},
	}

	for _, req := range knownRequesters {
		t.Run(req.actorType, func(t *testing.T) {
			result := extractRequesterName(req.exampleName)
			assert.Equal(t, req.actorType, result,
				"Actor %q should extract to %q for rate limiting metrics",
				req.exampleName, req.actorType)
		})
	}
}

func newRateLimiterForTest(t *testing.T) (*unit.TestActor, gen.PID, error) {
	sender := gen.PID{Node: "test", ID: 100}

	projectRoot, err := testutil.FindProjectRoot()
	assert.NoError(t, err, "Failed to get project root")
	pluginPath := projectRoot + "/plugins"

	pluginManager := plugin.NewManager("", pluginPath)
	pluginManager.Load()

	env := map[gen.Env]any{
		"PluginManager": pluginManager,
	}

	listener, err := unit.Spawn(t, NewRateLimiter, unit.WithLogLevel(gen.LogLevelDebug), unit.WithEnv(env))
	if err != nil {
		return nil, gen.PID{}, err
	}

	return listener, sender, nil
}
