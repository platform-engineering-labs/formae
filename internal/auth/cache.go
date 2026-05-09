// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package auth

import (
	"sync"
	"time"
)

const defaultReapInterval = 60 * time.Second

// AuthCache is a TTL-based cache for auth validation results.
// Keyed by a hash of the Authorization header to avoid repeated RPC calls
// for the same credentials. A background reaper removes expired entries
// so the map doesn't grow unbounded on long-running agents.
type AuthCache struct {
	mu      sync.RWMutex
	entries map[string]*cacheEntry
	stopCh  chan struct{}
}

type cacheEntry struct {
	valid     bool
	expiresAt time.Time
}

func NewAuthCache() *AuthCache {
	c := &AuthCache{
		entries: make(map[string]*cacheEntry),
		stopCh:  make(chan struct{}),
	}
	go c.reapLoop(defaultReapInterval)
	return c
}

// Get returns the cached validation result for the given key.
// Returns (valid, true) on cache hit, (false, false) on miss or expiry.
func (c *AuthCache) Get(key string) (valid bool, found bool) {
	c.mu.RLock()
	entry, ok := c.entries[key]
	c.mu.RUnlock()

	if !ok || time.Now().After(entry.expiresAt) {
		return false, false
	}
	return entry.valid, true
}

// Set stores a validation result with the given TTL.
func (c *AuthCache) Set(key string, valid bool, ttl time.Duration) {
	c.mu.Lock()
	c.entries[key] = &cacheEntry{
		valid:     valid,
		expiresAt: time.Now().Add(ttl),
	}
	c.mu.Unlock()
}

// Stop halts the background reaper goroutine.
func (c *AuthCache) Stop() {
	close(c.stopCh)
}

// reapLoop periodically removes expired entries from the cache.
func (c *AuthCache) reapLoop(interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			c.reapExpired()
		case <-c.stopCh:
			return
		}
	}
}

func (c *AuthCache) reapExpired() {
	now := time.Now()
	c.mu.Lock()
	for k, entry := range c.entries {
		if now.After(entry.expiresAt) {
			delete(c.entries, k)
		}
	}
	c.mu.Unlock()
}
