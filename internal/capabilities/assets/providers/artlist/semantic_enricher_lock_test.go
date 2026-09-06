package artlist

import (
	"testing"
	"time"
)

// TestAcquireEnrichFolderLock_SameFolderSerialises pins the mutual-exclusion
// contract: two Enrich RMW cycles for the SAME folder must never overlap.
func TestAcquireEnrichFolderLock_SameFolderSerialises(t *testing.T) {
	release1 := acquireEnrichFolderLock("folder-a")
	secondAcquired := make(chan struct{})
	go func() {
		release2 := acquireEnrichFolderLock("folder-a")
		close(secondAcquired)
		release2()
	}()

	select {
	case <-secondAcquired:
		t.Fatal("second lock for the same folder must block until the first is released")
	case <-time.After(50 * time.Millisecond):
		// Expected: still blocked.
	}

	release1()

	select {
	case <-secondAcquired:
		// Expected: unblocked after release.
	case <-time.After(2 * time.Second):
		t.Fatal("second lock for the same folder must be granted after the first release")
	}
}

// TestAcquireEnrichFolderLock_DifferentFoldersParallel pins the P1.3
// remediation contract: locks for DIFFERENT folders must not serialise each
// other (a slow Drive round-trip for one folder must not block another).
func TestAcquireEnrichFolderLock_DifferentFoldersParallel(t *testing.T) {
	releaseA := acquireEnrichFolderLock("folder-a")
	otherAcquired := make(chan struct{})
	go func() {
		releaseB := acquireEnrichFolderLock("folder-b")
		close(otherAcquired)
		releaseB()
	}()

	select {
	case <-otherAcquired:
		// Expected: folder-b is independent of folder-a.
	case <-time.After(2 * time.Second):
		t.Fatal("locks for different folders must not serialise each other")
	}

	releaseA()
}
