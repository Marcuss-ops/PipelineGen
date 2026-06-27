// In-memory WorkerStore adapter for the admin cert-report handler
// (RW-PROD-001, BLOCKER fix after code-review, June 2026).
//
// This is the production-available WorkerStore impl — it keeps the
// most recent cert identity per worker_id in a map guarded by a
// sync.RWMutex. The composition root will eventually wire a
// SQLite-backed impl that persists Cert* fields in the worker_nodes
// table; until that migration lands this in-memory store keeps the
// admin endpoint alive end-to-end.
//
// Semantics:
//   - SetIdentity replaces any prior identity for the same worker_id.
//     Called from WorkersBrokerHandler.RegisterWorker after the
//     mTLS-bound session row is created.
//   - GetCurrentCertIdentity returns the latest stashed CertReport
//     for the supplied worker_id; ErrWorkerNotFound when missing.
//
// Concurrency: the outer map is locked with a RWMutex — multiple
// concurrent heartbeats don't block reads; the swap is atomic.
//
// Scope: in-memory only. On server restart all identities are lost.
// The audit trail moves to SQLite once the workernodes_repository is
// extended with Cert* columns (follow-up PR per Wave 4B follow-up).
package admin

import (
	"context"
	"sync"
)

// InMemoryWorkerStore satisfies WorkerStore with the simplest
// possible backing store. Production wiring will swap this for the
// SQLite-backed implementation once Cert* columns land in the
// worker_nodes table (RW-PROD-001 follow-up).
type InMemoryWorkerStore struct {
	mu         sync.RWMutex
	identities map[string]*CertReport
}

// NewInMemoryWorkerStore returns an empty store.
func NewInMemoryWorkerStore() *InMemoryWorkerStore {
	return &InMemoryWorkerStore{
		identities: make(map[string]*CertReport),
	}
}

// SetIdentity registers/updates the cert report for workerID.
// Compose root hands the pointer from the broker handler in here.
func (s *InMemoryWorkerStore) SetIdentity(workerID string, report *CertReport) {
	if workerID == "" || report == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.identities[workerID] = report
}

// Forget drops the record for the supplied workerID. Kept for the
// Drain phase (RW-PROD-012) when a worker terminates.
func (s *InMemoryWorkerStore) Forget(workerID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.identities, workerID)
}

// GetCurrentCertIdentity is the WorkerStore implementation.
// context.Context is unused but matches the interface so the SQLite
// adapter can later be drop-in without changing the handler.
func (s *InMemoryWorkerStore) GetCurrentCertIdentity(_ context.Context, workerID string) (*CertReport, error) {
	if workerID == "" {
		return nil, &ErrWorkerNotFound{WorkerID: workerID}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.identities[workerID]
	if !ok {
		return nil, &ErrWorkerNotFound{WorkerID: workerID}
	}
	// Defensive copy so callers (handlers, tests) cannot mutate the
	// store's record by accident — keeps the contract obvious.
	copy := *r
	return &copy, nil
}
