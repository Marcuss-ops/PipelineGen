// Package voiceover — finalizer_test.go (P0.4 Fase 3a, July 2026).
//
// Tests the unified VoiceoverFinalizer delegation through both paths.
// Uses in-memory SQLite for real tx lifecycle (BeginTx/Commit/Rollback)
// so the test exercises the full finalizeStage flow without panicking
// on a zero-value *sql.Tx.
package voiceover

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
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

func (r *finalizerTestRepo) InsertTx(ctx context.Context, tx *sql.Tx, rec *VoiceoverRecord) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO voiceovers (
			id, request_id, text_hash, text_preview, language, voice, filename,
			local_path, cleaned_path, folder_id, folder_path, drive_file_id,
			drive_link, download_link, file_hash, status, error, strategy,
			metadata, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		rec.ID, rec.RequestID, rec.TextHash, rec.TextPreview, rec.Language, rec.Voice, rec.Filename,
		rec.LocalPath, rec.CleanedPath, rec.FolderID, rec.FolderPath, rec.DriveFileID,
		rec.DriveLink, rec.DownloadLink, rec.FileHash, rec.Status, rec.Error, rec.Strategy,
		rec.Metadata, rec.CreatedAt, rec.UpdatedAt,
	)
	return err
}

func (r *finalizerTestRepo) DeleteByIDTx(ctx context.Context, tx *sql.Tx, id string) error {
	_, err := tx.ExecContext(ctx, `DELETE FROM voiceovers WHERE id = ?`, id)
	return err
}

func (r *finalizerTestRepo) PreReadByID(_ context.Context, _ string) (*VoiceoverRecord, error) {
	return nil, nil
}

func (r *finalizerTestRepo) CountByDriveFileIDTx(_ context.Context, _ *sql.Tx, _ string, _ string) (string, int, error) {
	return "", 0, nil
}

var _ VoiceoverRepository = (*finalizerTestRepo)(nil)

// ─────────────────────────────────────────────────────────────────────
// Path B: Service.finalizeStage → Finalizer
// ─────────────────────────────────────────────────────────────────────

func TestFinalizeStage_DelegatesToFinalizer(t *testing.T) {
	db := openFinalizerTestDB(t)
	finalizer := &stubFinalizer{
		cannedRes: &FinalizeResult{ID: "test-id", Reused: false},
	}

	svc := &Service{
		finalizer:     finalizer,
		voiceoverRepo: &finalizerTestRepo{db: db},
		log:           zap.NewNop(),
	}

	item := BatchItem{
		ID:           "test-id",
		Language:     "en",
		Voice:        "en_female",
		Filename:     "test_en.mp3",
		LocalPath:    "/tmp/test_en.mp3",
		DriveFileID:  "drive-123",
		DriveLink:    "https://drive.google.com/file/d/drive-123/view",
		DownloadLink: "https://drive.google.com/uc?id=drive-123",
		FileHash:     "abc123",
		Status:       StatusUploaded,
	}
	req := &BatchRequest{
		Text:     "Hello world",
		Strategy: "replace",
	}
	dest := &ResolvedDestination{
		FolderID:   "folder-1",
		FolderPath: "/tmp/vo",
	}
	metaJSON := []byte(`{"key":"val"}`)

	result := svc.finalizeStage(
		context.Background(),
		item,
		"req-123",
		"hash-abc",
		"en",
		req,
		dest,
		metaJSON,
		true,          // shouldSwap
		"old-drive-1", // oldDriveFileID
		"/tmp/old.mp3",
		"/tmp/old_cleaned.mp3",
	)

	assert.Equal(t, StatusCompleted, result.Status, "finalizeStage should return StatusCompleted on success")
	assert.Equal(t, 1, len(finalizer.calls), "Finalizer.Finalize should be called exactly once")

	cmd := finalizer.calls[0]
	assert.Equal(t, "test-id", cmd.ID)
	assert.Equal(t, "req-123", cmd.RequestID)
	assert.Equal(t, "hash-abc", cmd.TextHash)
	assert.Equal(t, "Hello world", cmd.Text)
	assert.Equal(t, "en", cmd.Language)
	assert.Equal(t, "en_female", cmd.Voice)
	assert.Equal(t, "test_en.mp3", cmd.Filename)
	assert.Equal(t, "replace", cmd.Strategy)
	assert.Equal(t, []byte(`{"key":"val"}`), cmd.MetaJSON)
	assert.Equal(t, "/tmp/test_en.mp3", cmd.LocalPath)
	assert.Equal(t, "drive-123", cmd.DriveFileID)
	assert.Equal(t, "folder-1", cmd.FolderID)
	assert.Equal(t, "/tmp/vo", cmd.FolderPath)
	assert.True(t, cmd.ShouldSwap, "shouldSwap must be forwarded")
	assert.Equal(t, "old-drive-1", cmd.OldDriveFileID)
	assert.Equal(t, "/tmp/old.mp3", cmd.OldLocalPath)
	assert.Equal(t, "/tmp/old_cleaned.mp3", cmd.OldCleanedPath)
}

func TestFinalizeStage_DedupeReuse(t *testing.T) {
	db := openFinalizerTestDB(t)
	finalizer := &stubFinalizer{
		cannedRes: &FinalizeResult{ID: "matched-id", Reused: true},
	}

	svc := &Service{
		finalizer:     finalizer,
		voiceoverRepo: &finalizerTestRepo{db: db},
		log:           zap.NewNop(),
	}

	item := BatchItem{
		ID:     "original-id",
		Status: StatusUploaded,
	}
	req := &BatchRequest{Text: "test", Strategy: "replace"}

	result := svc.finalizeStage(
		context.Background(),
		item,
		"req-1", "hash-1", "en",
		req,
		&ResolvedDestination{FolderID: "f1"},
		[]byte(`{}`),
		false, "", "", "",
	)

	assert.Equal(t, StatusCompleted, result.Status)
	assert.Equal(t, "matched-id", result.ID, "DedupeReuse should adopt the matched ID")
}

func TestFinalizeStage_FinalizerError(t *testing.T) {
	db := openFinalizerTestDB(t)
	finalizer := &stubFinalizer{
		cannedErr: assert.AnError,
	}

	svc := &Service{
		finalizer:     finalizer,
		voiceoverRepo: &finalizerTestRepo{db: db},
		log:           zap.NewNop(),
	}

	item := BatchItem{ID: "test-id"}
	req := &BatchRequest{Text: "test", Strategy: "replace"}

	result := svc.finalizeStage(
		context.Background(),
		item,
		"req-1", "hash-1", "en",
		req,
		&ResolvedDestination{FolderID: "f1"},
		[]byte(`{}`),
		false, "", "", "",
	)

	assert.Equal(t, StatusFailed, result.Status)
	assert.Contains(t, result.Error, "Finalize:")
}

func TestFinalizeStage_NilFinalizer(t *testing.T) {
	svc := &Service{
		finalizer: nil, // unwired
		log:       zap.NewNop(),
	}

	item := BatchItem{ID: "test-id"}
	req := &BatchRequest{Text: "test", Strategy: "replace"}

	result := svc.finalizeStage(
		context.Background(),
		item,
		"req-1", "hash-1", "en",
		req,
		&ResolvedDestination{FolderID: "f1"},
		[]byte(`{}`),
		false, "", "", "",
	)

	assert.Equal(t, StatusFailed, result.Status)
	assert.Contains(t, result.Error, "finalizer not wired")
}

// ─────────────────────────────────────────────────────────────────────
// P0.4 Fase 4a + Audit P0.5: PostCommitVerifier + CompletionState
// ─────────────────────────────────────────────────────────────────────

// stubPostCommitVerifier records invocations and surfaces canned
// errors via err. The audit-P0.5 contract is:
//
//   err == nil                                          → CompletionState = StateCompleted
//   errors.Is(err, ErrReconciliationRequired) == true  → CompletionState = StateReconciliationRequired
//   any other non-nil err                                → CompletionState = StateCompletedUnverified
type stubPostCommitVerifier struct {
	verified []string
	err      error
}

func (s *stubPostCommitVerifier) Verify(_ context.Context, voiceoverID string) error {
	s.verified = append(s.verified, voiceoverID)
	return s.err
}

var _ VoiceoverPostCommitVerifier = (*stubPostCommitVerifier)(nil)

func TestFinalizeStage_PostCommitVerification(t *testing.T) {
	db := openFinalizerTestDB(t)
	verifier := &stubPostCommitVerifier{}

	svc := &Service{
		finalizer: &stubFinalizer{
			cannedRes: &FinalizeResult{ID: "vo-verify", Reused: false},
		},
		voiceoverRepo:      &finalizerTestRepo{db: db},
		postCommitVerifier: verifier,
		log:                zap.NewNop(),
	}

	item := BatchItem{
		ID:         "vo-verify",
		Status:     StatusUploaded,
		DriveFileID: "drive-1",
	}
	req := &BatchRequest{Text: "test", Strategy: "replace"}

	result := svc.finalizeStage(
		context.Background(),
		item,
		"req-1", "hash-1", "en",
		req,
		&ResolvedDestination{FolderID: "f1"},
		[]byte(`{}`),
		false, "", "", "",
	)

	assert.Equal(t, StatusCompleted, result.Status)
	assert.Len(t, verifier.verified, 1, "PostCommitVerifier.Verify must be called after commit")
	assert.Equal(t, "vo-verify", verifier.verified[0])
}

func TestFinalizeStage_PostCommitVerificationNilSafe(t *testing.T) {
	db := openFinalizerTestDB(t)

	// Unwired verifier — should not panic.
	svc := &Service{
		finalizer: &stubFinalizer{
			cannedRes: &FinalizeResult{ID: "vo-nil", Reused: false},
		},
		voiceoverRepo:      &finalizerTestRepo{db: db},
		postCommitVerifier: nil, // unwired
		log:                zap.NewNop(),
	}

	item := BatchItem{ID: "vo-nil", Status: StatusUploaded}
	req := &BatchRequest{Text: "test", Strategy: "replace"}

	result := svc.finalizeStage(
		context.Background(),
		item,
		"req-1", "hash-1", "en",
		req,
		&ResolvedDestination{FolderID: "f1"},
		[]byte(`{}`),
		false, "", "", "",
	)

	assert.Equal(t, StatusCompleted, result.Status,
		"Unwired PostCommitVerifier must not block success")
}

// ─────────────────────────────────────────────────────────────────────
// Audit P0.5 (July 2026): CompletionState 3-state mock-verifier tests.
//
// The contract (single source of truth: stages.go::finalizeStage):
//
//   verifier returns nil                                       → CompletionState = StateCompleted
//   verifier returns err wrapping ErrReconciliationRequired    → CompletionState = StateReconciliationRequired
//                                                                    + item.Status = StatusFailed
//                                                                    ("must NOT report StatusCompleted")
//   verifier returns any other non-nil err                     → CompletionState = StateCompletedUnverified
//                                                                    + item.Status = StatusCompleted (warn)
//   verifier unwired                                           → CompletionState stays "" (omitempty hides wire)
//
// Each test reads `cannedRes.CompletionState` AFTER finalizeStage
// returns because the typed `FinalizeResult` returned by the stub
// finalizer is the same pointer finalizeStage mutates — there is no
// test-side recording wrapper required.
// ─────────────────────────────────────────────────────────────────────

func TestFinalizeStage_PostCommitVerificationOK_StateCompleted(t *testing.T) {
	db := openFinalizerTestDB(t)
	cannedRes := &FinalizeResult{ID: "vo-ok", Reused: false}
	verifier := &stubPostCommitVerifier{err: nil}

	svc := &Service{
		finalizer:          &stubFinalizer{cannedRes: cannedRes},
		voiceoverRepo:      &finalizerTestRepo{db: db},
		postCommitVerifier: verifier,
		log:                zap.NewNop(),
	}

	item := BatchItem{ID: "vo-ok", Status: StatusUploaded, DriveFileID: "drive-ok"}
	req := &BatchRequest{Text: "test", Strategy: "replace"}

	result := svc.finalizeStage(
		context.Background(), item,
		"req-ok", "hash-ok", "en",
		req,
		&ResolvedDestination{FolderID: "f1"},
		[]byte(`{}`),
		false, "", "", "",
	)

	assert.Equal(t, StatusCompleted, result.Status, "verifier nil → StatusCompleted preserved")
	assert.Equal(t, StateCompleted, cannedRes.CompletionState,
		"verifier nil → FinalizeResult.CompletionState=StateCompleted (audit P0.5 contract)")
	assert.Len(t, verifier.verified, 1, "PostCommitVerifier.Verify must be called once after commit")
	assert.Equal(t, "vo-ok", verifier.verified[0])
}

func TestFinalizeStage_PostCommitVerificationWarnOnly_StateCompletedUnverified(t *testing.T) {
	db := openFinalizerTestDB(t)
	cannedRes := &FinalizeResult{ID: "vo-warn", Reused: false}
	// Bare error (NOT wrapping ErrReconciliationRequired) → warn-level.
	// Mirrors the production "media_assets projection missing only"
	// case where the canonical voiceovers row IS present but the
	// secondary row is gone.
	bareErr := errors.New("post-commit verification: media_assets projection missing for id=vo-warn")
	verifier := &stubPostCommitVerifier{err: bareErr}

	svc := &Service{
		finalizer:          &stubFinalizer{cannedRes: cannedRes},
		voiceoverRepo:      &finalizerTestRepo{db: db},
		postCommitVerifier: verifier,
		log:                zap.NewNop(),
	}

	item := BatchItem{ID: "vo-warn", Status: StatusUploaded, DriveFileID: "drive-warn"}
	req := &BatchRequest{Text: "test", Strategy: "replace"}

	result := svc.finalizeStage(
		context.Background(), item,
		"req-warn", "hash-warn", "en",
		req,
		&ResolvedDestination{FolderID: "f1"},
		[]byte(`{}`),
		false, "", "", "",
	)

	assert.Equal(t, StatusCompleted, result.Status,
		"warn-level divergence (canonical row IS present) keeps StatusCompleted (audit P0.5)")
	assert.Equal(t, StateCompletedUnverified, cannedRes.CompletionState,
		"bare err → FinalizeResult.CompletionState=StateCompletedUnverified (audit P0.5 contract)")
	assert.Len(t, verifier.verified, 1)
}

func TestFinalizeStage_PostCommitVerificationCanonicalRowMissing_StateReconciliationRequired(t *testing.T) {
	db := openFinalizerTestDB(t)
	cannedRes := &FinalizeResult{ID: "vo-reconcile", Reused: false}
	// Err wraps ErrReconciliationRequired → severe divergence
	// (canonical voiceovers row missing). Mirrors the production
	// adapter's `fmt.Errorf("...: %w", ErrReconciliationRequired)`
	// wrap on the voiceovers-row-missing branch.
	wrappedErr := fmt.Errorf("post-commit verification: voiceovers row missing for id=%q: %w",
		"vo-reconcile", ErrReconciliationRequired)
	verifier := &stubPostCommitVerifier{err: wrappedErr}

	svc := &Service{
		finalizer:          &stubFinalizer{cannedRes: cannedRes},
		voiceoverRepo:      &finalizerTestRepo{db: db},
		postCommitVerifier: verifier,
		log:                zap.NewNop(),
	}

	item := BatchItem{ID: "vo-reconcile", Status: StatusUploaded, DriveFileID: "drive-reconcile"}
	req := &BatchRequest{Text: "test", Strategy: "replace"}

	result := svc.finalizeStage(
		context.Background(), item,
		"req-reconcile", "hash-reconcile", "en",
		req,
		&ResolvedDestination{FolderID: "f1"},
		[]byte(`{}`),
		false, "", "", "",
	)

	// Audit P0.5 mandate: "NON far finire il result come StateCompleted
	// se il check post-commit è ReconciliationRequired" — finalizeStage
	// MUST surface item.Status=StatusFailed so GenerateBatch's aggregate
	// OK check correctly propagates the divergence.
	assert.Equal(t, StatusFailed, result.Status,
		"canonical row missing MUST NOT report StatusCompleted (audit P0.5 surface contract)")
	assert.Equal(t, StateReconciliationRequired, cannedRes.CompletionState,
		"err wrapping ErrReconciliationRequired → FinalizeResult.CompletionState=StateReconciliationRequired")
	assert.Contains(t, result.Error, "post_commit_reconciliation_required",
		"forensic-trail Error string carries the typed prefix")
	assert.Contains(t, result.Errors, FailureReconciliationRequired,
		"FailureReconciliationRequired (NEW typed constant, audit P0.5) appended to Errors[] for forensic trail — NOT FailureTxCommit (the tx did commit successfully)")
	assert.Len(t, verifier.verified, 1)
}

// ─────────────────────────────────────────────────────────────────────
// Audit P0 #2 (July 2026): OptionalSteps + RequiredSteps tracking
// ─────────────────────────────────────────────────────────────────────
//
// Pre-P0 #2 the finalizer tracked SkippedSteps []string which
// conflated optional guard-skips with required-dep-not-wired
// wiring failures. P0 #2 splits the surface — see FinalizeResult
// doc on the production shape.
//
// Required-dep-not-wired is now a HARD ERROR (fail-fast at the top
// of Finalize()) per godlike/07 ZERO LEGACY. The sub-tests below
// assert that:
//
//   - OptionalSteps tracks only Step 1 (dedupe) guard-skips.
//   - RequiredSteps tracks Steps 4/5/6 with execution-state
//     markers (": executed" / ": guarded (...)") on success.
//   - Required-dep-not-wired wired scenarios surface a typed error
//     from Finalize() with the canonical errRequiredStepNotWired
//     prefix — NOT a RequiredSteps "<step>: unwired" entry.

// TestFinalizeResult_TracksOptionalAndRequiredSteps exercises the
// real voiceoverFinalizer with various dep configurations and
// asserts the audit-P0 #2 split tracking contract. The test uses
// in-memory SQLite so Commit/Rollback work.
func TestFinalizeResult_TracksOptionalAndRequiredSteps(t *testing.T) {
	db := openFinalizerTestDB(t)
	repo := &finalizerTestRepo{db: db}

	t.Run("all wired with DriveFileID and FileHash", func(t *testing.T) {
		// Full wiring: all required steps execute, optional Steps empty.
		f := newVoiceoverFinalizer(voiceoverFinalizerDeps{
			VoiceoverRepo:    repo,
			Outbox:           &stubOutboxEnqueuer{},
			LifecycleService: &stubProjectionUpserter{},
			Logger:           zap.NewNop(),
		})

		tx, err := repo.BeginTx(context.Background())
		require.NoError(t, err)
		defer func() { _ = tx.Rollback() }()

		res, err := f.Finalize(context.Background(), tx, &FinalizeCommand{
			ID:             "vo-1",
			RequestID:      "req-1",
			TextHash:       "hash",
			Text:           "hello",
			Language:       "en",
			Voice:          "en_female",
			Filename:       "test.mp3",
			LocalPath:      "/tmp/test.mp3",
			DriveFileID:    "drive-1",
			DriveLink:      "https://drive.google.com/file/d/drive-1/view",
			DownloadLink:   "https://drive.google.com/uc?id=drive-1",
			FileHash:       "abc123",
			FolderID:       "folder-1",
			FolderPath:     "/tmp/vo",
			ShouldSwap:     true,
			OldDriveFileID: "old-drive-1",
			OldLocalPath:   "/tmp/old.mp3",
		})
		require.NoError(t, err)
		assert.False(t, res.Reused)
		assert.Empty(t, res.OptionalSteps, "Step 1 ran (DriveFileID populated) → no optional step skipped")
		assert.Equal(t, []string{
			"media_assets_projection: executed",
			"index_outbox: executed",
			"cleanup_outbox: executed",
		}, res.RequiredSteps, "all required steps executed → exactly the 3 executed markers")
	})

	t.Run("empty DriveFileID skips dedupe", func(t *testing.T) {
		// OptStep 1 data-state guard (DriveFileID empty) — recordable
		// skip. Required steps should all still execute.
		f := newVoiceoverFinalizer(voiceoverFinalizerDeps{
			VoiceoverRepo:    repo,
			Outbox:           &stubOutboxEnqueuer{},
			LifecycleService: &stubProjectionUpserter{},
			Logger:           zap.NewNop(),
		})

		tx, err := repo.BeginTx(context.Background())
		require.NoError(t, err)
		defer func() { _ = tx.Rollback() }()

		res, err := f.Finalize(context.Background(), tx, &FinalizeCommand{
			ID:          "vo-2",
			RequestID:   "req-2",
			TextHash:    "hash",
			Text:        "hello",
			Language:    "en",
			Voice:       "en_female",
			Filename:    "test.mp3",
			LocalPath:   "/tmp/test.mp3",
			DriveFileID: "", // empty → Step 1 guard-skipped
			FileHash:    "abc123",
			FolderID:    "folder-1",
		})
		require.NoError(t, err)
		assert.Equal(t, []string{"dedupe: empty DriveFileID"}, res.OptionalSteps,
			"empty DriveFileID → only Step 1 in OptionalSteps")
		assert.Equal(t, []string{
			"media_assets_projection: executed",
			"index_outbox: executed",
			"cleanup_outbox: guarded (ShouldSwap=false)",
		}, res.RequiredSteps,
			"all required steps execute OR guard-skip with reason; no Step 1 / dedupe here")
	})

	t.Run("empty FileHash guard-skips index outbox", func(t *testing.T) {
		// RequiredStep data-state guard (FileHash empty) — recordable
		// as a RequiredSteps guard marker, NOT an optional skip.
		f := newVoiceoverFinalizer(voiceoverFinalizerDeps{
			VoiceoverRepo:    repo,
			Outbox:           &stubOutboxEnqueuer{},
			LifecycleService: &stubProjectionUpserter{},
			Logger:           zap.NewNop(),
		})

		tx, err := repo.BeginTx(context.Background())
		require.NoError(t, err)
		defer func() { _ = tx.Rollback() }()

		res, err := f.Finalize(context.Background(), tx, &FinalizeCommand{
			ID:          "vo-3",
			RequestID:   "req-3",
			TextHash:    "hash",
			Text:        "hello",
			Language:    "en",
			Voice:       "en_female",
			Filename:    "test.mp3",
			LocalPath:   "/tmp/test.mp3",
			DriveFileID: "drive-2",
			FileHash:    "", // empty → Step 5 guard-skipped
			FolderID:    "folder-1",
		})
		require.NoError(t, err)
		assert.Empty(t, res.OptionalSteps)
		assert.Equal(t, []string{
			"media_assets_projection: executed",
			"index_outbox: guarded (empty FileHash)",
			"cleanup_outbox: guarded (ShouldSwap=false)",
		}, res.RequiredSteps,
			"Step 5 guard-skipped for data-state reason → RequiredSteps marker, not OptionalSteps entry")
	})

	t.Run("ShouldSwap true with no prior artefacts guard-skips cleanup", func(t *testing.T) {
		// Step 6 guard-skip for data-state reason — ShouldSwap=true but
		// no OldDriveFileID + no old local paths. Distinct from the
		// ShouldSwap=false guard.
		f := newVoiceoverFinalizer(voiceoverFinalizerDeps{
			VoiceoverRepo:    repo,
			Outbox:           &stubOutboxEnqueuer{},
			LifecycleService: &stubProjectionUpserter{},
			Logger:           zap.NewNop(),
		})

		tx, err := repo.BeginTx(context.Background())
		require.NoError(t, err)
		defer func() { _ = tx.Rollback() }()

		res, err := f.Finalize(context.Background(), tx, &FinalizeCommand{
			ID:          "vo-6",
			RequestID:   "req-6",
			TextHash:    "hash",
			Text:        "hello",
			Language:    "en",
			Voice:       "en_female",
			Filename:    "test.mp3",
			LocalPath:   "/tmp/test.mp3",
			DriveFileID: "drive-5",
			FileHash:    "abc123",
			FolderID:    "folder-1",
			ShouldSwap:  true, // no old artefacts → Step 6 guard-skipped
			// OldDriveFileID, OldLocalPath, OldCleanedPath all empty.
		})
		require.NoError(t, err)
		assert.Empty(t, res.OptionalSteps)
		assert.Equal(t, []string{
			"media_assets_projection: executed",
			"index_outbox: executed",
			"cleanup_outbox: guarded (no prior artefacts)",
		}, res.RequiredSteps,
			"Step 6 guard-skipped for data-state reason (no prior artefacts) → RequiredSteps marker")
	})
}

// ─────────────────────────────────────────────────────────────────────
// Audit P0 #2 (July 2026): Required-dep-not-wired fails fast
// ─────────────────────────────────────────────────────────────────────
//
// The audit-mandated fail-fast contract: a missing Required dep
// (LifecycleService / Outbox) MUST surface as a typed error from
// Finalize() — NOT degrade into RequiredSteps "<step>: unwired" or
// StatusCompleted. These 3 sub-tests pin the contract:
//
//   - unwired LifecycleService → typed error mentioning media_assets_projection
//   - unwired Outbox → typed error mentioning index_outbox
//     (BOTH index + cleanup outbox steps depend on the same dep;
//      the error message names index as the first-encountered
//      required step).
//   - sanity: all wired + no field guards → no error (covers the
//      "test didn't accidentally regress to always-erroring"
//      tail-case).

// TestFinalize_RequiredStepNotWired_FailsFast pins the audit-P0 #2
// fail-fast contract: unwired required deps surface as a typed
// error rather than degrading to SkippedSteps-style reports of
// the form "<step>: unwired".
func TestFinalize_RequiredStepNotWired_FailsFast(t *testing.T) {
	db := openFinalizerTestDB(t)
	repo := &finalizerTestRepo{db: db}

	t.Run("unwired LifecycleService returns required-step-not-wired error", func(t *testing.T) {
		f := newVoiceoverFinalizer(voiceoverFinalizerDeps{
			VoiceoverRepo:    repo,
			Outbox:           &stubOutboxEnqueuer{},
			LifecycleService: nil, // P0 #2 fatal: not Optional-skipped
			Logger:           zap.NewNop(),
		})

		tx, err := repo.BeginTx(context.Background())
		require.NoError(t, err)
		defer func() { _ = tx.Rollback() }()

		res, err := f.Finalize(context.Background(), tx, &FinalizeCommand{
			ID:          "vo-lifecycle-unwired",
			RequestID:   "req-lu",
			TextHash:    "hash",
			Text:        "hello",
			Language:    "en",
			Voice:       "en_female",
			Filename:    "test.mp3",
			LocalPath:   "/tmp/test.mp3",
			DriveFileID: "drive-1",
			FileHash:    "abc123",
			FolderID:    "folder-1",
			ShouldSwap:  true,
		})

		// Audit-P0 #2 fail-fast: error MUST NOT be nil, MUST mention
		// the canonical required-step name (so operators / log
		// scanners can grep on the surface contract).
		require.Error(t, err, "unwired LifecycleService is a fatal wiring error — Finalize() must return non-nil err")
		assert.Contains(t, err.Error(), "required step \"media_assets_projection\" not wired",
			"error MUST name the required step + the canonical not-wired phrasing")
		assert.Nil(t, res,
			"unwired Required step → Finalize() returns nil result, NOT a degraded result with SkippedSteps/RequiredSteps entries")
	})

	t.Run("unwired Outbox returns required-step-not-wired error", func(t *testing.T) {
		// LifecycleService wired; Outbox unwired. Step 5 (index
		// outbox) is the first-encountered required step that
		// depends on Outbox; Step 6 (cleanup) also depends on it.
		// The audit requires fail-fast BEFORE any business work,
		// so we expect a single typed error mentioning
		// index_outbox (the canonical required-step name; wire-stable
		// with the pre-P0 #2 SkippedSteps values for log-grep continuity).
		f := newVoiceoverFinalizer(voiceoverFinalizerDeps{
			VoiceoverRepo:    repo,
			Outbox:           nil, // P0 #2 fatal: not Optional-skipped
			LifecycleService: &stubProjectionUpserter{},
			Logger:           zap.NewNop(),
		})

		tx, err := repo.BeginTx(context.Background())
		require.NoError(t, err)
		defer func() { _ = tx.Rollback() }()

		res, err := f.Finalize(context.Background(), tx, &FinalizeCommand{
			ID:          "vo-outbox-unwired",
			RequestID:   "req-ou",
			TextHash:    "hash",
			Text:        "hello",
			Language:    "en",
			Voice:       "en_female",
			Filename:    "test.mp3",
			LocalPath:   "/tmp/test.mp3",
			DriveFileID: "drive-2",
			FileHash:    "abc123",
			FolderID:    "folder-1",
			ShouldSwap:  true,
		})

		require.Error(t, err, "unwired Outbox is a fatal wiring error — Finalize() must return non-nil err")
		assert.Contains(t, err.Error(), "required step \"index_outbox\" not wired",
			"error MUST name the required step (index outbox is the first required step depending on Outbox)")
		assert.Nil(t, res)
	})

	t.Run("all required deps wired + no data-state guards → no fail-fast error", func(t *testing.T) {
		// Sanity tail: the fail-fast check must NOT over-trigger
		// when both required deps are wired. Pre-P0 #2 this was
		// implicit; pin it so a future refactor that moves the
		// fail-fast guard accidentally above a "soft" dependency
		// check surfaces as a test failure rather than a silent
		// regression.
		f := newVoiceoverFinalizer(voiceoverFinalizerDeps{
			VoiceoverRepo:    repo,
			Outbox:           &stubOutboxEnqueuer{},
			LifecycleService: &stubProjectionUpserter{},
			Logger:           zap.NewNop(),
		})

		tx, err := repo.BeginTx(context.Background())
		require.NoError(t, err)
		defer func() { _ = tx.Rollback() }()

		res, err := f.Finalize(context.Background(), tx, &FinalizeCommand{
			ID:          "vo-all-wired",
			RequestID:   "req-aw",
			TextHash:    "hash",
			Text:        "hello",
			Language:    "en",
			Voice:       "en_female",
			Filename:    "test.mp3",
			LocalPath:   "/tmp/test.mp3",
			DriveFileID: "drive-aw",
			FileHash:    "abc123",
			FolderID:    "folder-1",
			ShouldSwap:  true,
		})

		require.NoError(t, err, "all required deps wired → no fail-fast")
		require.NotNil(t, res)
		assert.Empty(t, res.OptionalSteps)
		assert.Equal(t, []string{
			"media_assets_projection: executed",
			"index_outbox: executed",
			"cleanup_outbox: guarded (no prior artefacts)",
		}, res.RequiredSteps,
			"all required deps wired + no prior artefacts → ShouldSwap=true without Old IDs path line through guarded")
	})
}

// ─────────────────────────────────────────────────────────────────────
// stubOutboxEnqueuer + stubProjectionUpserter for SkippedSteps tests
// ─────────────────────────────────────────────────────────────────────

type stubOutboxEnqueuer struct{}

func (s *stubOutboxEnqueuer) EnqueueIndexEvent(_ context.Context, _ *sql.Tx, _, _ string) error {
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
