package stockpipeline

import (
	"path/filepath"
	"sync"
)

type sharedSourceLease struct {
	mu              sync.Mutex
	path            string // downloader-resolved shared source path
	ownerPath       string // leader caller's final LocalPath
	refCount        int
	released        bool
	removeOnRelease bool
}

// reserveSharedLeaseLocked reserves a reference while sharedRefsMu is held.
// The caller is responsible for taking that lifecycle lock.
func (s *StockStager) reserveSharedLeaseLocked(cacheKey, ownerPath string) (*sharedSourceLease, bool) {
	if value, ok := s.sharedRefs.Load(cacheKey); ok {
		lease := value.(*sharedSourceLease)
		lease.mu.Lock()
		if !lease.released {
			lease.refCount++
			lease.mu.Unlock()
			s.assetLeases.Store(ownerPath, cacheKey)
			return lease, false
		}
		lease.mu.Unlock()
		s.sharedRefs.Delete(cacheKey)
	}

	lease := &sharedSourceLease{ownerPath: ownerPath, refCount: 1}
	s.sharedRefs.Store(cacheKey, lease)
	s.assetLeases.Store(ownerPath, cacheKey)
	return lease, true
}

func (s *StockStager) reserveSharedLease(cacheKey, ownerPath string) (*sharedSourceLease, bool) {
	s.sharedRefsMu.Lock()
	defer s.sharedRefsMu.Unlock()
	return s.reserveSharedLeaseLocked(cacheKey, ownerPath)
}

func (s *StockStager) publishSharedLease(lease *sharedSourceLease, sourcePath string, removeOnRelease bool) {
	lease.mu.Lock()
	if lease.path == "" {
		lease.path = sourcePath
		lease.removeOnRelease = removeOnRelease
	}
	lease.mu.Unlock()
}

func (s *StockStager) releaseSharedLease(cacheKey string) error {
	s.sharedRefsMu.Lock()
	defer s.sharedRefsMu.Unlock()

	value, ok := s.sharedRefs.Load(cacheKey)
	if !ok {
		return nil
	}
	lease := value.(*sharedSourceLease)
	lease.mu.Lock()
	if lease.released {
		lease.mu.Unlock()
		return nil
	}
	if lease.refCount > 0 {
		lease.refCount--
	}
	if lease.refCount != 0 {
		lease.mu.Unlock()
		return nil
	}

	lease.released = true
	leasePath := lease.path
	removeOnRelease := lease.removeOnRelease
	lease.mu.Unlock()

	// Keep the lifecycle lock until the final source removal completes.
	// No new reservation can otherwise slip between refcount zero and
	// removal of the shared source directory.
	var removeErr error
	if removeOnRelease && leasePath != "" {
		fs, fsErr := s.fs()
		if fsErr != nil {
			removeErr = fsErr
		} else {
			dir := filepath.Dir(leasePath)
			if dir != "" && dir != "." && dir != "/" {
				removeErr = fs.RemoveAll(dir)
			}
		}
	}
	if removeErr != nil {
		// Keep one retryable reference and the lease in sharedRefs. Cleanup
		// restores the asset binding so a later retry can remove the same
		// directory instead of leaking it permanently.
		lease.mu.Lock()
		lease.released = false
		lease.refCount = 1
		lease.mu.Unlock()
		return removeErr
	}
	s.sharedRefs.Delete(cacheKey)
	return nil
}

func (s *StockStager) isLeaseLeader(leaseKey, localPath string) bool {
	s.sharedRefsMu.Lock()
	defer s.sharedRefsMu.Unlock()
	value, ok := s.sharedRefs.Load(leaseKey)
	if !ok {
		return false
	}
	lease := value.(*sharedSourceLease)
	lease.mu.Lock()
	defer lease.mu.Unlock()
	return !lease.released && lease.ownerPath == localPath
}
