// Package voiceover — finalizer_stubs_test.go (P0.4 Fase 3a, July 2026).
//
// Shared test doubles for the unified VoiceoverFinalizer and canonical
// per-item pipeline tests. Uses in-memory SQLite for real tx lifecycle.
package voiceover

import (
	"context"
	"database/sql"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"


	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/voiceover/service/persistence"
)

// ─────────────────────────────────────────────────────────────────────
// stubFinalizer — records Finalize invocations, returns canned results.
// ─────────────────────────────────────────────────────────────────────

type stubFinalizer struct {
	calls     []*FinalizeCommand
	cannedRes *FinalizeResult
	cannedErr error
}

func (s *stubFinalizer) Finalize(_ context.Context, _ *sql.Tx, cmd *FinalizeCommand) (*FinalizeResult, error) {
	s.calls = append(s.calls, cmd)
	return s.cannedRes, s.cannedErr
}

func (s *stubFinalizer) VerifyPostCommit(_ context.Context, _ string) error {
	return nil // nil-safe: no-op stub (tests that wire the real verifier use the real finalizer)
}

var _ VoiceoverFinalizer = (*stubFinalizer)(nil)

// ─────────────────────────────────────────────────────────────────────
// openFinalizerTestDB opens an in-memory SQLite database and creates
// the minimal voiceovers table needed by the real finalizer path.
// ─────────────────────────────────────────────────────────────────────

func openFinalizerTestDB(t *testing.T) *sql.DB {
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
// txRepo wraps *sql.DB so stubTxRepo-style tests can open real txns.
// ─────────────────────────────────────────────────────────────────────

type finalizerTestRepo struct {
	db *sql.DB
}

func (r *finalizerTestRepo) BeginTx(ctx context.Context) (*sql.Tx, error) {
	return r.db.BeginTx(ctx, nil)
}

func (r *finalizerTestRepo) InsertTx(ctx context.Context, tx *sql.Tx, rec *persistence.VoiceoverRecord) error {
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

func (r *finalizerTestRepo) DeleteByIDTx(ctx context.Context, tx *sql.Tx, id string) error {
	_, err := tx.ExecContext(ctx, `DELETE FROM voiceovers WHERE id = ?`, id)
	return err
}

func (r *finalizerTestRepo) PreReadByID(_ context.Context, _ string) (*persistence.VoiceoverRecord, error) {
	return nil, nil
}

func (r *finalizerTestRepo) CountByDriveFileIDTx(_ context.Context, _ *sql.Tx, _ string, _ string) (string, int, error) {
	return "", 0, nil
}

func (r *finalizerTestRepo) FindByIdempotencyKeyTx(_ context.Context, _ *sql.Tx, idempotencyKey string) (string, error) {
	if idempotencyKey == "" {
		return "", sql.ErrNoRows
	}
	return "", sql.ErrNoRows
}

var _ persistence.Repository = (*finalizerTestRepo)(nil)

// ─────────────────────────────────────────────────────────────────────
// Canonical per-item pipeline test support
// ─────────────────────────────────────────────────────────────────────
//
// The former Service-level tests are covered by the canonical per-item pipeline
// (ProcessSegmentUseCase.Execute) — via the shared stub ports defined
// in process_voiceover_item_test.go (`stubProcessTTS`,
// `stubProcessDestResolver`, `stubProcessPublisher`) plus the local
// `stubFinalizer` (same package). Each migration preserves the original
// regression intent (delegation, dedupe-reuse, error propagation,
// nil-guard, post-commit verifier semantics) routed through the post-DRY
// path so the regression guard does not depend on the retired Service
// internals.
//
// godlike/06 SSOT: the canonical per-item pipeline IS
// `newProcessSegmentUseCase(...).Execute(...)`. The FASE 2 test group at
// the bottom of this file exercises the same behaviors against
// `newVoiceoverFinalizer(...).Finalize(...)` directly — the two
// surfaces are complementary (Execute wraps the tx + finalizer;
// Finalizer exercises the 6-step sequence without the tx envelope).
//
// godlike/07 NO-FAKE-AVAILABILITY: each subtest asserts a falsifiable
// invariant. t.Skip is gone; mute "pass" is impossible.

// TestFinalizeStage_NilFinalizerConstructorPanic verifies the
// godlike/07 fail-fast-at-construction contract: passing nil Finalizer
// to NewProcessSegmentUseCase MUST panic with the typed message
// `voiceover.NewProcessSegmentUseCase: Finalizer is required
// (ProcessSegmentDeps.Finalizer)`. Uses require.PanicsWithValue so a
// regression in the panic MESSAGE (e.g. a future refactor that rewords

func (s *stubOutboxEnqueuer) EnqueueIndexEvent(_ context.Context, _ *sql.Tx, _, _, _ string) error {
	return nil
}
func (s *stubOutboxEnqueuer) EnqueueCleanupEvent(_ context.Context, _ *sql.Tx, _, _, _ string, _ []string) error {
	return nil
}

var _ TxOutboxEnqueuer = (*stubOutboxEnqueuer)(nil)

type stubProjectionUpserter struct{}

func (s *stubProjectionUpserter) UpsertVoiceoverProjectionTx(_ context.Context, _ *sql.Tx, _ *VoiceoverProjectionInput) error {
	return nil
}

var _ LifecycleProjectionUpserter = (*stubProjectionUpserter)(nil)

// ─────────────────────────────────────────────────────────────────────
// FASE 2 Contract Tests — recording stubs for finalizer step verification
// ─────────────────────────────────────────────────────────────────────

// recordingFinalizerRepo wraps finalizerTestRepo with configurable
// CountByDriveFileIDTx / FindByIdempotencyKeyTx return values and
// recording of InsertTx + DeleteByIDTx calls. Allows hermetic testing
// of the dedupe gate and idempotency gate without a real SQLite DB.
type recordingFinalizerRepo struct {
	*finalizerTestRepo

	// Configurable return values for the dedupe gate (Step 1).
	countByDriveFileIDMatchedID string
	countByDriveFileIDCount     int
	countByDriveFileIDErr       error

	// Configurable return values for the idempotency gate (Step 0).
	findByIdempotencyKeyMatchedID string
	findByIdempotencyKeyErr       error

	// Recorded calls.
	inserted          []*persistence.VoiceoverRecord
	deleted           []string
	countByDriveCalls int
	findByIdemCalls   int
}

func newRecordingFinalizerRepo(t *testing.T, db *sql.DB) *recordingFinalizerRepo {
	return &recordingFinalizerRepo{
		finalizerTestRepo: &finalizerTestRepo{db: db},
	}
}

func (r *recordingFinalizerRepo) CountByDriveFileIDTx(ctx context.Context, tx *sql.Tx, id, driveFileID string) (string, int, error) {
	r.countByDriveCalls++
	return r.countByDriveFileIDMatchedID, r.countByDriveFileIDCount, r.countByDriveFileIDErr
}

func (r *recordingFinalizerRepo) FindByIdempotencyKeyTx(ctx context.Context, tx *sql.Tx, idempotencyKey string) (string, error) {
	r.findByIdemCalls++
	return r.findByIdempotencyKeyMatchedID, r.findByIdempotencyKeyErr
}

func (r *recordingFinalizerRepo) InsertTx(ctx context.Context, tx *sql.Tx, rec *persistence.VoiceoverRecord) error {
	r.inserted = append(r.inserted, rec)
	return nil
}

func (r *recordingFinalizerRepo) DeleteByIDTx(ctx context.Context, tx *sql.Tx, id string) error {
	r.deleted = append(r.deleted, id)
	return nil
}

var _ persistence.Repository = (*recordingFinalizerRepo)(nil)

// payloadRecordingOutbox records EnqueueIndexEvent + EnqueueCleanupEvent
// calls so FASE 2 tests can assert the exact payload fields.
type payloadRecordingOutbox struct {
	indexCalls   []outboxIndexCall
	cleanupCalls []outboxCleanupCall
}

type outboxIndexCall struct {
	assetID     string
	contentHash string
}

type outboxCleanupCall struct {
	voiceoverID    string
	oldDriveFileID string
	newDriveFileID string
	oldLocalPaths  []string
}

func (o *payloadRecordingOutbox) EnqueueIndexEvent(_ context.Context, _ *sql.Tx, assetID, _, contentHash string) error {
	o.indexCalls = append(o.indexCalls, outboxIndexCall{assetID: assetID, contentHash: contentHash})
	return nil
}

func (o *payloadRecordingOutbox) EnqueueCleanupEvent(_ context.Context, _ *sql.Tx, voiceoverID, oldDriveFileID, newDriveFileID string, oldLocalPaths []string) error {
	paths := make([]string, len(oldLocalPaths))
	copy(paths, oldLocalPaths)
	o.cleanupCalls = append(o.cleanupCalls, outboxCleanupCall{
		voiceoverID:    voiceoverID,
		oldDriveFileID: oldDriveFileID,
		newDriveFileID: newDriveFileID,
		oldLocalPaths:  paths,
	})
	return nil
}

var _ TxOutboxEnqueuer = (*payloadRecordingOutbox)(nil)

// payloadRecordingProjection records UpsertVoiceoverProjectionTx calls
// so FASE 2 tests can assert the projection input shape.
type payloadRecordingProjection struct {
	inputs []*VoiceoverProjectionInput
}

func (p *payloadRecordingProjection) UpsertVoiceoverProjectionTx(_ context.Context, _ *sql.Tx, input *VoiceoverProjectionInput) error {
	p.inputs = append(p.inputs, input)
	return nil
}

var _ LifecycleProjectionUpserter = (*payloadRecordingProjection)(nil)
