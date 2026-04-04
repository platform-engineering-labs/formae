// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package auth

import (
	"sync"
	"time"
)

// AuthCache is a TTL-based cache for auth validation results.
// Keyed by a hash of the Authorization header to avoid repeated RPC calls
// for the same credentials.
type AuthCache struct {
	mu      sync.RWMutex
	entries map[string]*cacheEntry
}

type cacheEntry struct {
	valid     bool
	expiresAt time.Time
}

func NewAuthCache() *AuthCache {
	return &AuthCache{
		entries: make(map[string]*cacheEntry),
	}
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
