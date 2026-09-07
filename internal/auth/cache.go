// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package auth

import (
	"sync"
	"time"
)

const defaultReapInterval = 60 * time.Second

// Verdict is a cached auth validation result: whether the request is
// allowed, and the subject attribution that goes with that answer.
type Verdict struct {
	Valid       bool
	Subject     string
	SubjectName string
}

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
	verdict   Verdict
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

// Get returns the cached verdict for the given key.
// Returns (verdict, true) on cache hit, (Verdict{}, false) on miss or expiry.
func (c *AuthCache) Get(key string) (Verdict, bool) {
	c.mu.RLock()
	entry, ok := c.entries[key]
	c.mu.RUnlock()

	if !ok || time.Now().After(entry.expiresAt) {
		return Verdict{}, false
	}
	return entry.verdict, true
}

// Set stores a verdict with the given TTL.
func (c *AuthCache) Set(key string, v Verdict, ttl time.Duration) {
	c.mu.Lock()
	c.entries[key] = &cacheEntry{
		verdict:   v,
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
