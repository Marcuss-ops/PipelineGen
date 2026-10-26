// Package adapters — stock_resolve_cache_test.go pins the canonical L1+L2
// resolve replay cache: L1 warm hits, durable L2 replay after a process
// restart (with L1 re-warm), malformed-payload rejection on write and
// miss-not-error on read, and L1-only degradation when no durable store is
// wired.
package adapters

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/pkg/cacheutil"
	"github.com/stretchr/testify/require"
)

// stubStockResolveL2 is a map-backed VidRushCachePort that pins the
// namespace and counts calls, with optional fault injection.
type stubStockResolveL2 struct {
	mu        sync.Mutex
	rows      map[string]map[string][]byte
	getCalls  int
	putCalls  int
	getErr    error
	putErr    error
	namespace string
}

func newStubStockResolveL2() *stubStockResolveL2 {
	return &stubStockResolveL2{rows: map[string]map[string][]byte{}}
}

func (s *stubStockResolveL2) Get(_ context.Context, namespace, key string) ([]byte, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.getCalls++
	s.namespace = namespace
	if s.getErr != nil {
		return nil, false, s.getErr
	}
	raw, ok := s.rows[namespace][key]
	return raw, ok, nil
}

func (s *stubStockResolveL2) Put(_ context.Context, namespace, key string, payload []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.putCalls++
	s.namespace = namespace
	if s.putErr != nil {
		return s.putErr
	}
	if s.rows[namespace] == nil {
		s.rows[namespace] = map[string][]byte{}
	}
	s.rows[namespace][key] = append([]byte(nil), payload...)
	return nil
}

func resetStockResolveL1ForTest() {
	stockResolveCacheL1 = cacheutil.NewLRU(stockResolveL1Capacity)
}

// TestStockResolveCache_L1HitThenL2RewarmAfterRestart pins the two-level
// contract: warm replays are served from the bounded L1, and a fresh process
// (empty L1) replays from the durable L2 and re-warms the L1.
func TestStockResolveCache_L1HitThenL2RewarmAfterRestart(t *testing.T) {
	resetStockResolveL1ForTest()
	l2 := newStubStockResolveL2()
	cache := NewStockResolveCache(l2)
	ctx := context.Background()
	payload := []byte(`{"candidates":[{"asset_id":"asset-1"}]}`)

	require.NoError(t, cache.Put(ctx, "key-1", payload))
	require.Equal(t, 1, l2.putCalls)
	require.Equal(t, stockResolveCacheNamespace, l2.namespace, "resolve entries live in the canonical namespace")

	got, hit, err := cache.Get(ctx, "key-1")
	require.NoError(t, err)
	require.True(t, hit)
	require.Equal(t, payload, got)
	require.Equal(t, 0, l2.getCalls, "a warm replay must be served from the L1")

	// Simulated restart: L1 gone, durable L2 still authoritative. A fresh
	// process builds a fresh cache instance over the re-created L1.
	resetStockResolveL1ForTest()
	cache = NewStockResolveCache(l2)
	got, hit, err = cache.Get(ctx, "key-1")
	require.NoError(t, err)
	require.True(t, hit)
	require.Equal(t, payload, got)
	require.Equal(t, 1, l2.getCalls, "a cold L1 must replay from the durable L2")

	// The L2 hit re-warmed the L1: the next read never touches the L2 again.
	_, hit, err = cache.Get(ctx, "key-1")
	require.NoError(t, err)
	require.True(t, hit)
	require.Equal(t, 1, l2.getCalls)
}

// TestStockResolveCache_MalformedPayloads pins the fail-closed cache
// hygiene: invalid payloads are never written durably, and a malformed
// durable row degrades to a miss instead of a hit or an error.
func TestStockResolveCache_MalformedPayloads(t *testing.T) {
	resetStockResolveL1ForTest()
	l2 := newStubStockResolveL2()
	cache := NewStockResolveCache(l2)
	ctx := context.Background()

	require.Error(t, cache.Put(ctx, "key-1", []byte("not-json")), "a malformed payload must never become a durable hit")
	require.Error(t, cache.Put(ctx, "key-1", []byte("null")), "empty/null payloads are rejected")
	require.Equal(t, 0, l2.putCalls)

	// A malformed row injected directly into the durable store is a miss.
	l2.rows[stockResolveCacheNamespace] = map[string][]byte{"key-2": []byte("not-json")}
	got, hit, err := cache.Get(ctx, "key-2")
	require.NoError(t, err, "a malformed durable row degrades to a miss, not an error")
	require.False(t, hit)
	require.Nil(t, got)

	// L2 read errors surface to the caller, which resolves live.
	l2.getErr = errors.New("l2 down")
	_, _, err = cache.Get(ctx, "key-1")
	require.Error(t, err)
}

// TestStockResolveCache_NilL2DegradesToL1Only pins the composition
// fallback: without a durable store the cache still works in-process and
// simply loses replay across restarts.
func TestStockResolveCache_NilL2DegradesToL1Only(t *testing.T) {
	resetStockResolveL1ForTest()
	cache := NewStockResolveCache(nil)
	ctx := context.Background()
	payload := []byte(`{"candidates":[{"asset_id":"asset-1"}]}`)

	require.NoError(t, cache.Put(ctx, "key-1", payload))
	got, hit, err := cache.Get(ctx, "key-1")
	require.NoError(t, err)
	require.True(t, hit)
	require.Equal(t, payload, got)

	resetStockResolveL1ForTest()
	cache = NewStockResolveCache(nil)
	_, hit, err = cache.Get(ctx, "key-1")
	require.NoError(t, err)
	require.False(t, hit, "without a durable L2 there is no replay across restarts")
}

// TestStockResolveCache_EmptyKeyIsANoOp pins the defensive boundary: an
// empty key can never store or serve anything.
func TestStockResolveCache_EmptyKeyIsANoOp(t *testing.T) {
	resetStockResolveL1ForTest()
	cache := NewStockResolveCache(newStubStockResolveL2())
	ctx := context.Background()

	require.NoError(t, cache.Put(ctx, "", []byte(`{"candidates":[]}`)))
	_, hit, err := cache.Get(ctx, "")
	require.NoError(t, err)
	require.False(t, hit)
}
