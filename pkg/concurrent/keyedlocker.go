// Package concurrent provides goroutine-safe concurrency primitives:
// errgroup-style parallel execution with cancellation, bounded map/reduce,
// panic-safe fire-and-forget goroutines, a typed Semaphore, and a
// reference-counted keyed locker.
package concurrent

import "sync"

// ── KeyedLocker — reference-counted per-key mutex registry ──────────────

// KeyedLocker serialises callers that share a key while leaving callers
// with different keys fully concurrent ("same folder → serialise,
// different folders → parallel").
//
// Lock(key) blocks until no other holder owns key, then returns a release
// func. The caller MUST invoke the release func exactly once when the
// guarded critical section completes (defer the call).
//
// The registry is reference-counted: entries are removed when the last
// holder releases, so the map does not grow unboundedly with the set of
// keys seen over the process lifetime.
//
// Canonical shared primitive (godlike/06 one-owner-per-fact): per-key
// locking previously existed as private copies (artlist's
// acquireEnrichFolderLock registry); new callers should use KeyedLocker
// instead of re-rolling the registry.
type KeyedLocker struct {
	mu    sync.Mutex
	locks map[string]*keyedLockEntry
}

type keyedLockEntry struct {
	mu   sync.Mutex
	refs int
}

// NewKeyedLocker constructs an empty registry.
func NewKeyedLocker() *KeyedLocker {
	return &KeyedLocker{locks: make(map[string]*keyedLockEntry)}
}

// Lock acquires the exclusive lock for key and returns the release func.
// Concurrent Lock calls for the SAME key serialise; different keys never
// block each other.
func (k *KeyedLocker) Lock(key string) func() {
	k.mu.Lock()
	entry := k.locks[key]
	if entry == nil {
		entry = &keyedLockEntry{}
		k.locks[key] = entry
	}
	entry.refs++
	k.mu.Unlock()

	entry.mu.Lock()
	return func() {
		entry.mu.Unlock()
		k.mu.Lock()
		entry.refs--
		if entry.refs == 0 {
			delete(k.locks, key)
		}
		k.mu.Unlock()
	}
}
