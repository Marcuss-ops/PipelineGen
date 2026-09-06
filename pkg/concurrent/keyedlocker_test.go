package concurrent

import (
	"testing"
	"time"
)

// TestKeyedLocker_SameKeySerialises pins the mutual-exclusion contract:
// two Lock calls for the SAME key must never overlap.
func TestKeyedLocker_SameKeySerialises(t *testing.T) {
	k := NewKeyedLocker()
	release1 := k.Lock("key-a")
	secondAcquired := make(chan struct{})
	go func() {
		release2 := k.Lock("key-a")
		close(secondAcquired)
		release2()
	}()

	select {
	case <-secondAcquired:
		t.Fatal("second lock for the same key must block until the first is released")
	case <-time.After(50 * time.Millisecond):
		// Expected: still blocked.
	}

	release1()

	select {
	case <-secondAcquired:
		// Expected: unblocked after release.
	case <-time.After(2 * time.Second):
		t.Fatal("second lock for the same key must be granted after the first release")
	}
}

// TestKeyedLocker_DifferentKeysParallel pins the concurrency contract:
// different keys must never block each other.
func TestKeyedLocker_DifferentKeysParallel(t *testing.T) {
	k := NewKeyedLocker()
	releaseA := k.Lock("key-a")
	otherAcquired := make(chan struct{})
	go func() {
		releaseB := k.Lock("key-b")
		close(otherAcquired)
		releaseB()
	}()

	select {
	case <-otherAcquired:
		// Expected: key-b is independent of key-a.
	case <-time.After(2 * time.Second):
		t.Fatal("locks for different keys must not serialise each other")
	}

	releaseA()
}

// TestKeyedLocker_ReleaseRemovesEntry pins the reference-counting contract:
// the registry drops an entry when its last holder releases, so the map
// does not grow with the set of keys seen over the process lifetime.
func TestKeyedLocker_ReleaseRemovesEntry(t *testing.T) {
	k := NewKeyedLocker()
	release := k.Lock("transient-key")
	if len(k.locks) != 1 {
		t.Fatalf("expected 1 registry entry while held, got %d", len(k.locks))
	}
	release()
	if len(k.locks) != 0 {
		t.Fatalf("registry entry must be removed after the last release, got %d", len(k.locks))
	}

	// Re-locking after full release must work (fresh entry, uncontended).
	release2 := k.Lock("transient-key")
	release2()
	if len(k.locks) != 0 {
		t.Fatalf("registry must be empty after acquire+release cycle, got %d", len(k.locks))
	}
}
