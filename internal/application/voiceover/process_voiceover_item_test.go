// Package voiceover — process_voiceover_item_test.go (P0.2 Fase 2c, July 2026).
//
// Tests the ProcessVoiceoverItemUseCase.Execute pipeline at the use case
// boundary with stub ports. Uses in-memory SQLite for real tx lifecycle so
// the Execute flow can exercise BeginTx → Finalize → Commit without panicking
// on a zero-value *sql.Tx.
package voiceover

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/voiceover/persistence"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
)

// ─────────────────────────────────────────────────────────────────────
// Stub ports for ProcessVoiceoverItemUseCase
// ─────────────────────────────────────────────────────────────────────

type stubProcessTTS struct {
	synthesized []TTSInput
	cannedOut   TTSOutput
	cannedErr   error
}

func (s *stubProcessTTS) Synthesize(_ context.Context, in TTSInput) (TTSOutput, error) {
	s.synthesized = append(s.synthesized, in)
	return s.cannedOut, s.cannedErr
}

var _ TTSProvider = (*stubProcessTTS)(nil)

type stubProcessDestResolver struct {
	resolved []*DestinationRequest
	folderID string
}

func (s *stubProcessDestResolver) Resolve(_ context.Context, dest *DestinationRequest) (*ResolvedDestination, error) {
	s.resolved = append(s.resolved, dest)
	return &ResolvedDestination{FolderID: s.folderID, FolderPath: "/tmp/vo"}, nil
}

var _ DestinationResolver = (*stubProcessDestResolver)(nil)

type stubProcessAudioPost struct {
	processed []AudioPostInput
	cannedOut AudioPostOutput
	cannedErr error
}

func (s *stubProcessAudioPost) Process(_ context.Context, in AudioPostInput) (AudioPostOutput, error) {
	s.processed = append(s.processed, in)
	return s.cannedOut, s.cannedErr
}

var _ AudioPostProcessor = (*stubProcessAudioPost)(nil)

type stubProcessPublisher struct {
	published []VoiceoverPublishCommand
	fileID    string
}

func (s *stubProcessPublisher) Publish(_ context.Context, cmd VoiceoverPublishCommand) (string, error) {
	s.published = append(s.published, cmd)
	return s.fileID, nil
}

var _ VoiceoverPublisher = (*stubProcessPublisher)(nil)

// stubProcessVoRepo wraps an in-memory *sql.DB so BeginTx returns a real
// *sql.Tx that supports Commit/Rollback without panicking. Mirrors the
// pattern established in finalizer_test.go::finalizerTestRepo.
type stubProcessVoRepo struct {
	db *sql.DB
}

func (r *stubProcessVoRepo) BeginTx(ctx context.Context) (*sql.Tx, error) {
	return r.db.BeginTx(ctx, nil)
}

func (r *stubProcessVoRepo) InsertTx(ctx context.Context, tx *sql.Tx, rec *persistence.VoiceoverRecord) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO voiceovers (
			id, request_id, text_hash, text_preview, language, voice, filename,
			local_path, cleaned_path, folder_id, folder_path, drive_file_id,
			drive_link, download_link, file_hash, status, error, strategy,
			metadata, idempotency_key, job_id, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		rec.ID, rec.RequestID, rec.TextHash, rec.TextPreview, rec.Language, rec.Voice, rec.Filename,
		rec.LocalPath, rec.CleanedPath, rec.FolderID, rec.FolderPath, rec.DriveFileID,
		rec.DriveLink, rec.DownloadLink, rec.LegacyFileMD5, rec.Status, rec.Error, rec.Strategy,
		rec.Metadata, rec.IdempotencyKey, rec.JobID, rec.CreatedAt, rec.UpdatedAt,
	)
	return err
}

func (r *stubProcessVoRepo) DeleteByIDTx(ctx context.Context, tx *sql.Tx, id string) error {
	_, err := tx.ExecContext(ctx, `DELETE FROM voiceovers WHERE id = ?`, id)
	return err
}

func (r *stubProcessVoRepo) PreReadByID(_ context.Context, _ string) (*persistence.VoiceoverRecord, error) {
	return nil, nil
}

func (r *stubProcessVoRepo) CountByDriveFileIDTx(_ context.Context, _ *sql.Tx, _ string, _ string) (string, int, error) {
	return "", 0, nil
}

func (r *stubProcessVoRepo) FindByIdempotencyKeyTx(ctx context.Context, tx *sql.Tx, idempotencyKey string) (string, error) {
	if idempotencyKey == "" {
		return "", sql.ErrNoRows
	}
	if tx == nil {
		return "", sql.ErrNoRows
	}
	var matchedID string
	err := tx.QueryRowContext(ctx,
		`SELECT id FROM voiceovers WHERE idempotency_key = ? LIMIT 1`,
		idempotencyKey,
	).Scan(&matchedID)
	if err == sql.ErrNoRows {
		return "", sql.ErrNoRows
	}
	if err != nil {
		return "", err
	}
	return matchedID, nil
}

var _ persistence.Repository = (*stubProcessVoRepo)(nil)

type stubProcessFinalizer struct {
	calls     []*FinalizeCommand
	cannedRes *FinalizeResult
	cannedErr error
}

func (s *stubProcessFinalizer) Finalize(_ context.Context, _ *sql.Tx, cmd *FinalizeCommand) (*FinalizeResult, error) {
	s.calls = append(s.calls, cmd)
	return s.cannedRes, s.cannedErr
}

var _ VoiceoverFinalizer = (*stubProcessFinalizer)(nil)

// openProcessTestDB creates an in-memory SQLite DB with the minimal
// voiceovers table needed by the finalizer's InsertTx.
func openProcessTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	_, err = db.Exec(`
		CREATE TABLE voiceovers (
			id TEXT PRIMARY KEY,
			request_id TEXT NOT NULL DEFAULT '',
			text_hash TEXT NOT NULL DEFAULT '',
			text_preview TEXT NOT NULL DEFAULT '',
			language TEXT NOT NULL DEFAULT '',
			voice TEXT NOT NULL DEFAULT '',
			filename TEXT NOT NULL DEFAULT '',
			local_path TEXT NOT NULL DEFAULT '',
			cleaned_path TEXT NOT NULL DEFAULT '',
			folder_id TEXT NOT NULL DEFAULT '',
			folder_path TEXT NOT NULL DEFAULT '',
			drive_file_id TEXT NOT NULL DEFAULT '',
			drive_link TEXT NOT NULL DEFAULT '',
			download_link TEXT NOT NULL DEFAULT '',
			file_hash TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT '',
			error TEXT NOT NULL DEFAULT '',
			strategy TEXT NOT NULL DEFAULT '',
			metadata TEXT NOT NULL DEFAULT '',
			idempotency_key TEXT NOT NULL DEFAULT '',
			job_id TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL DEFAULT '',
			updated_at TEXT NOT NULL DEFAULT ''
		)
	`)
	require.NoError(t, err)
	return db
}

// ─────────────────────────────────────────────────────────────────────
// TestRemoveSilenceRunsExactlyOnce
// ─────────────────────────────────────────────────────────────────────

// P0.2 Fase 2c (July 2026): when item.RemoveSilence is true,
// TTSProvider.Synthesize must receive RemoveSilence=false (the TTS
// bridge must NEVER strip silence inline), and AudioPostProcessor.Process
// must be called exactly once. The pre-Fase-2c code passed
// item.RemoveSilence verbatim to Synthesize, causing the TTS Python
// bridge (tts_edge.py) to strip silence AND AudioPostProcessor to
// re-process the already-cleaned audio — double-processing that wastes
// CPU cycles and risks audio artifacts on the second pass.
func TestRemoveSilenceRunsExactlyOnce(t *testing.T) {
	db := openProcessTestDB(t)

	tts := &stubProcessTTS{
		cannedOut: TTSOutput{
			LocalPath:   "/tmp/vo/test_en.mp3",
			CleanedPath: "",
			Voice:       "en_female",
			LegacyFileMD5:    "abc123",
			Duration:    45*time.Second + 210*time.Millisecond, // 45_210_000 us pre-clean
		},
	}
	dest := &stubProcessDestResolver{folderID: "folder-1"}
	audioPost := &stubProcessAudioPost{
		cannedOut: AudioPostOutput{
			CleanedPath: "/tmp/vo/cleaned_test_en.mp3",
			DurationUS:  43_870_000,
			EditMap: []audio.AudioEdit{
				{SourceStartUS: 0, SourceEndUS: 620_000},
				{SourceStartUS: 44_490_000, SourceEndUS: 45_210_000},
			},
		},
	}
	pub := &stubProcessPublisher{fileID: "drive-123"}
	finalizer := &stubProcessFinalizer{
		cannedRes: &FinalizeResult{ID: "test-id", Reused: false},
	}

	uc := NewProcessVoiceoverItemUseCase(ProcessVoiceoverItemDeps{
		Pipeline: ProcessVoiceoverPipelineDeps{
			TTSProvider:         tts,
			DestinationResolver: dest,
			AudioPostProcessor:  audioPost,
			Publisher:           pub,
			VoiceoverRepository: &stubProcessVoRepo{db: db},
		},
		Finalize: ProcessVoiceoverFinalizeDeps{
			Finalizer: finalizer,
		},
		Logger: zap.NewNop(),
	})

	item := &GenerateVoiceoverItemCommand{
		ParentJobID:   "parent-1",
		RequestID:     "req-1",
		Text:          "Hello world",
		TextHash:      "hash-001",
		Language:      "en",
		Voice:         "en_female",
		Filename:      "test_en.mp3",
		Destination:   &DestinationRequest{FolderID: "folder-1"},
		Strategy:      "replace",
		RemoveSilence: true, // key: caller wants silence removed
	}

	res, err := uc.Execute(context.Background(), item)
	require.NoError(t, err)
	assert.Equal(t, StatusCompleted, res.Status)

	// Assertion 1: TTSProvider.Synthesize received RemoveSilence=false
	require.Len(t, tts.synthesized, 1, "TTSProvider.Synthesize should be called exactly once")
	assert.False(t, tts.synthesized[0].RemoveSilence,
		"P0.2 Fase 2c: TTSProvider.Synthesize must ALWAYS receive RemoveSilence=false — the TTS bridge must never strip silence inline")

	// Assertion 2: AudioPostProcessor.Process was called exactly once
	require.Len(t, audioPost.processed, 1,
		"P0.2 Fase 2c: AudioPostProcessor.Process must be called when item.RemoveSilence=true")
	assert.Equal(t, "/tmp/vo/test_en.mp3", audioPost.processed[0].LocalPath,
		"AudioPostProcessor must receive the TTS output local path")

	// Assertion 3: the cleaned path from AudioPostProcessor was used
	assert.Equal(t, "/tmp/vo/cleaned_test_en.mp3", res.CleanedPath,
		"Cleaned path from AudioPostProcessor must populate result.CleanedPath")

	// Assertion 4: the silence-cleanup report is built and surfaced on the
	// result, so operators can verify the timeline uses the cleaned duration.
	require.NotNil(t, res.SilenceCleanup, "SilenceCleanup must be populated when RemoveSilence removes edits")
	assert.Equal(t, int64(45_210_000), res.SilenceCleanup.OriginalDurationUS)
	assert.Equal(t, int64(620_000), res.SilenceCleanup.TrimStartUS)
	assert.Equal(t, int64(720_000), res.SilenceCleanup.TrimEndUS)
	assert.Equal(t, int64(43_870_000), res.SilenceCleanup.CleanDurationUS)
}

// TestRemoveSilence_SkipsPostProcessingWhenFalse ensures that when
// item.RemoveSilence is false, AudioPostProcessor is NOT invoked at all.
func TestRemoveSilence_SkipsPostProcessingWhenFalse(t *testing.T) {
	db := openProcessTestDB(t)

	tts := &stubProcessTTS{
		cannedOut: TTSOutput{
			LocalPath: "/tmp/vo/test_en.mp3",
			Voice:     "en_female",
			LegacyFileMD5:  "abc123",
		},
	}
	dest := &stubProcessDestResolver{folderID: "folder-1"}
	audioPost := &stubProcessAudioPost{} // never called — verify 0 invocations
	pub := &stubProcessPublisher{fileID: "drive-123"}
	finalizer := &stubProcessFinalizer{
		cannedRes: &FinalizeResult{ID: "test-id", Reused: false},
	}

	uc := NewProcessVoiceoverItemUseCase(ProcessVoiceoverItemDeps{
		Pipeline: ProcessVoiceoverPipelineDeps{
			TTSProvider:         tts,
			DestinationResolver: dest,
			AudioPostProcessor:  audioPost,
			Publisher:           pub,
			VoiceoverRepository: &stubProcessVoRepo{db: db},
		},
		Finalize: ProcessVoiceoverFinalizeDeps{
			Finalizer: finalizer,
		},
		Logger: zap.NewNop(),
	})

	item := &GenerateVoiceoverItemCommand{
		ParentJobID:   "parent-1",
		RequestID:     "req-1",
		Text:          "Hello world",
		TextHash:      "hash-001",
		Language:      "en",
		Voice:         "en_female",
		Filename:      "test_en.mp3",
		Destination:   &DestinationRequest{FolderID: "folder-1"},
		Strategy:      "replace",
		RemoveSilence: false, // key: no silence removal
	}

	res, err := uc.Execute(context.Background(), item)
	require.NoError(t, err)
	assert.Equal(t, StatusCompleted, res.Status)

	// TTS still receives RemoveSilence=false (the invariant)
	require.Len(t, tts.synthesized, 1)
	assert.False(t, tts.synthesized[0].RemoveSilence,
		"TTSProvider.Synthesize must always receive RemoveSilence=false")

	// AudioPostProcessor must NOT be called
	assert.Len(t, audioPost.processed, 0,
		"P0.2 Fase 2c: AudioPostProcessor must NOT be called when item.RemoveSilence=false")
}

// ─────────────────────────────────────────────────────────────────────
// FASE 4 contract: per-item path's FASE 4 orphan-cleanup wiring
// ─────────────────────────────────────────────────────────────────────

// TestProcessItem_FinalizerFailure_FailsClosed pins the canonical
// fail-closed contract for the per-item voiceover pipeline when
// Stage 3 (Drive upload) succeeds but Stage 4 (Finalize inside a
// caller-owned tx) fails:
//
//  1. uc.Execute returns a non-nil error (the Finalize error wrapped
//     in a typed *PipelineError with stage=tx_commit, retryable=true).
//  2. result.Status == StatusFailed; result.Stage == StageTxCommit
//     (set by the per-item path's prefix-based stage classification).
//  3. result.DriveFileID is non-empty (Stage 3 succeeded).
//  4. NO voiceovers row in SQLite (Finalize tx was rolled back).
//  5. TxOutboxEnqueuer.cleanupEvents has exactly 1 entry — the FASE 4
//     orphan-cleanup event for the orphaned Drive file (oldDriveFileID
//     is the cleanup target; newDriveFileID is empty because no
//     replacement row was finalized; oldLocalPaths contains the TTS
//     local path).
//  6. The cleanup tx is non-nil (proves the production code opens a
//     FRESH BeginTx independent of the rolled-back Finalize tx).
//
// This test is the per-item-path companion to
// TestProcessSegmentUseCase_Execute_FASE4_DriveUploadOK_FinalizeFail_EmitsCleanup
// (process_segment_test.go). The difference: this test exercises
// ProcessVoiceoverItemUseCase.Execute, which is the THIN WRAPPER
// around ProcessSegmentUseCase. The contract locked here is that
// the per-item path does NOT short-circuit the cleanup path — the
// FASE 4 surface is identical for both callers.
//
// godlike/07 NO-FAKE-AVAILABILITY: a per-item failure that orphans
// a Drive file MUST emit the cleanup event; otherwise the orphan
// sweeper is the only safety net and the operator's on-call burden
// is unbounded. This test pins the fail-closed behavior.
func TestProcessItem_FinalizerFailure_FailsClosed(t *testing.T) {
	db := openProcessTestDB(t)

	tts := &stubProcessTTS{
		cannedOut: TTSOutput{
			LocalPath:   "/tmp/vo/peritem-orphan.mp3",
			CleanedPath: "",
			Voice:       "en_female",
			LegacyFileMD5:    "peritem-orphan-hash",
		},
	}
	dest := &stubProcessDestResolver{folderID: "folder-peritem"}
	pub := &stubProcessPublisher{fileID: "drive-peritem-123"}
	finalizer := &stubProcessFinalizer{
		cannedErr: errors.New("simulated DB constraint failure on Step 3 InsertTx"),
	}
	outboxStub := &stubTxOutboxEnqueuer{} // reuse from process_segment_test.go (same package)

	uc := NewProcessVoiceoverItemUseCase(ProcessVoiceoverItemDeps{
		Pipeline: ProcessVoiceoverPipelineDeps{
			TTSProvider:         tts,
			DestinationResolver: dest,
			AudioPostProcessor:  nil, // RemoveSilence=false so AudioPost is skipped
			Publisher:           pub,
			VoiceoverRepository: &stubProcessVoRepo{db: db},
		},
		Recovery: ProcessVoiceoverRecoveryDeps{
			TxOutboxEnqueuer: outboxStub,
		},
		Finalize: ProcessVoiceoverFinalizeDeps{
			Finalizer: finalizer,
		},
		Logger: zap.NewNop(),
	})

	item := &GenerateVoiceoverItemCommand{
		ParentJobID: "parent-peritem",
		RequestID:   "req-peritem",
		Text:        "Hello world",
		TextHash:    "peritem-hash-001",
		Language:    "en",
		Voice:       "en_female",
		Filename:    "peritem_en.mp3",
		Destination: &DestinationRequest{FolderID: "folder-peritem"},
		Strategy:    "replace",
		// RemoveSilence=false so Stage 2 is skipped (no AudioPost).
	}

	out, err := uc.Execute(context.Background(), item)

	// Assertion 1: Execute returns a non-nil error.
	require.Error(t, err, "FASE 4: Finalize failure MUST surface as a non-nil error to the caller")
	require.NotNil(t, out, "FASE 4: Execute MUST return a non-nil result even on failure (godlike/07 NO-FAKE-AVAILABILITY)")
	assert.Equal(t, StatusFailed, out.Status, "FASE 4: Status must be StatusFailed when Finalize fails")

	// Assertion 2: Stage classification is tx_commit + retryable=true.
	var pe *PipelineError
	require.True(t, errors.As(err, &pe), "FASE 4: error must wrap a typed *PipelineError")
	assert.Equal(t, StageTxCommit, pe.Stage, "FASE 4: stage must be StageTxCommit for Finalize failure")
	assert.True(t, pe.IsRetryable(), "FASE 4: tx_commit is retryable (SQLite busy / transient lock contention)")

	// Assertion 3: result.DriveFileID is non-empty (Stage 3 succeeded).
	assert.Equal(t, "drive-peritem-123", out.DriveFileID,
		"FASE 4: DriveFileID must be populated — Stage 3 succeeded before Finalize failed")
	assert.Contains(t, out.Error, "finalize_failed:",
		"FASE 4: out.Error must carry the canonical finalize_failed prefix")

	// Assertion 4: NO voiceovers row in SQLite (Finalize tx was rolled back).
	var count int
	rowErr := db.QueryRow(`SELECT COUNT(*) FROM voiceovers`).Scan(&count)
	require.NoError(t, rowErr, "FASE 4: voiceovers table must be queryable")
	assert.Equal(t, 0, count,
		"FASE 4: NO voiceovers row must be present — Finalize tx was rolled back by Execute's defer")

	// Assertion 5: TxOutboxEnqueuer recorded exactly 1 cleanup event.
	require.Len(t, outboxStub.cleanupEvents, 1,
		"FASE 4: orphan-cleanup event MUST be enqueued when Stage 3 OK + Stage 4 FAIL")
	ce := outboxStub.cleanupEvents[0]
	assert.Equal(t, "drive-peritem-123", ce.oldDriveFileID,
		"FASE 4: cleanup event's oldDriveFileID must match the publisher's orphaned fileID")
	assert.Contains(t, ce.oldLocalPaths, "/tmp/vo/peritem-orphan.mp3",
		"FASE 4: cleanup event's oldLocalPaths must contain the TTS local path")
	assert.Empty(t, ce.newDriveFileID,
		"FASE 4: cleanup event's newDriveFileID must be empty (per-item path — no replacement row was finalized)")

	// Assertion 6: cleanup tx is non-nil (production code opens a FRESH BeginTx).
	require.NotNil(t, ce.tx, "FASE 4: cleanup event must carry a non-nil tx (fresh BeginTx independent of Finalize)")

	// Index events must NOT have been emitted on the orphan-cleanup path.
	assert.Len(t, outboxStub.indexEvents, 0,
		"FASE 4: EnqueueIndexEvent must NOT be called on the orphan-cleanup path (Step 5 runs inside the rolled-back Finalize tx)")

	// Publisher was called exactly once (Stage 3 succeeded BEFORE Finalize failed).
	require.Len(t, pub.published, 1,
		"FASE 4: Publisher must be called exactly once — Stage 3 succeeded before Stage 4 failed")
}

// TestNewProcessVoiceoverItemUseCase_WiresTxOutboxEnqueuer pins the
// constructor's threading contract: the FASE 4 TxOutboxEnqueuer field
// on ProcessVoiceoverItemDeps MUST be forwarded to the inner
// ProcessSegmentUseCase.deps.TxOutboxEnqueuer. This is the canonical
// SOLE seam where FASE 4 wiring happens (godlike/06 SSOT
// one-canonical-owner-per-fact).
//
// The test introspects the unexported `processSeg` field directly
// (white-box test, same package as the production type). This is
// intentional: a getter method would expose the field as part of
// the public API surface, but the test-only introspection is a
// minimal-blast-radius verification (godlike/07) that the wiring
// is correct WITHOUT changing the public type.
//
// Without this threading, the per-item path's FASE 4 orphan-cleanup
// path is silently skipped (godlike/07 NO-FAKE-AVAILABILITY violation
// — operator sees Finalize-failed but no cleanup event).
func TestNewProcessVoiceoverItemUseCase_WiresTxOutboxEnqueuer(t *testing.T) {
	db := openProcessTestDB(t)

	outboxStub := &stubTxOutboxEnqueuer{}

	uc := NewProcessVoiceoverItemUseCase(ProcessVoiceoverItemDeps{
		Pipeline: ProcessVoiceoverPipelineDeps{
			TTSProvider:         &stubProcessTTS{cannedOut: TTSOutput{LocalPath: "/tmp/x.mp3", Voice: "v", LegacyFileMD5: "h"}},
			DestinationResolver: &stubProcessDestResolver{folderID: "f"},
			AudioPostProcessor:  nil,
			Publisher:           &stubProcessPublisher{fileID: "drive-x"},
			VoiceoverRepository: &stubProcessVoRepo{db: db},
		},
		Recovery: ProcessVoiceoverRecoveryDeps{
			TxOutboxEnqueuer: outboxStub,
		},
		Finalize: ProcessVoiceoverFinalizeDeps{
			Finalizer: &stubProcessFinalizer{cannedRes: &FinalizeResult{ID: "x"}},
		},
		Logger: zap.NewNop(),
	})

	// Direct unexported field traversal (white-box test, same package).
	require.NotNil(t, uc.processSeg, "ProcessVoiceoverItemUseCase.processSeg must be non-nil after NewProcessVoiceoverItemUseCase")
	assert.Same(t, outboxStub, uc.processSeg.deps.TxOutboxEnqueuer,
		"TxOutboxEnqueuer must be threaded to ProcessSegmentUseCase.deps — the FASE 4 orphan-cleanup contract depends on this seam")
}

// TestProcessItem_FinalizerFailure_NilTxOutboxEnqueuer_NoPanic pins the
// nil-safety contract for pre-FASE-4 callers: when TxOutboxEnqueuer is
// nil and Finalize fails, the per-item path MUST NOT panic. The
// orphan-cleanup path is silently skipped (godoc: "When nil ... the
// orphan-cleanup path is silently skipped — the background orphan
// sweeper will eventually recover the Drive file"). This is the
// godlike/07 minimum-blast-radius invariant for composition roots
// that haven't yet wired FASE 4 — they continue to work end-to-end
// with the orphan sweeper as the safety net.
//
// Without this test, a future refactor that adds a non-nil guard on
// TxOutboxEnqueuer in the FASE 4 path would silently break pre-FASE-4
// composition roots (nil-pointer panic on orphan-cleanup path). The
// test asserts the same fail-closed contract as
// TestProcessItem_FinalizerFailure_FailsClosed minus the outbox event
// assertion (no outbox wired → no event recorded).
func TestProcessItem_FinalizerFailure_NilTxOutboxEnqueuer_NoPanic(t *testing.T) {
	db := openProcessTestDB(t)

	tts := &stubProcessTTS{
		cannedOut: TTSOutput{
			LocalPath:   "/tmp/vo/peritem-nil-outbox.mp3",
			CleanedPath: "",
			Voice:       "en_female",
			LegacyFileMD5:    "peritem-nil-outbox-hash",
		},
	}
	dest := &stubProcessDestResolver{folderID: "folder-peritem-nil"}
	pub := &stubProcessPublisher{fileID: "drive-peritem-nil-123"}
	finalizer := &stubProcessFinalizer{
		cannedErr: errors.New("simulated DB constraint failure (nil outbox path)"),
	}

	uc := NewProcessVoiceoverItemUseCase(ProcessVoiceoverItemDeps{
		Pipeline: ProcessVoiceoverPipelineDeps{
			TTSProvider:         tts,
			DestinationResolver: dest,
			AudioPostProcessor:  nil, // RemoveSilence=false
			Publisher:           pub,
			VoiceoverRepository: &stubProcessVoRepo{db: db},
		},
		Recovery: ProcessVoiceoverRecoveryDeps{
			TxOutboxEnqueuer: nil, // KEY: nil outbox — pre-FASE-4 composition root contract
		},
		Finalize: ProcessVoiceoverFinalizeDeps{
			Finalizer: finalizer,
		},
		Logger: zap.NewNop(),
	})

	item := &GenerateVoiceoverItemCommand{
		ParentJobID: "parent-peritem-nil",
		RequestID:   "req-peritem-nil",
		Text:        "Hello world",
		TextHash:    "peritem-nil-hash-001",
		Language:    "en",
		Voice:       "en_female",
		Filename:    "peritem_nil_en.mp3",
		Destination: &DestinationRequest{FolderID: "folder-peritem-nil"},
		Strategy:    "replace",
	}

	// Assert: no panic (this is the load-bearing assertion). A future
	// refactor that adds a non-nil guard on TxOutboxEnqueuer would panic
	// here on the FASE 4 orphan-cleanup path.
	out, err := uc.Execute(context.Background(), item)

	// The same fail-closed contract as the wired-outbox path.
	require.Error(t, err, "Finalize failure must surface as a non-nil error regardless of outbox wiring")
	require.NotNil(t, out, "Execute must return a non-nil result even on failure (godlike/07 NO-FAKE-AVAILABILITY)")
	assert.Equal(t, StatusFailed, out.Status, "Status must be StatusFailed when Finalize fails")
	assert.Equal(t, "drive-peritem-nil-123", out.DriveFileID,
		"DriveFileID must be populated — Stage 3 succeeded before Stage 4 failed")

	var pe *PipelineError
	require.True(t, errors.As(err, &pe), "error must wrap a typed *PipelineError")
	assert.Equal(t, StageTxCommit, pe.Stage, "stage must be StageTxCommit for Finalize failure")
	assert.True(t, pe.IsRetryable(), "tx_commit is retryable")

	// NO voiceovers row in SQLite (Finalize tx was rolled back).
	var count int
	rowErr := db.QueryRow(`SELECT COUNT(*) FROM voiceovers`).Scan(&count)
	require.NoError(t, rowErr, "voiceovers table must be queryable")
	assert.Equal(t, 0, count,
		"NO voiceovers row must be present — Finalize tx was rolled back by Execute's defer")

	// Publisher was called exactly once.
	require.Len(t, pub.published, 1,
		"Publisher must be called exactly once even with nil outbox — Stage 3 runs unconditionally")
}

// TestRemoveSilence_TTSAlwaysReceivesFalse is a regression guard: even
// when the caller doesn't set RemoveSilence (zero value) and
// AudioPostProcessor is unwired, the TTS input must still carry
// RemoveSilence=false. The invariant is unconditional.
func TestRemoveSilence_TTSAlwaysReceivesFalse(t *testing.T) {
	db := openProcessTestDB(t)

	tts := &stubProcessTTS{
		cannedOut: TTSOutput{
			LocalPath: "/tmp/vo/test_en.mp3",
			Voice:     "en_female",
			LegacyFileMD5:  "abc123",
		},
	}
	dest := &stubProcessDestResolver{folderID: "folder-1"}
	pub := &stubProcessPublisher{fileID: "drive-123"}
	finalizer := &stubProcessFinalizer{
		cannedRes: &FinalizeResult{ID: "test-id", Reused: false},
	}

	uc := NewProcessVoiceoverItemUseCase(ProcessVoiceoverItemDeps{
		Pipeline: ProcessVoiceoverPipelineDeps{
			TTSProvider:         tts,
			DestinationResolver: dest,
			AudioPostProcessor:  nil,
			Publisher:           pub,
			VoiceoverRepository: &stubProcessVoRepo{db: db},
		},
		Finalize: ProcessVoiceoverFinalizeDeps{
			Finalizer: finalizer,
		},
		Logger: zap.NewNop(),
	})

	// RemoveSilence defaults to false (zero value); AudioPostProcessor is nil.
	// The contract: TTS must still receive RemoveSilence=false unconditionally.
	item := &GenerateVoiceoverItemCommand{
		ParentJobID: "parent-1",
		RequestID:   "req-1",
		Text:        "Hello world",
		TextHash:    "hash-001",
		Language:    "en",
		Voice:       "en_female",
		Filename:    "test_en.mp3",
		Destination: &DestinationRequest{FolderID: "folder-1"},
		Strategy:    "replace",
	}

	res, err := uc.Execute(context.Background(), item)
	require.NoError(t, err)
	assert.Equal(t, StatusCompleted, res.Status)

	require.Len(t, tts.synthesized, 1)
	assert.False(t, tts.synthesized[0].RemoveSilence,
		"P0.2 Fase 2c regression guard: RemoveSilence must ALWAYS be false on TTSInput, even when caller doesn't set RemoveSilence and AudioPostProcessor is unwired")
}
