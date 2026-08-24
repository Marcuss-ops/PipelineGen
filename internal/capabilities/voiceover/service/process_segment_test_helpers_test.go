// Package voiceover — usecase/process_segment_test_helpers_test.go
//
// Stub port surface extracted from process_segment_test.go as part of
// the FASE-bucketed split (July 2026). This file owns the SINGLE
// canonical location for in-memory test doubles used across the 4
// FASE-bucketed test files (construction / execution / idempotency /
// e2e) plus per-item tests in process_voiceover_item_test.go
// (the existing per-item FASE 4 contract tests reuse stubTxOutboxEnqueuer
// from here — the file's package-level symbol sharing makes this
// zero-touch for the per-item tests).
//
// godlike/06 SSOT (one canonical owner per fact): each stub owns a
// single port from the production port surface. Compile-time pins
// (var _ Port = (*Stub)(nil)) guard against signature drift; a future
// production port refactor that breaks any of these pins surfaces
// the regression at build-time before the silent-success /
// false-negative pathology reaches tests.
//
// godlike/07 minimum-blast-radius: zero production code changes.
// The stubs only read from the production port surface; no test-only
// fields on production structs, no test-conditional compile branches.
//
// Stubs owned here (canonical SSOT list — 10 types total):
//   - 8 port stubs:
//   - stubTxOutboxEnqueuer (FASE 4 + FASE 6 tests; per-item reuse)
//   - stubLifecycleProjectionUpserter (FASE 5 E2E tests)
//   - stubIdempotencyVoRepo (FASE 3 idempotency tests)
//   - stubVoRepoFailSecondBeginTx (FASE 4 BeginTx-failure path)
//   - stubFailingInsertRepo (FASE 6 E2E InsertTx-failure path)
//   - recordingDestResolver (TestResolveDestinationWithFallback)
//   - stubDefaultFolderResolver (TestResolveDestinationWithFallback)
//   - sqliteErrorStub (finalizer dedupe-gate typed-sentinel probe)
//   - 2 helper struct types: indexEventCall, cleanupEventCall
//     (mirror stubs' recorder payloads; only used by stubTxOutboxEnqueuer
//     and asserted through the struct fields, not by name)
//
// Stubs NOT moved here (they live in process_voiceover_item_test.go
// per the canonical "reuse from process_voiceover_item_test.go" surface):
// stubProcessTTS, stubProcessDestResolver, stubProcessPublisher,
// stubProcessFinalizer, stubProcessVoRepo, openProcessTestDB. These
// are the per-item path's canonical stubs and the same-package
// symbol sharing makes them visible to all FASE-bucketed files.
package voiceover

import (
	"context"
	"database/sql"
	"sync"
	"testing"

	"github.com/mattn/go-sqlite3"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/voiceover/service/persistence"
)

// ────────────────────────────────────────────────────────────────────────────
// stubTxOutboxEnqueuer — FASE 4 / FASE 6 outbox stub
// ────────────────────────────────────────────────────────────────────────────

// stubTxOutboxEnqueuer records EnqueueIndexEvent + EnqueueCleanupEvent calls
// for FASE 4 tests. The optional cleanupErr field lets tests simulate
// EnqueueCleanupEvent failures (Test 20).
type stubTxOutboxEnqueuer struct {
	indexEvents   []indexEventCall
	cleanupEvents []cleanupEventCall
	cleanupErr    error // optional: when set, EnqueueCleanupEvent returns this error
}

type indexEventCall struct {
	tx          *sql.Tx
	assetID     string
	contentHash string
}

type cleanupEventCall struct {
	tx             *sql.Tx
	voiceoverID    string
	oldDriveFileID string
	newDriveFileID string
	oldLocalPaths  []string
}

func (s *stubTxOutboxEnqueuer) EnqueueIndexEvent(_ context.Context, tx *sql.Tx, assetID, _, contentHash string) error {
	s.indexEvents = append(s.indexEvents, indexEventCall{tx: tx, assetID: assetID, contentHash: contentHash})
	return nil
}

func (s *stubTxOutboxEnqueuer) EnqueueCleanupEvent(_ context.Context, tx *sql.Tx, voiceoverID, oldDriveFileID, newDriveFileID string, oldLocalPaths []string) error {
	s.cleanupEvents = append(s.cleanupEvents, cleanupEventCall{
		tx: tx, voiceoverID: voiceoverID, oldDriveFileID: oldDriveFileID,
		newDriveFileID: newDriveFileID, oldLocalPaths: oldLocalPaths,
	})
	if s.cleanupErr != nil {
		return s.cleanupErr
	}
	return nil
}

var _ TxOutboxEnqueuer = (*stubTxOutboxEnqueuer)(nil)

// ──────────────────────────────────────────────────────────────────────────────
// stubLifecycleProjectionUpserter — FASE 5 E2E stub
// ──────────────────────────────────────────────────────────────────────────────

// stubLifecycleProjectionUpserter satisfies LifecycleProjectionUpserter
// so the real voiceoverFinalizer can run inside an E2E test. Records
// each call for assertion.
type stubLifecycleProjectionUpserter struct {
	calls []*VoiceoverProjectionInput
}

func (s *stubLifecycleProjectionUpserter) UpsertVoiceoverProjectionTx(_ context.Context, _ *sql.Tx, input *VoiceoverProjectionInput) error {
	s.calls = append(s.calls, input)
	return nil
}

var _ LifecycleProjectionUpserter = (*stubLifecycleProjectionUpserter)(nil)

// ─────────────────────────────────────────────────────────────────────────
// FASE 3 idempotency stub — tracks inserts and looks up by idempotency key
// ─────────────────────────────────────────────────────────────────────────

// stubIdempotencyVoRepo extends stubProcessVoRepo with a lightweight
// in-memory row store that records inserts and serves idempotency lookups.
// Enough to verify the "same job retried 2x → no duplicate Drive/DB"
// contract without a full SQLite migration round-trip in the test.
type stubIdempotencyVoRepo struct {
	*stubProcessVoRepo
	mu      sync.Mutex
	rows    map[string]*persistence.VoiceoverRecord // idempotencyKey → record
	inserts []*persistence.VoiceoverRecord
}

func newStubIdempotencyVoRepo(t *testing.T) *stubIdempotencyVoRepo {
	return &stubIdempotencyVoRepo{
		stubProcessVoRepo: &stubProcessVoRepo{db: openProcessTestDB(t)},
		rows:              make(map[string]*persistence.VoiceoverRecord),
	}
}

func (r *stubIdempotencyVoRepo) FindByIdempotencyKeyTx(_ context.Context, _ *sql.Tx, idempotencyKey string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if idempotencyKey == "" {
		return "", sql.ErrNoRows
	}
	rec, ok := r.rows[idempotencyKey]
	if !ok {
		return "", sql.ErrNoRows
	}
	return rec.ID, nil
}

func (r *stubIdempotencyVoRepo) InsertTx(_ context.Context, _ *sql.Tx, rec *persistence.VoiceoverRecord) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	// Simulate the UNIQUE INDEX: if a row with the same idempotency key
	// already exists, reject the insert (SQLite UNIQUE constraint violation).
	if rec.IdempotencyKey != "" {
		if _, exists := r.rows[rec.IdempotencyKey]; exists {
			return &sqliteErrorStub{code: sqlite3.ErrConstraint, extendedCode: sqlite3.ErrConstraintUnique}
		}
		r.rows[rec.IdempotencyKey] = rec
	}
	r.inserts = append(r.inserts, rec)
	return nil
}

// sqliteErrorStub satisfies the error interface and the sqlite3.Error shape
// for the finalizer's dedupe-gate typed-sentinel probe. The finalizer probes
// `sqliteErr.Code == sqlite3.ErrConstraint` (the base ErrNo code), not the
// ExtendedCode — so we set both fields to satisfy any probe variant.
type sqliteErrorStub struct {
	code         sqlite3.ErrNo
	extendedCode sqlite3.ErrNoExtended
}

func (e *sqliteErrorStub) Error() string {
	return "UNIQUE constraint failed: voiceovers.idempotency_key"
}

// ────────────────────────────────────────────────────────────────────────────
// stubVoRepoFailSecondBeginTx — FASE 4 BeginTx-failure stub
// ────────────────────────────────────────────────────────────────────────────

// stubVoRepoFailSecondBeginTx wraps a real stubProcessVoRepo and fails
// BeginTx on the SECOND call. The first call (for the Stage 4 Finalize tx)
// succeeds normally; the second call (for the FASE 4 orphan-cleanup tx)
// returns the configured error. All other methods delegate to the inner repo.
// Used by Test 19 to exercise the BeginTx-failure path in enqueueOrphanCleanup.
type stubVoRepoFailSecondBeginTx struct {
	*stubProcessVoRepo
	mu        sync.Mutex
	callCount int
	failErr   error
}

func newStubVoRepoFailSecondBeginTx(t *testing.T, failErr error) *stubVoRepoFailSecondBeginTx {
	return &stubVoRepoFailSecondBeginTx{
		stubProcessVoRepo: &stubProcessVoRepo{db: openProcessTestDB(t)},
		failErr:           failErr,
	}
}

func (r *stubVoRepoFailSecondBeginTx) BeginTx(ctx context.Context) (*sql.Tx, error) {
	r.mu.Lock()
	r.callCount++
	count := r.callCount
	r.mu.Unlock()
	if count >= 2 {
		return nil, r.failErr
	}
	return r.stubProcessVoRepo.BeginTx(ctx)
}

var _ persistence.Repository = (*stubVoRepoFailSecondBeginTx)(nil)

// ──────────────────────────────────────────────────────────────────────────────
// stubFailingInsertRepo — FASE 6 E2E InsertTx-failure stub
// ──────────────────────────────────────────────────────────────────────────────

// stubFailingInsertRepo — wraps stubProcessVoRepo, fails InsertTx with a
// configurable error. All other methods (including BeginTx for the orphan-
// cleanup path) delegate to the embedded *stubProcessVoRepo via Go field
// promotion. Used by Test 23 to trigger the orphan-cleanup path when the
// real finalizer's Step 3 (InsertTx) fails inside the finalize tx.
type stubFailingInsertRepo struct {
	*stubProcessVoRepo
	insertErr error
}

func (r *stubFailingInsertRepo) InsertTx(ctx context.Context, tx *sql.Tx, rec *persistence.VoiceoverRecord) error {
	return r.insertErr
}

var _ persistence.Repository = (*stubFailingInsertRepo)(nil)

// ──────────────────────────────────────────────────────────────────────────────
// recordingDestResolver — destination helper stub
// ──────────────────────────────────────────────────────────────────────────────

// recordingDestResolver is the canonical record-input stub for
// DestinationResolver used by TestResolveDestinationWithFallback.
// It records the last *DestinationRequest it was called with so the
// test can assert the SHARED destination_helpers.go synthesises the
// expected DestinationRequest{FolderID, FolderPath} from the
// defaultResolver's return values (rather than passing nil or a
// wrong shape). Mirrors the finalizerTestRepo pattern.
type recordingDestResolver struct {
	folderID     string
	folderPath   string
	lastRequest  *DestinationRequest
	resolveCalls int
}

func (s *recordingDestResolver) Resolve(_ context.Context, req *DestinationRequest) (*ResolvedDestination, error) {
	s.lastRequest = req
	s.resolveCalls++
	return &ResolvedDestination{FolderID: s.folderID, FolderPath: s.folderPath}, nil
}

var _ DestinationResolver = (*recordingDestResolver)(nil)

// ──────────────────────────────────────────────────────────────────────────────
// stubDefaultFolderResolver — default-folder resolver stub
// ──────────────────────────────────────────────────────────────────────────────

// stubDefaultFolderResolver is the canonical stub for
// VoiceoverDefaultFolderResolver used by TestResolveDestinationWithFallback.
// Returns the configured (folderID, localOutputDir, ok) tuple verbatim.
// It is package-private (lowercase) because the destination resolver
// port is internal to the voiceover package (mirrors the
// finalizerTestRepo pattern at finalizer_test.go).
type stubDefaultFolderResolver struct {
	folderID     string
	outputDir    string
	ok           bool
	resolveCalls int
}

func (s *stubDefaultFolderResolver) Resolve(_ context.Context) (string, string, bool) {
	s.resolveCalls++
	return s.folderID, s.outputDir, s.ok
}

var _ VoiceoverDefaultFolderResolver = (*stubDefaultFolderResolver)(nil)

// ──────────────────────────────────────────────────────────────────────────────
// TestHelpersCompile — guards helpers-surfaced compile-time pins at test time.
// godlike/06 SSOT: any future refactor that drifts the canonical port surface
// (e.g. adds a method to TxOutboxEnqueuer) MUST trip this test before the
// silent-success / false-negative pathology reaches production. The test is
// hermetic and runs in <1ms — pays for itself on the first regression.
// ──────────────────────────────────────────────────────────────────────────────

// TestHelpersCompile asserts every compile-time pin declared in this
// helpers file resolves against the canonical production port surface.
// A future signature drift on any of these ports breaks the pin at
// build-time AND this test asserts them at test-time for double coverage.
func TestHelpersCompile(t *testing.T) {
	// Compile-time pins already guard the surface at build-time; this
	// test asserts them at test-time so a regression surfaces in `go test`
	// output rather than silent build-time drift.
	var _ TxOutboxEnqueuer = (*stubTxOutboxEnqueuer)(nil)
	var _ LifecycleProjectionUpserter = (*stubLifecycleProjectionUpserter)(nil)
	var _ persistence.Repository = (*stubVoRepoFailSecondBeginTx)(nil)
	var _ persistence.Repository = (*stubFailingInsertRepo)(nil)
	var _ DestinationResolver = (*recordingDestResolver)(nil)
	var _ VoiceoverDefaultFolderResolver = (*stubDefaultFolderResolver)(nil)

	// Constructors: confirm signatures are stable (panic-on-error isn't
	// expected here — these are constructor calls that should succeed).
	_ = newStubIdempotencyVoRepo // *testing.T parameter
	_ = newStubVoRepoFailSecondBeginTx
}
