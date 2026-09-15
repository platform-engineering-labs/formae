// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package api

import (
	"sync"
	"time"

	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
)

// cachedStatsProvider answers Stats from the last successful reading for the
// length of one interval, and only then asks the wrapped provider again.
//
// The metrics reader calls Stats on every export, and the datastore answers
// with aggregate queries over every resource it holds. Those numbers change
// slowly and nothing reads them faster than a dashboard refresh, so a reading
// a minute old is as good as a fresh one and costs the datastore nothing.
type cachedStatsProvider struct {
	inner    StatsProvider
	interval time.Duration
	now      func() time.Time

	mu      sync.Mutex
	last    *apimodel.Stats
	readAt  time.Time
	hasRead bool
}

func newCachedStatsProvider(inner StatsProvider, interval time.Duration, now func() time.Time) *cachedStatsProvider {
	return &cachedStatsProvider{inner: inner, interval: interval, now: now}
}

func (c *cachedStatsProvider) Stats() (*apimodel.Stats, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.hasRead && c.now().Sub(c.readAt) <= c.interval {
		return c.last, nil
	}
	stats, err := c.inner.Stats()
	if err != nil {
		return nil, err
	}
	c.last, c.readAt, c.hasRead = stats, c.now(), true
	return stats, nil
}
