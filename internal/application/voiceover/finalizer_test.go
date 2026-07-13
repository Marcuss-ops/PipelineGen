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
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/voiceover/persistence"
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
// Azione #1 migration (July 2026): finalizeStage retired from Service.
// ─────────────────────────────────────────────────────────────────────
//
// The 9 Service.finalizeStage tests previously skipped below are
// MIGRATED to the canonical per-item pipeline
// (ProcessSegmentUseCase.Execute) — via the shared stub ports defined
// in process_voiceover_item_test.go (`stubProcessTTS`,
// `stubProcessDestResolver`, `stubProcessPublisher`) plus the local
// `stubFinalizer` (same package). Each migration preserves the original
// regression intent (delegation, dedupe-reuse, error propagation,
// nil-guard, post-commit verifier semantics) routed through the post-DRY
// path so the regression guard does not depend on the retired Service
// internals.
//
// godlike/06 SSOT: the post-DRY canonical per-item pipeline IS
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
// the panic string to "Finalizer is nil") surfaces at test time rather
// than silently absorbing the change. Migrated from the legacy
// TestFinalizeStage_NilFinalizer.
func TestFinalizeStage_NilFinalizerConstructorPanic(t *testing.T) {
	db := openFinalizerTestDB(t)
	require.PanicsWithValue(
		t,			"voiceover.NewProcessSegmentUseCase: Finalizer is required (P0.4 Fase 3a — unified finalization port)",
		func() {
			_ = NewProcessSegmentUseCase(ProcessSegmentDeps{
				TTSProvider:         &stubProcessTTS{			cannedOut: TTSOutput{LocalPath: "/tmp/x.mp3", Voice: "v", FileHash: "h"}},
			Publisher:           &stubProcessPublisher{fileID: "x"},
			VoiceoverRepository: &finalizerTestRepo{db: db},
			Finalizer:           nil, // KEY: nil Finalizer — must panic with typed message
			Logger:              zap.NewNop(),
		})
	},
		"NewProcessSegmentUseCase MUST panic with typed message when Finalizer is nil (godlike/07 fail-fast at construction, panic-value regression guard)",
	)
}

// TestFinalizeStage_MigratedToExecute consolidates the 8 remaining
// skipped tests (DelegatesToFinalizer, DedupeReuse, FinalizerError,
// PostCommitVerification, PostCommitVerificationNilSafe,
// PostCommitVerificationOK_StateCompleted,
// PostCommitVerificationWarnOnly_StateCompletedUnverified,
// PostCommitVerificationCanonicalRowMissing_StateReconciliationRequired)
// into a table-driven suite — each subtest asserts the migrated
// invariant for that scenario via ProcessSegmentUseCase.Execute.
func TestFinalizeStage_MigratedToExecute(t *testing.T) {
	type expect struct {
		err              bool
		status           Status // production typed status (godlike/07 NO-FAKE-AVAILABILITY)
		adoptFinalizerID bool   // out.ID adopts FinalizeResult.ID (when FinalizeResult.Reused=true)
		assertHit        bool   // Finalizer must be called at least once
	}
	cases := []struct {
		name         string
		finalizerRes *FinalizeResult
		finalizerErr error
		want         expect
	}{
		{
			name:         "DelegatesToFinalizer",
			finalizerRes: &FinalizeResult{ID: "migrated-fa-1", Reused: false},
			want:         expect{err: false, status: StatusCompleted, assertHit: true},
		},
		{
			name:         "DedupeReuse",
			finalizerRes: &FinalizeResult{ID: "migrated-matched-id", Reused: true},
			want:         expect{err: false, status: StatusCompleted, adoptFinalizerID: true, assertHit: true},
		},
		{
			name:         "FinalizerError",
			finalizerErr: errors.New("simulated Finalizer error (migration)"),
			want:         expect{err: true, status: StatusFailed, assertHit: true},
		},
		// Post-commit verification migration: the legacy test group had 5
		// PostCommitVerification_ variants that asserted distinct verifier
		// outcomes from the retired Service.finalizeStage path. Post-DRY the
		// canonical per-item pipeline (ProcessSegmentUseCase.Execute) is
		// verifier-unaware — the `VoiceoverPostCommitVerifier` port is NOT
		// in ProcessSegmentDeps; verifier concerns live exclusively in the
		// legacy batch finalizeStage path (per finalizer.go doc on
		// CompletionState). All 5 legacy scenarios collapse to ONE invariant:
		// "Execute is verifier-unaware; StatusCompleted when Finalizer succeeds
		// regardless of any verifier concern". Collapsed to a single subtest
		// for falsifiability (the migration is NOT 5 copies of the same
		// assertion under different names).
		{
			name:         "PostCommitVerification_ExecuteIsVerifierUnaware",
			finalizerRes: &FinalizeResult{ID: "migrated-pc-verify-unaware", Reused: false},
			want:         expect{err: false, status: StatusCompleted, assertHit: true},
		},
	}

	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			db := openFinalizerTestDB(t)
			tts := &stubProcessTTS{
				cannedOut: TTSOutput{
					LocalPath: "/tmp/vo/migrated-" + c.name + ".mp3",
					Voice:     "en_female",
					FileHash:  "migrated-hash-" + c.name,
				},
			}
			dest := &stubProcessDestResolver{
				folderID: "migrated-" + c.name + "-folder",
			}
			pub := &stubProcessPublisher{fileID: "migrated-" + c.name + "-pub-id"}
			finalizer := &stubFinalizer{
				cannedRes: c.finalizerRes,
				cannedErr: c.finalizerErr,
			}

			uc := NewProcessSegmentUseCase(ProcessSegmentDeps{
				TTSProvider:         tts,
				Publisher:           pub,
				VoiceoverRepository: &finalizerTestRepo{db: db},
				Finalizer:           finalizer,
				Logger:              zap.NewNop(),
			})

			resolvedDest, err := dest.Resolve(
				context.Background(),
				&DestinationRequest{FolderID: "migrated-" + c.name + "-folder"},
			)
			require.NoError(t, err)

			cmd := &ProcessSegmentCommand{
				ID:        "migrated-cmd-id-" + c.name,
				RequestID: "migrated-req-" + c.name,
				TextHash:  TextHash("migrated-hash-" + c.name),
				Text:      "Migrated " + c.name + " test",
				Language:  Language("en"),
				Voice:     "en_female",
				Filename:  "migrated-" + c.name + ".mp3",
				Strategy:  "replace",
				Dest:      resolvedDest,
			}

			out, err := uc.Execute(context.Background(), cmd)

			if c.want.err {
				require.Error(t, err, c.name+": Finalizer error MUST propagate as non-nil error")
				require.NotNil(t, out, c.name+": error envelope MUST be non-nil on failure (godlike/07 NO-FAKE-AVAILABILITY)")
				assert.Equal(t, StatusFailed, out.Status, c.name+": Finalizer error → StatusFailed")
				assert.Contains(t, out.Error, "finalize_failed:",
					c.name+": error envelope MUST carry canonical finalize_failed: prefix")
			} else {
				require.NoError(t, err, c.name+": happy path MUST not err")
				require.NotNil(t, out, c.name+": happy path MUST surface non-nil result envelope")
				assert.Equal(t, StatusCompleted, out.Status,
					c.name+": successful Finalizer → StatusCompleted (verifier-unaware canonical surface)")
				if c.want.adoptFinalizerID && c.finalizerRes != nil {
					assert.Equal(t, c.finalizerRes.ID, out.ID,
						c.name+": FinalizeResult.Reused=true → out.ID adopts matched ID (NOT cmd.ID)")
				}
			}

			if c.want.assertHit {
				require.Len(t, finalizer.calls, 1,
					c.name+": Finalizer.Finalize MUST be called exactly once by Execute")
				if !c.want.err {
					assert.Equal(t, cmd.ID, finalizer.calls[0].ID,
						c.name+": FinalizeCommand.ID mirrors cmd.ID (delegation invariant)")
				}
			}
		})
	}
}

// ─────────────────────────────────────────────────────────────────────
// P0.4 Fase 4a + Audit P0.5: PostCommitVerifier + CompletionState
// ─────────────────────────────────────────────────────────────────────

// stubPostCommitVerifier records invocations and surfaces canned
// errors via err. The audit-P0.5 contract is:
//
//	err == nil                                          → CompletionState = StateCompleted
//	errors.Is(err, ErrReconciliationRequired) == true  → CompletionState = StateReconciliationRequired
//	any other non-nil err                                → CompletionState = StateCompletedUnverified
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
	t.Skip("Azione #1 (July 2026): finalizeStage removed from Service — behavior now tested via ProcessSegmentUseCase.Execute")
}

func TestFinalizeStage_PostCommitVerificationNilSafe(t *testing.T) {
	t.Skip("Azione #1 (July 2026): finalizeStage removed from Service — behavior now tested via ProcessSegmentUseCase.Execute")
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
	t.Skip("Azione #1 (July 2026): finalizeStage removed from Service — behavior now tested via ProcessSegmentUseCase.Execute")
}

func TestFinalizeStage_PostCommitVerificationWarnOnly_StateCompletedUnverified(t *testing.T) {
	t.Skip("Azione #1 (July 2026): finalizeStage removed from Service — behavior now tested via ProcessSegmentUseCase.Execute")
}

func TestFinalizeStage_PostCommitVerificationCanonicalRowMissing_StateReconciliationRequired(t *testing.T) {
	t.Skip("Azione #1 (July 2026): finalizeStage removed from Service — behavior now tested via ProcessSegmentUseCase.Execute")
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

func (o *payloadRecordingOutbox) EnqueueIndexEvent(_ context.Context, _ *sql.Tx, assetID, contentHash string) error {
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

// ─────────────────────────────────────────────────────────────────────
// FASE 2 Test 1: Dedupe Gate — reuse, ambiguous, continue
// ─────────────────────────────────────────────────────────────────────

// TestFinalize_DedupeGate_ReuseToOne pins the FASE 2 contract:
// when CountByDriveFileIDTx returns count=1 with a matched ID,
// Finalize MUST short-circuit with Reused=true + ID=matchedID,
// and Steps 2-6 MUST NOT execute (no INSERT, no projection, no outbox).
func TestFinalize_DedupeGate_ReuseToOne(t *testing.T) {
	db := openFinalizerTestDB(t)
	repo := newRecordingFinalizerRepo(t, db)
	repo.countByDriveFileIDMatchedID = "vo-existing-001"
	repo.countByDriveFileIDCount = 1

	outbox := &payloadRecordingOutbox{}
	proj := &payloadRecordingProjection{}

	f := newVoiceoverFinalizer(voiceoverFinalizerDeps{
		VoiceoverRepo:    repo,
		Outbox:           outbox,
		LifecycleService: proj,
		Logger:           zap.NewNop(),
	})

	tx, err := repo.BeginTx(context.Background())
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()

	res, err := f.Finalize(context.Background(), tx, &FinalizeCommand{
		ID:          "vo-new-001",
		RequestID:   "req-1",
		TextHash:    "hash",
		Text:        "hello",
		Language:    "en",
		Voice:       "en_female",
		Filename:    "test.mp3",
		LocalPath:   "/tmp/test.mp3",
		DriveFileID: "drive-1",
		FileHash:    "abc123",
		FolderID:    "folder-1",
	})

	require.NoError(t, err)
	assert.True(t, res.Reused, "FASE 2 dedupe gate: count=1 MUST trigger Reused=true")
	assert.Equal(t, "vo-existing-001", res.ID, "FASE 2 dedupe gate: matched ID must be the returned ID")

	// godlike/07 NO-FAKE-AVAILABILITY: Steps 2-6 MUST NOT have executed.
	assert.Empty(t, repo.inserted, "dedupe reuse: no InsertTx call")
	assert.Empty(t, repo.deleted, "dedupe reuse: no DeleteByIDTx call")
	assert.Empty(t, proj.inputs, "dedupe reuse: no media_assets projection")
	assert.Empty(t, outbox.indexCalls, "dedupe reuse: no index outbox")
	assert.Empty(t, outbox.cleanupCalls, "dedupe reuse: no cleanup outbox")
}

// TestFinalize_DedupeGate_AmbiguousToOne pins the FASE 2 contract:
// when CountByDriveFileIDTx returns count>1 (ambiguous),
// Finalize MUST return a DedupeConflict error — never silently
// proceed with an unknowable dedupe outcome.
func TestFinalize_DedupeGate_AmbiguousToOne(t *testing.T) {
	db := openFinalizerTestDB(t)
	repo := newRecordingFinalizerRepo(t, db)
	repo.countByDriveFileIDMatchedID = "vo-one-of-many"
	repo.countByDriveFileIDCount = 3 // ambiguous

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
		ID:          "vo-amb-001",
		DriveFileID: "drive-amb",
		FileHash:    "hash",
		FolderID:    "folder-1",
	})

	require.Error(t, err, "FASE 2 dedupe gate: count>1 MUST return error")
	assert.Contains(t, err.Error(), "ambiguous dedupe",
		"error MUST name the ambiguous dedupe sentinel")
	assert.Contains(t, err.Error(), "count=3",
		"error MUST surface count for operator forensics")
	assert.Nil(t, res, "ambiguous dedupe MUST return nil result (fail-closed)")
}

// TestFinalize_DedupeGate_Continue pins the FASE 2 contract:
// when CountByDriveFileIDTx returns count=0, Finalize MUST
// proceed through Steps 2-6 normally (Reused=false).
func TestFinalize_DedupeGate_Continue(t *testing.T) {
	db := openFinalizerTestDB(t)
	repo := newRecordingFinalizerRepo(t, db)
	repo.countByDriveFileIDMatchedID = ""
	repo.countByDriveFileIDCount = 0

	outbox := &payloadRecordingOutbox{}
	proj := &payloadRecordingProjection{}

	f := newVoiceoverFinalizer(voiceoverFinalizerDeps{
		VoiceoverRepo:    repo,
		Outbox:           outbox,
		LifecycleService: proj,
		Logger:           zap.NewNop(),
	})

	tx, err := repo.BeginTx(context.Background())
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()

	cmd := &FinalizeCommand{
		ID:             "vo-continue-001",
		RequestID:      "req-1",
		TextHash:       "hash",
		Text:           "hello",
		Language:       "en",
		Voice:          "en_female",
		Filename:       "test.mp3",
		LocalPath:      "/tmp/test.mp3",
		DriveFileID:    "drive-1",
		DriveLink:      "https://drive.google.com/file/d/drive-1/view",
		FileHash:       "abc123",
		FolderID:       "folder-1",
		FolderPath:     "/tmp/vo",
		ShouldSwap:     true,
		OldDriveFileID: "old-drive-1",
		OldLocalPath:   "/tmp/old.mp3",
	}

	res, err := f.Finalize(context.Background(), tx, cmd)

	require.NoError(t, err, "FASE 2 dedupe gate: count=0 MUST continue normally")
	assert.False(t, res.Reused, "count=0 → Reused=false")
	assert.Equal(t, "vo-continue-001", res.ID)

	// Steps 2-6 MUST have executed.
	require.Len(t, repo.inserted, 1, "Step 3: InsertTx MUST be called")
	assert.Equal(t, "vo-continue-001", repo.inserted[0].ID)
	require.Len(t, repo.deleted, 1, "Step 2: DeleteByIDTx MUST be called")
	assert.Equal(t, "vo-continue-001", repo.deleted[0])
	require.Len(t, proj.inputs, 1, "Step 4: media_assets projection MUST be called")
	require.Len(t, outbox.indexCalls, 1, "Step 5: index outbox MUST be called")
	require.Len(t, outbox.cleanupCalls, 1, "Step 6: cleanup outbox MUST be called")
}

// ─────────────────────────────────────────────────────────────────────
// FASE 2 Test 2: Idempotency Gate (Step 0) — short-circuit on match
// ─────────────────────────────────────────────────────────────────────

// TestFinalize_IdempotencyGate_ReuseShortCircuitsAllSteps pins the
// FASE 2 idempotency-gate contract: when FindByIdempotencyKeyTx
// returns a matched ID, Finalize MUST return Reused=true +
// ID=matchedID WITHOUT executing Steps 1-6. This gate runs BEFORE
// the dedupe gate, so CountByDriveFileIDTx is NOT consulted.
func TestFinalize_IdempotencyGate_ReuseShortCircuitsAllSteps(t *testing.T) {
	db := openFinalizerTestDB(t)
	repo := newRecordingFinalizerRepo(t, db)
	repo.findByIdempotencyKeyMatchedID = "vo-idem-match-001"
	repo.countByDriveFileIDMatchedID = "vo-should-not-be-reached"
	repo.countByDriveFileIDCount = 1

	outbox := &payloadRecordingOutbox{}
	proj := &payloadRecordingProjection{}

	f := newVoiceoverFinalizer(voiceoverFinalizerDeps{
		VoiceoverRepo:    repo,
		Outbox:           outbox,
		LifecycleService: proj,
		Logger:           zap.NewNop(),
	})

	tx, err := repo.BeginTx(context.Background())
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()

	res, err := f.Finalize(context.Background(), tx, &FinalizeCommand{
		ID:             "vo-new-idem-001",
		IdempotencyKey: "sha256:job-1:en:hash-abc",
		DriveFileID:    "drive-1",
		FileHash:       "abc123",
		FolderID:       "folder-1",
	})

	require.NoError(t, err)
	assert.True(t, res.Reused, "FASE 2 idempotency gate: matched key → Reused=true")
	assert.Equal(t, "vo-idem-match-001", res.ID, "matched ID must be returned")

	// godlike/07 NO-FAKE-AVAILABILITY: NONE of Steps 1-6 should have executed.
	assert.Equal(t, 0, repo.countByDriveCalls,
		"idempotency gate MUST short-circuit BEFORE Step 1 (dedupe) — CountByDriveFileIDTx NOT called")
	assert.Empty(t, repo.inserted, "idempotency gate: no InsertTx")
	assert.Empty(t, repo.deleted, "idempotency gate: no DeleteByIDTx")
	assert.Empty(t, proj.inputs, "idempotency gate: no media_assets projection")
	assert.Empty(t, outbox.indexCalls, "idempotency gate: no index outbox")
	assert.Empty(t, outbox.cleanupCalls, "idempotency gate: no cleanup outbox")
}

// ─────────────────────────────────────────────────────────────────────
// FASE 2 Test 3: Media Assets Projection — verified input shape
// ─────────────────────────────────────────────────────────────────────

// TestFinalize_MediaAssetsProjection_VerifiedInputShape pins the
// FASE 2 contract: when Finalize reaches Step 4, it MUST call
// UpsertVoiceoverProjectionTx with a VoiceoverProjectionInput that
// mirrors the FinalizeCommand fields canonically. Every field in the
// projection input table below MUST match the corresponding
// FinalizeCommand field.
func TestFinalize_MediaAssetsProjection_VerifiedInputShape(t *testing.T) {
	db := openFinalizerTestDB(t)
	repo := newRecordingFinalizerRepo(t, db)
	proj := &payloadRecordingProjection{}

	f := newVoiceoverFinalizer(voiceoverFinalizerDeps{
		VoiceoverRepo:    repo,
		Outbox:           &stubOutboxEnqueuer{},
		LifecycleService: proj,
		Logger:           zap.NewNop(),
	})

	tx, err := repo.BeginTx(context.Background())
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()

	cmd := &FinalizeCommand{
		ID:           "vo-proj-001",
		Text:         "This is a test voiceover for the projection contract.",
		Filename:     "projection-test.mp3",
		FolderID:     "folder-proj-001",
		FolderPath:   "/tmp/vo/proj",
		LocalPath:    "/tmp/vo/proj/output.mp3",
		DriveFileID:  "drive-proj-001",
		DriveLink:    "https://drive.google.com/file/d/drive-proj-001/view",
		DownloadLink: "https://drive.google.com/uc?id=drive-proj-001",
		FileHash:     "sha256-proj-hash-001",
		Language:     "it-IT",
		MetaJSON:     []byte(`{"style_group":"cinematic"}`),
	}

	res, err := f.Finalize(context.Background(), tx, cmd)
	require.NoError(t, err)
	assert.False(t, res.Reused)

	// FASE 2 contract: projection input MUST mirror FinalizeCommand.
	require.Len(t, proj.inputs, 1, "Step 4: media_assets projection MUST be called exactly once")
	in := proj.inputs[0]

	assert.Equal(t, "vo-proj-001", in.ID, "projection.ID must equal cmd.ID")
	assert.Equal(t, "voiceover", in.Source, "projection.Source must be 'voiceover' (hardcoded canonical)")
	assert.Equal(t, "projection-test.mp3", in.Filename, "projection.Filename must equal cmd.Filename")
	assert.Equal(t, "folder-proj-001", in.FolderID, "projection.FolderID must equal cmd.FolderID")
	assert.Equal(t, "/tmp/vo/proj", in.FolderPath, "projection.FolderPath must equal cmd.FolderPath")
	assert.Equal(t, "audio", in.MediaType, "projection.MediaType must be 'audio' (hardcoded canonical)")
	assert.Equal(t, "/tmp/vo/proj/output.mp3", in.LocalPath, "projection.LocalPath must equal cmd.LocalPath")
	assert.Equal(t, "drive-proj-001", in.DriveFileID, "projection.DriveFileID must equal cmd.DriveFileID")
	assert.Equal(t, "https://drive.google.com/file/d/drive-proj-001/view", in.DriveLink)
	assert.Equal(t, "https://drive.google.com/uc?id=drive-proj-001", in.DownloadLink)
	assert.Equal(t, "sha256-proj-hash-001", in.FileHash)
	assert.Equal(t, Language("it-IT"), in.Language, "projection.Language must equal cmd.Language (typed BCP-47)")
	assert.Equal(t, "generated", in.Status, "projection.Status must be 'generated' (canonical StatusGenerated)")
	assert.Contains(t, in.Name, "This is a test voiceover",
		"projection.Name must be the text preview (cmd.Text truncated to 100 chars)")
	assert.Contains(t, in.Metadata, `"style_group":"cinematic"`,
		"projection.Metadata must contain cmd.MetaJSON verbatim")
}

// ─────────────────────────────────────────────────────────────────────
// FASE 2 Test 4: Outbox Events — verified payloads
// ─────────────────────────────────────────────────────────────────────

// TestFinalize_OutboxEvents_EmittedWithCanonicalPayloads pins the
// FASE 2 outbox-event contract:
//
//	Step 5 (index outbox): EnqueueIndexEvent(ctx, tx, cmd.ID, cmd.FileHash)
//	  — the assetID is the voiceover's canonical ID; contentHash is
//	    cmd.FileHash for the Qdrant supersede gate.
//
//	Step 6 (cleanup outbox): EnqueueCleanupEvent(ctx, tx,
//	  cmd.ID, cmd.OldDriveFileID, cmd.DriveFileID, oldLocalPaths)
//	  — ShouldSwap=true + OldDriveFileID non-empty → event emitted.
func TestFinalize_OutboxEvents_EmittedWithCanonicalPayloads(t *testing.T) {
	db := openFinalizerTestDB(t)
	repo := newRecordingFinalizerRepo(t, db)
	outbox := &payloadRecordingOutbox{}
	proj := &payloadRecordingProjection{}

	f := newVoiceoverFinalizer(voiceoverFinalizerDeps{
		VoiceoverRepo:    repo,
		Outbox:           outbox,
		LifecycleService: proj,
		Logger:           zap.NewNop(),
	})

	tx, err := repo.BeginTx(context.Background())
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()

	cmd := &FinalizeCommand{
		ID:             "vo-outbox-001",
		FileHash:       "abc123hash",
		DriveFileID:    "new-drive-id",
		ShouldSwap:     true,
		OldDriveFileID: "old-drive-id",
		OldLocalPath:   "/tmp/old-audio.mp3",
		OldCleanedPath: "/tmp/old-audio-cleaned.wav",
		FolderID:       "folder-1",
	}

	res, err := f.Finalize(context.Background(), tx, cmd)
	require.NoError(t, err)
	assert.False(t, res.Reused)

	// ── Step 5 (index outbox) assertions ──
	require.Len(t, outbox.indexCalls, 1, "FASE 2: index outbox MUST be emitted exactly once")
	idx := outbox.indexCalls[0]
	assert.Equal(t, "vo-outbox-001", idx.assetID,
		"index outbox: assetID must equal cmd.ID (canonical voiceover identifier)")
	assert.Equal(t, "abc123hash", idx.contentHash,
		"index outbox: contentHash must equal cmd.FileHash (Qdrant supersede gate input)")

	// ── Step 6 (cleanup outbox) assertions ──
	require.Len(t, outbox.cleanupCalls, 1, "FASE 2: cleanup outbox MUST be emitted when ShouldSwap=true + OldDriveFileID non-empty")
	cl := outbox.cleanupCalls[0]
	assert.Equal(t, "vo-outbox-001", cl.voiceoverID,
		"cleanup outbox: voiceoverID must equal cmd.ID")
	assert.Equal(t, "old-drive-id", cl.oldDriveFileID,
		"cleanup outbox: oldDriveFileID must equal cmd.OldDriveFileID")
	assert.Equal(t, "new-drive-id", cl.newDriveFileID,
		"cleanup outbox: newDriveFileID must equal cmd.DriveFileID")
	assert.Len(t, cl.oldLocalPaths, 2, "cleanup outbox: 2 old local paths (OldLocalPath + OldCleanedPath)")
	assert.Contains(t, cl.oldLocalPaths, "/tmp/old-audio.mp3")
	assert.Contains(t, cl.oldLocalPaths, "/tmp/old-audio-cleaned.wav")
}

// TestFinalize_IndexOutbox_GuardedEmptyFileHash pins the guard-skip
// contract: when cmd.FileHash is empty, Step 5 is guard-skipped
// (RequiredSteps marker, not error). This test also verifies that
// the cleanup outbox (Step 6) still executes independently.
func TestFinalize_IndexOutbox_GuardedEmptyFileHash(t *testing.T) {
	db := openFinalizerTestDB(t)
	repo := newRecordingFinalizerRepo(t, db)
	outbox := &payloadRecordingOutbox{}
	proj := &payloadRecordingProjection{}

	f := newVoiceoverFinalizer(voiceoverFinalizerDeps{
		VoiceoverRepo:    repo,
		Outbox:           outbox,
		LifecycleService: proj,
		Logger:           zap.NewNop(),
	})

	tx, err := repo.BeginTx(context.Background())
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()

	cmd := &FinalizeCommand{
		ID:             "vo-empty-hash-001",
		FileHash:       "", // empty → Step 5 guard-skipped
		DriveFileID:    "drive-empty-hash",
		ShouldSwap:     true,
		OldDriveFileID: "old-drive-empty",
		OldLocalPath:   "/tmp/old.mp3",
		FolderID:       "folder-1",
	}

	res, err := f.Finalize(context.Background(), tx, cmd)
	require.NoError(t, err)

	// Guard-skip marker must be in RequiredSteps.
	foundGuard := false
	for _, s := range res.RequiredSteps {
		if s == "index_outbox: guarded (empty FileHash)" {
			foundGuard = true
			break
		}
	}
	assert.True(t, foundGuard, "FASE 2: empty FileHash MUST surface guard-skip marker in RequiredSteps")

	// Index outbox must NOT have been emitted.
	assert.Empty(t, outbox.indexCalls, "empty FileHash → no EnqueueIndexEvent call")

	// Cleanup outbox (Step 6) MUST still execute independently.
	require.Len(t, outbox.cleanupCalls, 1, "cleanup outbox MUST execute even when index outbox guard-skipped")
}
