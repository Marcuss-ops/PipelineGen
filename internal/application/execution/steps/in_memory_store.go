// Package steps — in_memory_store.go (Stock Cutover §12-5, July 2026).
//
// InMemoryStore is the production-grade in-process implementation of
// the canonical steps.Store port (see store.go in this package).
//
// godlike/06 SSOT: this file lives in the canonical steps package
// because there is exactly one owner of the "did phase X
// run/complete/fail for job Y with input fingerprint Z" fact, and
// the canonical port is the seam. Adding a second in-memory
// implementation of Begin/Complete (e.g. in stockpipeline/steps.go)
// would re-fragment the fact across two implementations.
//
// godlike/07 typed-error contract: the InMemoryStore surfaces the
// package-level sentinel errors (ErrStepAlreadyCompleted,
// ErrStepNotFound, ErrInvalidStepKey) typed-exact so callers can
// `errors.Is` from any seam.
//
// Production default: when no SQLite-backed Store is wired in
// composition root, the orchestrator's NewOrchestrator invokes
// NewInMemoryStore as the canonical default. The in-memory store
// loses checkpoint state across process restarts — production
// composition roots SHOULD wire the persistent impl once it
// lands; this in-memory impl is correct for tests + dev runs and
// is a hermetic default for dev-only modes.
//
// Concurrency: every accessor guards mutations via sync.RWMutex.
// Multiple goroutines racing on the same (jobID, stepKey,
// fingerprint) row are serialized via the underlying mutex + a
// single-writer mutation model.
package steps

import (
	"context"
	"encoding/json"
	"sort"
	"sync"
	"time"
)

// Compile-time assertion: *inMemoryStore satisfies the canonical Store port.
var _ Store = (*inMemoryStore)(nil)

// inMemoryStore is the canonical in-process Store impl.
type inMemoryStore struct {
	mu       sync.RWMutex
	rows     map[StepKey]*StepState
	autoIncr int64
}

// NewInMemoryStore returns a fresh empty inMemoryStore wrapped as
// the canonical Store interface. Use as the production default when
// no persistent Store is wired (dev modes, tests, integrations).
//
// Returns the Store interface, not the concrete struct, so callers
// cannot accidentally touch private fields directly.
func NewInMemoryStore() Store {
	return &inMemoryStore{
		rows: make(map[StepKey]*StepState),
	}
}

func (s *inMemoryStore) keyOK(key StepKey) error {
	if err := key.Validated(); err != nil {
		return err
	}
	return nil
}

// MarkStarted is the entry-point: idempotent on re-call with the
// same (jobID, stepKey, input_fingerprint) triple (resets attempt /
// status to Pending). Returns ErrStepAlreadyCompleted if the prior
// row was Completed (godlike/07 fail-closed terminal-immutability).
func (s *inMemoryStore) MarkStarted(_ context.Context, key StepKey) error {
	if err := s.keyOK(key); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().UTC()
	existing, ok := s.rows[key]
	if ok {
		if existing.Status == StatusCompleted {
			return ErrStepAlreadyCompleted
		}
		// Idempotent re-call for non-terminal prior state.
		existing.Attempt++
		existing.Status = StatusPending
		existing.StartedAt = now
		existing.LastError = ""
		return nil
	}

	s.autoIncr++
	s.rows[key] = &StepState{
		ID:           s.autoIncr,
		JobID:        key.JobID,
		StepKey:      key.StepKey,
		Fingerprint:  key.InputFingerprint,
		Status:       StatusPending,
		Attempt:      1,
		StartedAt:    now,
		CompletedAt:  time.Time{},
		Result:       nil,
		ArtifactRefs: nil,
		LastError:    "",
	}
	return nil
}

// MarkCompleted transitions Pending|Running|Failed → Completed
// and stamps result + artifact_refs + completed_at.
//
// Idempotency: if the prior row was Completed AND the new
// (result, artifact_refs) is byte-for-byte equal to the prior
// values, returns nil. If the prior row was Completed AND the new
// values differ, returns ErrStepAlreadyCompleted (godlike/07
// fail-closed).
//
// ErrStepNotFound is returned when no row exists for the triple
// (Pre-MarkStarted completion is a programming error; the port
// surfaces it loudly rather than silently INSERTing a completed
// row).
func (s *inMemoryStore) MarkCompleted(_ context.Context, key StepKey, result, artifactRefs json.RawMessage) error {
	if err := s.keyOK(key); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	existing, ok := s.rows[key]
	if !ok {
		return ErrStepNotFound
	}
	if existing.Status == StatusCompleted {
		if bytesEqual(existing.Result, result) && bytesEqual(existing.ArtifactRefs, artifactRefs) {
			return nil
		}
		return ErrStepAlreadyCompleted
	}

	existing.Status = StatusCompleted
	existing.CompletedAt = time.Now().UTC()
	existing.Result = result
	existing.ArtifactRefs = artifactRefs
	return nil
}

// MarkFailed transitions Pending|Running → Failed and stamps
// LastError. If the prior row is Completed, returns
// ErrStepAlreadyCompleted (terminal-immutability). If no prior
// row exists, INSERTs a Failed row at attempt=1 — this matches
// the canonical contract where a fatal-error path before
// MarkStarted still produces an audit-trail row.
func (s *inMemoryStore) MarkFailed(_ context.Context, key StepKey, errMessage string) error {
	if err := s.keyOK(key); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().UTC()
	existing, ok := s.rows[key]
	if ok {
		if existing.Status == StatusCompleted {
			return ErrStepAlreadyCompleted
		}
		existing.Status = StatusFailed
		existing.CompletedAt = now
		existing.LastError = errMessage
		return nil
	}

	s.autoIncr++
	s.rows[key] = &StepState{
		ID:          s.autoIncr,
		JobID:       key.JobID,
		StepKey:     key.StepKey,
		Fingerprint: key.InputFingerprint,
		Status:      StatusFailed,
		Attempt:     1,
		StartedAt:   now,
		CompletedAt: now,
		LastError:   errMessage,
	}
	return nil
}

// FirstNonCompleted returns the row with the smallest StepKey
// (canonical "01_stage / 02_render" lexically-sorted naming rule
// per store.go doc-comment) whose latest row is NOT Completed.
//
// Returns (nil, nil) when no non-completed row exists for the
// jobID (all steps completed OR no rows for the jobID).
func (s *inMemoryStore) FirstNonCompleted(_ context.Context, jobID string) (*StepState, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Index rows by stepKey for this JobID, picking the latest row
	// (max ID) per stepKey as the canonical "current state".
	byStepKey := make(map[string]*StepState)
	for k, row := range s.rows {
		if k.JobID != jobID {
			continue
		}
		cur, exists := byStepKey[k.StepKey]
		if !exists || row.ID > cur.ID {
			byStepKey[k.StepKey] = row
		}
	}
	if len(byStepKey) == 0 {
		return nil, nil
	}

	// Lexically smallest stepKey whose row is NOT Completed.
	keys := make([]string, 0, len(byStepKey))
	for k := range byStepKey {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if byStepKey[k].Status != StatusCompleted {
			return byStepKey[k], nil
		}
	}
	return nil, nil
}

// ListByJob returns ALL rows for jobID ordered by StepKey ASC
// then id ASC, so callers can reconstruct the fingerprint-version
// audit log (not just the latest per stepKey). Returns (nil, nil)
// for unseen jobID.
func (s *inMemoryStore) ListByJob(_ context.Context, jobID string) ([]StepState, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make([]StepState, 0)
	for _, row := range s.rows {
		if row.JobID == jobID {
			out = append(out, *row)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].StepKey != out[j].StepKey {
			return out[i].StepKey < out[j].StepKey
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

// bytesEqual compares two json.RawMessage values for byte-equality.
// Two nil rawMessages are byte-equal; mixed nil/non-nil are not.
func bytesEqual(a, b json.RawMessage) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
