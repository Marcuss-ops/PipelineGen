// Package cacheutil provides small, dependency-free cache primitives shared
// across capabilities. Leaf package: no internal imports.
//
// LRU is the canonical bounded cache for process-lifetime registries that
// previously relied on unbounded sync.Map + full-sweep janitors: capacity is
// fixed at construction, eviction is amortized O(1), and there is no
// background goroutine to leak or to destroy warm entries wholesale.
//
// The cache is SHARDED for read concurrency. `Get` promotes the entry to
// most-recently-used, so it is a mutating operation and cannot be served under
// a shared lock; with a single mutex every lookup — including the read-only
// hits — serialized all callers and bounced one cache line across cores.
// Sharding confines that serialization to 1/N of the keyspace.
//
// Sharding trades a global LRU order for a per-shard one: a hot key can be
// evicted while a colder key survives in another shard. That is the intended
// trade for a read-dominant registry. Small caches (below lruShardThreshold)
// keep a single shard, where the global order is actually meaningful.
package cacheutil

import (
	"container/list"
	"sync"
)

const (
	// lruShardCount is the number of independent mutex + list pairs.
	lruShardCount = 16
	// lruShardThreshold is the capacity at or above which the cache is
	// sharded. Below it a single shard is kept: with a handful of entries,
	// per-shard capacities would collapse to 1 and destroy LRU ordering
	// entirely, and contention is not the bottleneck at that size.
	lruShardThreshold = 64
)

// LRU is a bounded, concurrency-safe least-recently-used cache keyed by
// string. A zero-value LRU is not usable; construct with NewLRU.
type LRU struct {
	shards []*lruShard
}

// lruShard is one independently locked LRU partition.
type lruShard struct {
	mu   sync.Mutex
	max  int
	ll   *list.List // front = most recently used
	item map[string]*list.Element
}

type lruEntry struct {
	key   string
	value any
}

// NewLRU constructs a bounded LRU holding at most max entries. max is
// clamped to at least 1 so a misconfigured capacity can never disable
// caching silently (a zero-capacity cache would miss on every Get).
func NewLRU(max int) *LRU {
	if max < 1 {
		max = 1
	}
	count := 1
	if max >= lruShardThreshold {
		count = lruShardCount
	}
	shards := make([]*lruShard, 0, count)
	base := max / count
	rem := max % count
	for i := 0; i < count; i++ {
		shardMax := base
		if i < rem {
			shardMax++
		}
		if shardMax < 1 {
			shardMax = 1
		}
		shards = append(shards, &lruShard{
			max:  shardMax,
			ll:   list.New(),
			item: make(map[string]*list.Element, shardMax),
		})
	}
	return &LRU{shards: shards}
}

// shardFor picks the partition for key with an inline FNV-1a hash: no
// allocation, no dependency, and a mix good enough for a string keyspace.
func (c *LRU) shardFor(key string) *lruShard {
	const (
		offset32 = 2166136261
		prime32  = 16777619
	)
	h := uint32(offset32)
	for i := 0; i < len(key); i++ {
		h ^= uint32(key[i])
		h *= prime32
	}
	return c.shards[int(h%uint32(len(c.shards)))]
}

// Get returns the value for key, marking it most-recently-used. The bool
// reports presence; a cached nil value is indistinguishable from a miss by
// design (callers must not store nil).
func (c *LRU) Get(key string) (any, bool) {
	shard := c.shardFor(key)
	shard.mu.Lock()
	defer shard.mu.Unlock()
	element, ok := shard.item[key]
	if !ok {
		return nil, false
	}
	shard.ll.MoveToFront(element)
	return element.Value.(*lruEntry).value, true
}

// Put stores value under key, evicting the least-recently-used entry in the
// key's shard when that shard is at capacity. Re-putting an existing key
// refreshes its recency without changing the entry count.
func (c *LRU) Put(key string, value any) {
	shard := c.shardFor(key)
	shard.mu.Lock()
	defer shard.mu.Unlock()
	if element, ok := shard.item[key]; ok {
		element.Value.(*lruEntry).value = value
		shard.ll.MoveToFront(element)
		return
	}
	element := shard.ll.PushFront(&lruEntry{key: key, value: value})
	shard.item[key] = element
	if shard.ll.Len() > shard.max {
		oldest := shard.ll.Back()
		if oldest != nil {
			shard.ll.Remove(oldest)
			delete(shard.item, oldest.Value.(*lruEntry).key)
		}
	}
}

// Len reports the number of entries currently held across all shards.
func (c *LRU) Len() int {
	total := 0
	for _, shard := range c.shards {
		total += shard.len()
	}
	return total
}

func (s *lruShard) len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ll.Len()
}
