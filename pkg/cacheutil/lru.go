// Package cacheutil provides small, dependency-free cache primitives shared
// across capabilities. Leaf package: no internal imports.
//
// LRU is the canonical bounded cache for process-lifetime registries that
// previously relied on unbounded sync.Map + full-sweep janitors: capacity is
// fixed at construction, eviction is amortized O(1), and there is no
// background goroutine to leak or to destroy warm entries wholesale.
package cacheutil

import (
	"container/list"
	"sync"
)

// LRU is a bounded, concurrency-safe least-recently-used cache keyed by
// string. A zero-value LRU is not usable; construct with NewLRU.
type LRU struct {
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
	return &LRU{
		max:  max,
		ll:   list.New(),
		item: make(map[string]*list.Element, max),
	}
}

// Get returns the value for key, marking it most-recently-used. The bool
// reports presence; a cached nil value is indistinguishable from a miss by
// design (callers must not store nil).
func (c *LRU) Get(key string) (any, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	element, ok := c.item[key]
	if !ok {
		return nil, false
	}
	c.ll.MoveToFront(element)
	return element.Value.(*lruEntry).value, true
}

// Put stores value under key, evicting the least-recently-used entry when
// the cache is at capacity. Re-putting an existing key refreshes its
// recency without changing the entry count.
func (c *LRU) Put(key string, value any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if element, ok := c.item[key]; ok {
		element.Value.(*lruEntry).value = value
		c.ll.MoveToFront(element)
		return
	}
	element := c.ll.PushFront(&lruEntry{key: key, value: value})
	c.item[key] = element
	if c.ll.Len() > c.max {
		oldest := c.ll.Back()
		if oldest != nil {
			c.ll.Remove(oldest)
			delete(c.item, oldest.Value.(*lruEntry).key)
		}
	}
}

// Len reports the number of entries currently held.
func (c *LRU) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ll.Len()
}
