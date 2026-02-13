// Licensed to Andrew Kroh under one or more agreements.
// Andrew Kroh licenses this file to you under the Apache 2.0 License.
// See the LICENSE file in the project root for more information.

package cache

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/andrewkroh/traefik-github-auth/internal/validator"
)

// Compile-time check that *Cache satisfies validator.Cache.
var _ validator.Cache = (*Cache)(nil)

func TestCache_ImplementsInterface(t *testing.T) {
	// This is a compile-time check enforced by the var declaration above.
	// If *Cache does not satisfy validator.Cache, this file will not compile.
	c := New(time.Minute, 1000)
	defer c.Stop()

	var iface validator.Cache = c
	_ = iface
}

func TestCache_GetMiss(t *testing.T) {
	c := New(time.Minute, 1000)
	defer c.Stop()

	result, err, ok := c.Get("test-token-1")
	if ok {
		t.Fatal("expected cache miss on empty cache, got hit")
	}
	if err != nil {
		t.Fatalf("expected nil error on miss, got: %v", err)
	}
	if result.Login != "" {
		t.Fatalf("expected zero-value result, got Login=%q", result.Login)
	}
}

func TestCache_SetAndGet(t *testing.T) {
	c := New(time.Minute, 1000)
	defer c.Stop()

	expected := validator.ValidationResult{
		Login: "testuser",
		ID:    12345,
		Email: "test@example.com",
		Org:   "test-org",
		Teams: []string{"team-a", "team-b"},
	}

	c.Set("test-token-1", expected, nil)

	result, err, ok := c.Get("test-token-1")
	if !ok {
		t.Fatal("expected cache hit, got miss")
	}
	if err != nil {
		t.Fatalf("expected nil error, got: %v", err)
	}
	if result.Login != expected.Login {
		t.Fatalf("Login: got %q, want %q", result.Login, expected.Login)
	}
	if result.ID != expected.ID {
		t.Fatalf("ID: got %d, want %d", result.ID, expected.ID)
	}
	if result.Email != expected.Email {
		t.Fatalf("Email: got %q, want %q", result.Email, expected.Email)
	}
	if len(result.Teams) != len(expected.Teams) {
		t.Fatalf("Teams length: got %d, want %d", len(result.Teams), len(expected.Teams))
	}
	for i, team := range result.Teams {
		if team != expected.Teams[i] {
			t.Fatalf("Teams[%d]: got %q, want %q", i, team, expected.Teams[i])
		}
	}
}

func TestCache_NegativeEntry(t *testing.T) {
	c := New(time.Minute, 1000)
	defer c.Stop()

	cachedErr := errors.New("unauthorized")
	c.Set("bad-token", validator.ValidationResult{}, cachedErr)

	result, err, ok := c.Get("bad-token")
	if !ok {
		t.Fatal("expected cache hit for negative entry, got miss")
	}
	if err == nil {
		t.Fatal("expected non-nil error for negative cache entry")
	}
	if err.Error() != "unauthorized" {
		t.Fatalf("expected error 'unauthorized', got %q", err.Error())
	}
	if result.Login != "" {
		t.Fatalf("expected zero-value result for negative entry, got Login=%q", result.Login)
	}
}

func TestCache_Expiry(t *testing.T) {
	ttl := 50 * time.Millisecond
	c := New(ttl, 1000)
	defer c.Stop()

	c.Set("test-token-1", validator.ValidationResult{Login: "testuser"}, nil)

	// Immediately should be a hit.
	if _, _, ok := c.Get("test-token-1"); !ok {
		t.Fatal("expected cache hit immediately after Set")
	}

	// Wait for expiry.
	time.Sleep(ttl + 20*time.Millisecond)

	if _, _, ok := c.Get("test-token-1"); ok {
		t.Fatal("expected cache miss after TTL expiry")
	}
}

func TestCache_Delete(t *testing.T) {
	c := New(time.Minute, 1000)
	defer c.Stop()

	c.Set("test-token-1", validator.ValidationResult{Login: "testuser"}, nil)

	// Verify it was stored.
	if _, _, ok := c.Get("test-token-1"); !ok {
		t.Fatal("expected cache hit after Set")
	}

	c.Delete("test-token-1")

	if _, _, ok := c.Get("test-token-1"); ok {
		t.Fatal("expected cache miss after Delete")
	}

	if c.Len() != 0 {
		t.Fatalf("expected 0 entries after Delete, got %d", c.Len())
	}
}

func TestCache_ConcurrentAccess(t *testing.T) {
	c := New(time.Minute, 1000)
	defer c.Stop()

	const goroutines = 50
	const iterations = 100

	var wg sync.WaitGroup
	wg.Add(goroutines * 3) // Set, Get, Delete goroutines

	// Concurrent Set goroutines.
	for i := 0; i < goroutines; i++ {
		go func(id int) {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				token := "test-token-concurrent"
				c.Set(token, validator.ValidationResult{
					Login: "user",
					ID:    int64(id),
				}, nil)
			}
		}(i)
	}

	// Concurrent Get goroutines.
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				c.Get("test-token-concurrent")
			}
		}()
	}

	// Concurrent Delete goroutines.
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				c.Delete("test-token-concurrent")
			}
		}()
	}

	wg.Wait()
	// No race condition or panic means success.
}

func TestCache_Cleanup(t *testing.T) {
	ttl := 50 * time.Millisecond
	c := New(ttl, 1000)
	defer c.Stop()

	c.Set("test-token-1", validator.ValidationResult{Login: "user1"}, nil)
	c.Set("test-token-2", validator.ValidationResult{Login: "user2"}, nil)
	c.Set("test-token-3", validator.ValidationResult{Login: "user3"}, nil)

	if c.Len() != 3 {
		t.Fatalf("expected 3 entries, got %d", c.Len())
	}

	// Wait long enough for entries to expire and cleanup to run.
	// Cleanup interval is TTL/2 = 25ms. We wait for TTL + a few cleanup cycles.
	time.Sleep(ttl + 100*time.Millisecond)

	if n := c.Len(); n != 0 {
		t.Fatalf("expected 0 entries after cleanup, got %d", n)
	}
}

func TestCache_DifferentTokensDifferentKeys(t *testing.T) {
	c := New(time.Minute, 1000)
	defer c.Stop()

	result1 := validator.ValidationResult{Login: "user1", ID: 1}
	result2 := validator.ValidationResult{Login: "user2", ID: 2}

	c.Set("test-token-1", result1, nil)
	c.Set("test-token-2", result2, nil)

	got1, _, ok := c.Get("test-token-1")
	if !ok {
		t.Fatal("expected cache hit for test-token-1")
	}
	if got1.Login != "user1" {
		t.Fatalf("test-token-1: got Login=%q, want %q", got1.Login, "user1")
	}

	got2, _, ok := c.Get("test-token-2")
	if !ok {
		t.Fatal("expected cache hit for test-token-2")
	}
	if got2.Login != "user2" {
		t.Fatalf("test-token-2: got Login=%q, want %q", got2.Login, "user2")
	}

	if c.Len() != 2 {
		t.Fatalf("expected 2 entries, got %d", c.Len())
	}
}

func TestCache_SameTokenSameKey(t *testing.T) {
	c := New(time.Minute, 1000)
	defer c.Stop()

	expected := validator.ValidationResult{Login: "testuser", ID: 42}
	c.Set("test-token-1", expected, nil)

	// Multiple gets for the same token should return the same result.
	for i := 0; i < 10; i++ {
		result, _, ok := c.Get("test-token-1")
		if !ok {
			t.Fatalf("iteration %d: expected cache hit", i)
		}
		if result.Login != expected.Login {
			t.Fatalf("iteration %d: Login: got %q, want %q", i, result.Login, expected.Login)
		}
		if result.ID != expected.ID {
			t.Fatalf("iteration %d: ID: got %d, want %d", i, result.ID, expected.ID)
		}
	}
}

func TestCache_ZeroTTL(t *testing.T) {
	// With TTL=0 the cache is effectively disabled.
	c := New(0, 1000)
	defer c.Stop()

	c.Set("test-token-1", validator.ValidationResult{Login: "testuser"}, nil)

	// Get should always return false when TTL is 0.
	if _, _, ok := c.Get("test-token-1"); ok {
		t.Fatal("expected cache miss when TTL is 0 (cache disabled)")
	}

	// Len should be 0 since Set is a no-op with TTL=0.
	if c.Len() != 0 {
		t.Fatalf("expected 0 entries when TTL is 0, got %d", c.Len())
	}
}

func TestCache_Len(t *testing.T) {
	c := New(time.Minute, 1000)
	defer c.Stop()

	if c.Len() != 0 {
		t.Fatalf("expected 0 entries on new cache, got %d", c.Len())
	}

	c.Set("test-token-1", validator.ValidationResult{Login: "user1"}, nil)
	if c.Len() != 1 {
		t.Fatalf("expected 1 entry, got %d", c.Len())
	}

	c.Set("test-token-2", validator.ValidationResult{Login: "user2"}, nil)
	if c.Len() != 2 {
		t.Fatalf("expected 2 entries, got %d", c.Len())
	}

	// Overwriting an existing entry should not change the count.
	c.Set("test-token-1", validator.ValidationResult{Login: "user1-updated"}, nil)
	if c.Len() != 2 {
		t.Fatalf("expected 2 entries after overwrite, got %d", c.Len())
	}

	c.Delete("test-token-1")
	if c.Len() != 1 {
		t.Fatalf("expected 1 entry after delete, got %d", c.Len())
	}
}

func TestCache_SetOverwrite(t *testing.T) {
	c := New(time.Minute, 1000)
	defer c.Stop()

	c.Set("test-token-1", validator.ValidationResult{Login: "original"}, nil)
	c.Set("test-token-1", validator.ValidationResult{Login: "updated"}, nil)

	result, _, ok := c.Get("test-token-1")
	if !ok {
		t.Fatal("expected cache hit")
	}
	if result.Login != "updated" {
		t.Fatalf("expected Login=%q after overwrite, got %q", "updated", result.Login)
	}
}

func TestCache_DeleteNonexistent(t *testing.T) {
	c := New(time.Minute, 1000)
	defer c.Stop()

	// Deleting a non-existent key should not panic or error.
	c.Delete("nonexistent-token")

	if c.Len() != 0 {
		t.Fatalf("expected 0 entries, got %d", c.Len())
	}
}

func TestCache_StopIdempotent(t *testing.T) {
	c := New(time.Minute, 1000)

	// Calling Stop multiple times should not panic.
	c.Stop()
	c.Stop()
}

func TestCache_HashToken(t *testing.T) {
	// Verify that hashToken produces consistent, distinct results.
	h1 := hashToken("test-token-1")
	h2 := hashToken("test-token-2")
	h1Again := hashToken("test-token-1")

	if h1 != h1Again {
		t.Fatal("hashToken is not deterministic")
	}
	if h1 == h2 {
		t.Fatal("different tokens produced same hash")
	}
	// SHA-256 hex digest should be 64 characters.
	if len(h1) != 64 {
		t.Fatalf("expected hash length 64, got %d", len(h1))
	}
}

func TestCache_MaxSize_EvictsOldest(t *testing.T) {
	// Create a cache with maxSize=2.
	c := New(time.Minute, 2)
	defer c.Stop()

	c.Set("token-a", validator.ValidationResult{Login: "userA"}, nil)
	time.Sleep(time.Millisecond) // Ensure distinct expiry times.
	c.Set("token-b", validator.ValidationResult{Login: "userB"}, nil)

	if c.Len() != 2 {
		t.Fatalf("expected 2 entries, got %d", c.Len())
	}

	// Adding a third entry should evict token-a (earliest expiry).
	time.Sleep(time.Millisecond)
	c.Set("token-c", validator.ValidationResult{Login: "userC"}, nil)

	if c.Len() != 2 {
		t.Fatalf("expected 2 entries after eviction, got %d", c.Len())
	}

	// token-a should be evicted.
	if _, _, ok := c.Get("token-a"); ok {
		t.Fatal("expected token-a to be evicted")
	}

	// token-b and token-c should still be present.
	if _, _, ok := c.Get("token-b"); !ok {
		t.Fatal("expected token-b to still be cached")
	}
	if _, _, ok := c.Get("token-c"); !ok {
		t.Fatal("expected token-c to still be cached")
	}
}

func TestCache_MaxSize_OverwriteDoesNotEvict(t *testing.T) {
	// Overwriting an existing key should not trigger eviction.
	c := New(time.Minute, 2)
	defer c.Stop()

	c.Set("token-a", validator.ValidationResult{Login: "userA"}, nil)
	c.Set("token-b", validator.ValidationResult{Login: "userB"}, nil)

	// Overwrite token-a. Should NOT evict anything.
	c.Set("token-a", validator.ValidationResult{Login: "userA-updated"}, nil)

	if c.Len() != 2 {
		t.Fatalf("expected 2 entries, got %d", c.Len())
	}

	result, _, ok := c.Get("token-a")
	if !ok {
		t.Fatal("expected token-a to still be cached")
	}
	if result.Login != "userA-updated" {
		t.Fatalf("expected Login=%q, got %q", "userA-updated", result.Login)
	}

	if _, _, ok := c.Get("token-b"); !ok {
		t.Fatal("expected token-b to still be cached")
	}
}

func TestCache_MaxSize_One(t *testing.T) {
	c := New(time.Minute, 1)
	defer c.Stop()

	c.Set("token-a", validator.ValidationResult{Login: "userA"}, nil)
	if c.Len() != 1 {
		t.Fatalf("expected 1 entry, got %d", c.Len())
	}

	c.Set("token-b", validator.ValidationResult{Login: "userB"}, nil)
	if c.Len() != 1 {
		t.Fatalf("expected 1 entry after eviction, got %d", c.Len())
	}

	// token-a should be evicted, token-b should be present.
	if _, _, ok := c.Get("token-a"); ok {
		t.Fatal("expected token-a to be evicted")
	}
	if _, _, ok := c.Get("token-b"); !ok {
		t.Fatal("expected token-b to still be cached")
	}
}
