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
	defer cache.Stop()

	_, found := cache.Get("nonexistent")
	if found {
		t.Fatal("expected cache miss for unknown key")
	}
}

func TestAuthCache_SetAndGet(t *testing.T) {
	cache := NewAuthCache()
	defer cache.Stop()

	cache.Set("key1", Verdict{Valid: true, Subject: "s", SubjectName: "Sam"}, time.Minute)

	v, found := cache.Get("key1")
	if !found {
		t.Fatal("expected cache hit")
	}
	if v.Valid != true || v.Subject != "s" || v.SubjectName != "Sam" {
		t.Fatalf("expected Verdict{Valid:true, Subject:%q, SubjectName:%q} to round-trip, got %+v", "s", "Sam", v)
	}
}

func TestAuthCache_SetInvalidAndGet(t *testing.T) {
	cache := NewAuthCache()
	defer cache.Stop()

	cache.Set("key1", Verdict{Valid: false}, time.Minute)

	v, found := cache.Get("key1")
	if !found {
		t.Fatal("expected cache hit")
	}
	if v.Valid {
		t.Fatal("expected Valid=false")
	}
}

func TestAuthCache_Expiry(t *testing.T) {
	cache := NewAuthCache()
	defer cache.Stop()

	cache.Set("key1", Verdict{Valid: true}, 10*time.Millisecond)

	time.Sleep(20 * time.Millisecond)

	_, found := cache.Get("key1")
	if found {
		t.Fatal("expected cache miss after TTL expiry")
	}
}

func TestAuthCache_OverwriteEntry(t *testing.T) {
	cache := NewAuthCache()
	defer cache.Stop()

	cache.Set("key1", Verdict{Valid: true}, time.Minute)
	cache.Set("key1", Verdict{Valid: false}, time.Minute)

	v, found := cache.Get("key1")
	if !found {
		t.Fatal("expected cache hit")
	}
	if v.Valid {
		t.Fatal("expected Valid=false after overwrite")
	}
}

func TestAuthCache_IndependentKeys(t *testing.T) {
	cache := NewAuthCache()
	defer cache.Stop()

	cache.Set("alice", Verdict{Valid: true, Subject: "alice"}, time.Minute)
	cache.Set("bob", Verdict{Valid: false}, time.Minute)

	alice, found := cache.Get("alice")
	if !found || !alice.Valid || alice.Subject != "alice" {
		t.Fatalf("expected alice=valid with Subject=alice, got %+v (found=%v)", alice, found)
	}

	bob, found := cache.Get("bob")
	if !found || bob.Valid {
		t.Fatalf("expected bob=invalid, got %+v (found=%v)", bob, found)
	}
}

func TestAuthCache_ReapExpired(t *testing.T) {
	cache := NewAuthCache()
	defer cache.Stop()

	cache.Set("expired", Verdict{Valid: true}, 10*time.Millisecond)
	cache.Set("alive", Verdict{Valid: true}, time.Minute)

	time.Sleep(20 * time.Millisecond)

	cache.reapExpired()

	cache.mu.RLock()
	_, expiredExists := cache.entries["expired"]
	_, aliveExists := cache.entries["alive"]
	cache.mu.RUnlock()

	if expiredExists {
		t.Fatal("expected expired entry to be reaped")
	}
	if !aliveExists {
		t.Fatal("expected alive entry to still exist")
	}
}
