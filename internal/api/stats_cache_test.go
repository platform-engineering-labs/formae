//go:build unit

// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package api

import (
	"errors"
	"testing"
	"time"

	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
	"github.com/stretchr/testify/require"
)

type countingStats struct {
	calls int
	err   error
}

func (c *countingStats) Stats() (*apimodel.Stats, error) {
	c.calls++
	if c.err != nil {
		return nil, c.err
	}
	return &apimodel.Stats{Stacks: c.calls}, nil
}

// The metrics reader asks for stats every export; the datastore answers with
// several aggregate queries over every resource. One answer is good for a
// while, so within the interval the reader gets the last answer and the
// datastore is left alone.
func TestCachedStatsProviderAsksTheDatastoreOncePerInterval(t *testing.T) {
	inner := &countingStats{}
	now := time.Unix(0, 0)
	cached := newCachedStatsProvider(inner, time.Minute, func() time.Time { return now })

	first, err := cached.Stats()
	require.NoError(t, err)
	second, err := cached.Stats()
	require.NoError(t, err)
	require.Equal(t, 1, inner.calls, "the second call within the interval must not reach the datastore")
	require.Equal(t, first, second)

	now = now.Add(time.Minute + time.Second)
	third, err := cached.Stats()
	require.NoError(t, err)
	require.Equal(t, 2, inner.calls, "after the interval the datastore is asked again")
	require.NotEqual(t, first.Stacks, third.Stacks)
}

// An error is not an answer: nothing is cached and the next call tries again.
func TestCachedStatsProviderDoesNotCacheAnError(t *testing.T) {
	inner := &countingStats{err: errors.New("datastore down")}
	now := time.Unix(0, 0)
	cached := newCachedStatsProvider(inner, time.Minute, func() time.Time { return now })

	_, err := cached.Stats()
	require.Error(t, err)
	inner.err = nil
	got, err := cached.Stats()
	require.NoError(t, err)
	require.Equal(t, 2, inner.calls)
	require.Equal(t, 2, got.Stacks)
}
