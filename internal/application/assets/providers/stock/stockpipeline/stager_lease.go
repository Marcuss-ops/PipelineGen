package stockpipeline

import (
	"path/filepath"
	"sync"
)

type sharedSourceLease struct {
	mu       sync.Mutex
	path     string
	refCount int
	released bool
}

func (s *StockStager) acquireSharedLease(cacheKey, leaderPath, callLocalPath string) {
	const maxAttempts = 4
	for attempt := 0; attempt < maxAttempts; attempt++ {
		leaseI, _ := s.sharedRefs.LoadOrStore(cacheKey, &sharedSourceLease{})
		lease := leaseI.(*sharedSourceLease)
		lease.mu.Lock()
		if lease.released {
			lease.mu.Unlock()
			s.sharedRefs.Delete(cacheKey)
			continue
		}
		lease.path = leaderPath
		lease.refCount++
		lease.mu.Unlock()
		s.assetLeases.Store(callLocalPath, cacheKey)
		return
	}
	s.sharedRefs.Delete(cacheKey)
	leaseI, _ := s.sharedRefs.LoadOrStore(cacheKey, &sharedSourceLease{})
	lease := leaseI.(*sharedSourceLease)
	lease.mu.Lock()
	lease.path = leaderPath
	lease.refCount++
	lease.mu.Unlock()
	s.assetLeases.Store(callLocalPath, cacheKey)
}

func (s *StockStager) releaseSharedLease(cacheKey string) error {
	val, ok := s.sharedRefs.Load(cacheKey)
	if !ok {
		return nil
	}
	lease := val.(*sharedSourceLease)
	lease.mu.Lock()
	defer lease.mu.Unlock()
	if lease.released {
		return nil
	}
	if lease.refCount > 0 {
		lease.refCount--
	}
	if lease.refCount == 0 {
		lease.released = true
		var unlinkErr error
		if lease.path != "" {
			fs, fsErr := s.fs()
			if fsErr == nil {
				dir := filepath.Dir(lease.path)
				if dir != "" && dir != "." && dir != "/" {
					unlinkErr = fs.RemoveAll(dir)
				}
			}
		}
		s.sharedRefs.Delete(cacheKey)
		return unlinkErr
	}
	return nil
}

func (s *StockStager) isLeaseLeader(leaseKey, localPath string) bool {
	val, ok := s.sharedRefs.Load(leaseKey)
	if !ok {
		return false
	}
	lease := val.(*sharedSourceLease)
	lease.mu.Lock()
	defer lease.mu.Unlock()
	return !lease.released && lease.path == localPath
}
