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
