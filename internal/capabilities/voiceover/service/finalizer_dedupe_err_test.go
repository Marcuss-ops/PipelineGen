// Package voiceover — finalizer_dedupe_err_test.go (audit P0 #3, July 2026).
//
// Verifies that when the dedupe lookup (CountByDriveFileIDTx) returns
// an error — e.g. SQLite transient failure, lock timeout, schema
// mismatch — the finalizer:
//
//	(a) returns the error wrapped with a "dedupe lookup" marker so
//	    callers can distinguish a dedupe-gate fault from a downstream-
//	    write fault,
//	(b) does NOT call DeleteByIDTx (atomic-swap delete step),
//	(c) does NOT call InsertTx (atomic-swap insert step),
//	(d) does NOT call UpsertVoiceoverProjectionTx (media_assets row),
//	(e) does NOT call EnqueueIndexEvent / EnqueueCleanupEvent (outbox).
//
// This test closes the silent-success class flagged by the audit
// (P0 #3): pre-fix, the dedupe lookup error was swallowed (`_ :=`),
// and an `err == nil + count == 0` outcome could let the finalizer
// proceed with DeleteByIDTx + InsertTx + UpsertVoiceoverProjectionTx +
// EnqueueIndexEvent + EnqueueCleanupEvent even though the gate's
// semantics were never validated. A StatusCompleted outcome against
// an unverified dedupe gate is a torn-state risk.
package voiceover

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"


	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/voiceover/service/persistence"
)

// ─────────────────────────────────────────────────────────────────────
// Recording mocks — count calls; assert NONE of the downstream
// steps fired when CountByDriveFileIDTx errors.
// ─────────────────────────────────────────────────────────────────────

// recordingDedupeErrRepo records which Repository methods are invoked.
// The dedupe lookup is forced to return countErr so the finalizer
// must fail-closed at the dedupe gate. All other counters MUST
// stay 0 on the dedupe-error path.
type recordingDedupeErrRepo struct {
	countErr error

	countCalls  int
	deleteCalls int
	insertCalls int
}

// BeginTx is part of the VoiceoverRepository interface but is NEVER
// invoked by voiceoverFinalizer.Finalize because the finalizer
// receives a caller-owned *sql.Tx. We panic on call so any future
// drift is caught at test time, not at production runtime.
func (r *recordingDedupeErrRepo) BeginTx(_ context.Context) (*sql.Tx, error) {
	panic("recordingDedupeErrRepo.BeginTx: NOT expected (caller owns the tx; finalizer.Finalize receives it as a parameter)")
}

func (r *recordingDedupeErrRepo) InsertTx(_ context.Context, _ *sql.Tx, _ *persistence.VoiceoverRecord) error {
	r.insertCalls++
	return nil
}

func (r *recordingDedupeErrRepo) DeleteByIDTx(_ context.Context, _ *sql.Tx, _ string) error {
	r.deleteCalls++
	return nil
}

func (r *recordingDedupeErrRepo) PreReadByID(_ context.Context, _ string) (*persistence.VoiceoverRecord, error) {
	return nil, nil
}

func (r *recordingDedupeErrRepo) CountByDriveFileIDTx(_ context.Context, _ *sql.Tx, _, _ string) (string, int, error) {
	r.countCalls++
	return "", 0, r.countErr
}

func (r *recordingDedupeErrRepo) FindByIdempotencyKeyTx(_ context.Context, _ *sql.Tx, idempotencyKey string) (string, error) {
	if idempotencyKey == "" {
		return "", sql.ErrNoRows
	}
	return "", sql.ErrNoRows
}

var _ persistence.Repository = (*recordingDedupeErrRepo)(nil)

// recordingOutbox counts enqueue calls; both MUST be 0 on the
// dedupe-error path.
type recordingOutbox struct {
	indexCalls   int
	cleanupCalls int
}

func (r *recordingOutbox) EnqueueIndexEvent(_ context.Context, _ *sql.Tx, _, _, _ string) error {
	r.indexCalls++
	return nil
}

func (r *recordingOutbox) EnqueueCleanupEvent(_ context.Context, _ *sql.Tx, _, _, _ string, _ []string) error {
	r.cleanupCalls++
	return nil
}

var _ TxOutboxEnqueuer = (*recordingOutbox)(nil)

// recordingProjection counts UpsertVoiceoverProjectionTx calls;
// MUST be 0 on the dedupe-error path.
type recordingProjection struct {
	calls int
}

func (r *recordingProjection) UpsertVoiceoverProjectionTx(_ context.Context, _ *sql.Tx, _ *VoiceoverProjectionInput) error {
	r.calls++
	return nil
}

var _ LifecycleProjectionUpserter = (*recordingProjection)(nil)

// ─────────────────────────────────────────────────────────────────────
// P0 #3 audit closure test
// ─────────────────────────────────────────────────────────────────────

// TestFinalize_DedupeLookupErr_PropagatesAndStopsAllDownstreamWrites
// pins the audit-closure contract for P0 #3.
func TestFinalize_DedupeLookupErr_PropagatesAndStopsAllDownstreamWrites(t *testing.T) {
	sentinel := errors.New("sqlite transient: database is locked")
	repo := &recordingDedupeErrRepo{countErr: sentinel}
	outbox := &recordingOutbox{}
	projection := &recordingProjection{}

	f := newVoiceoverFinalizer(voiceoverFinalizerDeps{
		VoiceoverRepo:    repo,
		Outbox:           outbox,
		LifecycleService: projection,
		Logger:           zap.NewNop(),
	})

	// Use a real (rolled-back) tx via in-memory SQLite so the
	// finalizer's pre-flight nil-tx guard passes; we are
	// exercising the dedupe-gate path specifically, NOT the nil-tx
	// guard. The recordingDedupeErrRepo's BeginTx is a panic stub
	// because the finalizer never invokes BeginTx (caller-owned tx).
	db := openFinalizerTestDB(t)
	realTx, err := db.BeginTx(context.Background(), nil)
	require.NoError(t, err, "test setup: BeginTx")
	defer func() { _ = realTx.Rollback() }()

	// DriveFileID MUST be non-empty here so the dedupe gate
	// actually runs (the finalizer skips the entire block when
	// DriveFileID is empty). Audit scenario: an upload completed
	// (DriveFileID populated) but the dedupe SQLite query fails.
	res, err := f.Finalize(context.Background(), realTx, &FinalizeCommand{
		ID:            "vo-dedupe-err",
		RequestID:     "req-dedupe-err",
		TextHash:      "hash",
		Text:          "hello",
		Language:      "en",
		Voice:         "en_female",
		Filename:      "test.mp3",
		LocalPath:     "/tmp/test.mp3",
		DriveFileID:   "drive-audit",
		DriveLink:     "https://drive.google.com/file/d/drive-audit/view",
		DownloadLink:  "https://drive.google.com/uc?id=drive-audit",
		LegacyFileMD5: "abc123",
		FolderID:      "folder-1",
		FolderPath:    "/tmp/vo",
		ShouldSwap:    true,
	})

	// ── (a) Error propagation contract ──
	require.Error(t, err, "Finalize must return an error when dedupe lookup fails")
	require.Nil(t, res, "FinalizeResult must be nil on dedupe lookup failure — partial result is a torn-state risk")
	assert.Contains(t, err.Error(), "dedupe lookup",
		"error must mention dedupe lookup so callers can distinguish from downstream-write faults")
	assert.True(t, errors.Is(err, sentinel),
		"returned err must wrap the original sentinel via %%w (preserves errors.Is)")

	// ── (b)–(e) Silent-success closure: NO downstream writes when
	// the dedupe gate's semantics were never validated ──
	assert.Equal(t, 1, repo.countCalls,
		"CountByDriveFileIDTx must be invoked exactly once")
	assert.Equal(t, 0, repo.deleteCalls,
		"DeleteByIDTx must NOT be called when dedupe lookup failed")
	assert.Equal(t, 0, repo.insertCalls,
		"InsertTx must NOT be called when dedupe lookup failed")
	assert.Equal(t, 0, projection.calls,
		"UpsertVoiceoverProjectionTx must NOT be called when dedupe lookup failed")
	assert.Equal(t, 0, outbox.indexCalls,
		"EnqueueIndexEvent must NOT be called when dedupe lookup failed")
	assert.Equal(t, 0, outbox.cleanupCalls,
		"EnqueueCleanupEvent must NOT be called when dedupe lookup failed")
}
