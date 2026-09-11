package cacheutil

import "testing"

func TestLRUBoundsCapacityAndEvictsLeastRecent(t *testing.T) {
	cache := NewLRU(2)
	cache.Put("a", 1)
	cache.Put("b", 2)
	cache.Put("c", 3) // evicts "a" (least recently used)

	if _, ok := cache.Get("a"); ok {
		t.Fatal("expected \"a\" to be evicted at capacity")
	}
	if value, ok := cache.Get("b"); !ok || value != 2 {
		t.Fatalf("expected \"b\" present with value 2, got %v/%v", value, ok)
	}
	if value, ok := cache.Get("c"); !ok || value != 3 {
		t.Fatalf("expected \"c\" present with value 3, got %v/%v", value, ok)
	}
	if cache.Len() != 2 {
		t.Fatalf("expected len 2, got %d", cache.Len())
	}
}

func TestLRUGetRefreshesRecency(t *testing.T) {
	cache := NewLRU(2)
	cache.Put("a", 1)
	cache.Put("b", 2)
	// Touching "a" makes "b" the least-recently-used entry.
	if _, ok := cache.Get("a"); !ok {
		t.Fatal("expected \"a\" present")
	}
	cache.Put("c", 3)
	if _, ok := cache.Get("b"); ok {
		t.Fatal("expected \"b\" evicted: \"a\" was more recently used")
	}
	if _, ok := cache.Get("a"); !ok {
		t.Fatal("expected \"a\" retained")
	}
}

func TestLRUPutExistingKeyRefreshesWithoutEviction(t *testing.T) {
	cache := NewLRU(2)
	cache.Put("a", 1)
	cache.Put("b", 2)
	cache.Put("a", 10) // refresh, no eviction
	if cache.Len() != 2 {
		t.Fatalf("expected len 2 after refresh, got %d", cache.Len())
	}
	if value, ok := cache.Get("a"); !ok || value != 10 {
		t.Fatalf("expected refreshed value 10, got %v/%v", value, ok)
	}
	cache.Put("c", 3) // evicts "b" (a was refreshed)
	if _, ok := cache.Get("b"); ok {
		t.Fatal("expected \"b\" evicted")
	}
}

func TestLRUMinCapacityIsOne(t *testing.T) {
	cache := NewLRU(0)
	cache.Put("a", 1)
	cache.Put("b", 2)
	if cache.Len() != 1 {
		t.Fatalf("expected capacity clamped to 1, len=%d", cache.Len())
	}
	if _, ok := cache.Get("a"); ok {
		t.Fatal("expected \"a\" evicted")
	}
}

func TestLRUMissReturnsAbsent(t *testing.T) {
	cache := NewLRU(2)
	if _, ok := cache.Get("missing"); ok {
		t.Fatal("expected miss")
	}
}
