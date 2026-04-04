// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package auth

import (
	"testing"
	"time"
)

func TestAuthCache_GetMiss(t *testing.T) {
	cache := NewAuthCache()

	_, found := cache.Get("nonexistent")
	if found {
		t.Fatal("expected cache miss for unknown key")
	}
}

func TestAuthCache_SetAndGet(t *testing.T) {
	cache := NewAuthCache()

	cache.Set("key1", true, time.Minute)

	valid, found := cache.Get("key1")
	if !found {
		t.Fatal("expected cache hit")
	}
	if !valid {
		t.Fatal("expected valid=true")
	}
}

func TestAuthCache_SetInvalidAndGet(t *testing.T) {
	cache := NewAuthCache()

	cache.Set("key1", false, time.Minute)

	valid, found := cache.Get("key1")
	if !found {
		t.Fatal("expected cache hit")
	}
	if valid {
		t.Fatal("expected valid=false")
	}
}

func TestAuthCache_Expiry(t *testing.T) {
	cache := NewAuthCache()

	cache.Set("key1", true, 10*time.Millisecond)

	time.Sleep(20 * time.Millisecond)

	_, found := cache.Get("key1")
	if found {
		t.Fatal("expected cache miss after TTL expiry")
	}
}

func TestAuthCache_OverwriteEntry(t *testing.T) {
	cache := NewAuthCache()

	cache.Set("key1", true, time.Minute)
	cache.Set("key1", false, time.Minute)

	valid, found := cache.Get("key1")
	if !found {
		t.Fatal("expected cache hit")
	}
	if valid {
		t.Fatal("expected valid=false after overwrite")
	}
}

func TestAuthCache_IndependentKeys(t *testing.T) {
	cache := NewAuthCache()

	cache.Set("alice", true, time.Minute)
	cache.Set("bob", false, time.Minute)

	aliceValid, found := cache.Get("alice")
	if !found || !aliceValid {
		t.Fatal("expected alice=valid")
	}

	bobValid, found := cache.Get("bob")
	if !found || bobValid {
		t.Fatal("expected bob=invalid")
	}
}
