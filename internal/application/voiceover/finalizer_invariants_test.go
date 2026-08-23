// Package voiceover — finalizer_invariants_test.go (P0.4 Fase 3a, July 2026).
//
// Tests the unified VoiceoverFinalizer delegation and required-step
// invariants through the canonical per-item and finalizer paths.
// Uses in-memory SQLite for real tx lifecycle (BeginTx/Commit/Rollback)
// so the tests exercise the caller-owned transaction contract.
package voiceover

import (
	"context"
	"errors"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// the panic string to "Finalizer is nil") surfaces at test time rather
// than silently absorbing the change.
func TestFinalizer_NilConstructorPanic(t *testing.T) {
	db := openFinalizerTestDB(t)
	require.PanicsWithValue(
		t, "voiceover.NewProcessSegmentUseCase: Finalizer is required (P0.4 Fase 3a — unified finalization port)",
		func() {
			_ = NewProcessSegmentUseCase(ProcessSegmentDeps{
				TTSProvider:         &stubProcessTTS{cannedOut: TTSOutput{LocalPath: "/tmp/x.mp3", Voice: "v", LegacyFileMD5: "h"}},
				Publisher:           &stubProcessPublisher{fileID: "x"},
				VoiceoverRepository: &finalizerTestRepo{db: db},
				Finalizer:           nil, // KEY: nil Finalizer — must panic with typed message
				Logger:              zap.NewNop(),
			})
		},
		"NewProcessSegmentUseCase MUST panic with typed message when Finalizer is nil (godlike/07 fail-fast at construction, panic-value regression guard)",
	)
}

// TestFinalizerDelegation_UsesProcessSegmentExecute pins the canonical delegation
// invariants for ProcessSegmentUseCase.Execute.
func TestFinalizerDelegation_UsesProcessSegmentExecute(t *testing.T) {
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
		// The historical post-commit verifier cases belonged to a retired
		// batch path. The canonical per-item pipeline is verifier-unaware,
		// so this table pins only that current invariant:
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
					LocalPath:     "/tmp/vo/migrated-" + c.name + ".mp3",
					Voice:         "en_female",
					LegacyFileMD5: "migrated-hash-" + c.name,
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

// TestFinalizerResult_TracksOptionalAndRequiredSteps exercises the
// real voiceoverFinalizer with various dep configurations and
// asserts the audit-P0 #2 split tracking contract. The test uses
// in-memory SQLite so Commit/Rollback work.
func TestFinalizerResult_TracksOptionalAndRequiredSteps(t *testing.T) {
	db := openFinalizerTestDB(t)
	repo := &finalizerTestRepo{db: db}

	t.Run("all wired with DriveFileID and LegacyFileMD5", func(t *testing.T) {
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
			LegacyFileMD5:  "abc123",
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
			ID:            "vo-2",
			RequestID:     "req-2",
			TextHash:      "hash",
			Text:          "hello",
			Language:      "en",
			Voice:         "en_female",
			Filename:      "test.mp3",
			LocalPath:     "/tmp/test.mp3",
			DriveFileID:   "", // empty → Step 1 guard-skipped
			LegacyFileMD5: "abc123",
			FolderID:      "folder-1",
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

	t.Run("empty LegacyFileMD5 guard-skips index outbox", func(t *testing.T) {
		// RequiredStep data-state guard (LegacyFileMD5 empty) — recordable
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
			ID:            "vo-3",
			RequestID:     "req-3",
			TextHash:      "hash",
			Text:          "hello",
			Language:      "en",
			Voice:         "en_female",
			Filename:      "test.mp3",
			LocalPath:     "/tmp/test.mp3",
			DriveFileID:   "drive-2",
			LegacyFileMD5: "", // empty → Step 5 guard-skipped
			FolderID:      "folder-1",
		})
		require.NoError(t, err)
		assert.Empty(t, res.OptionalSteps)
		assert.Equal(t, []string{
			"media_assets_projection: executed",
			"index_outbox: guarded (empty LegacyFileMD5)",
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
			ID:            "vo-6",
			RequestID:     "req-6",
			TextHash:      "hash",
			Text:          "hello",
			Language:      "en",
			Voice:         "en_female",
			Filename:      "test.mp3",
			LocalPath:     "/tmp/test.mp3",
			DriveFileID:   "drive-5",
			LegacyFileMD5: "abc123",
			FolderID:      "folder-1",
			ShouldSwap:    true, // no old artefacts → Step 6 guard-skipped
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

// TestFinalizer_RequiredStepNotWired_FailsFast pins the audit-P0 #2
// fail-fast contract: unwired required deps surface as a typed
// error rather than degrading to SkippedSteps-style reports of
// the form "<step>: unwired".
func TestFinalizer_RequiredStepNotWired_FailsFast(t *testing.T) {
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
			ID:            "vo-lifecycle-unwired",
			RequestID:     "req-lu",
			TextHash:      "hash",
			Text:          "hello",
			Language:      "en",
			Voice:         "en_female",
			Filename:      "test.mp3",
			LocalPath:     "/tmp/test.mp3",
			DriveFileID:   "drive-1",
			LegacyFileMD5: "abc123",
			FolderID:      "folder-1",
			ShouldSwap:    true,
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
			ID:            "vo-outbox-unwired",
			RequestID:     "req-ou",
			TextHash:      "hash",
			Text:          "hello",
			Language:      "en",
			Voice:         "en_female",
			Filename:      "test.mp3",
			LocalPath:     "/tmp/test.mp3",
			DriveFileID:   "drive-2",
			LegacyFileMD5: "abc123",
			FolderID:      "folder-1",
			ShouldSwap:    true,
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
			ID:            "vo-all-wired",
			RequestID:     "req-aw",
			TextHash:      "hash",
			Text:          "hello",
			Language:      "en",
			Voice:         "en_female",
			Filename:      "test.mp3",
			LocalPath:     "/tmp/test.mp3",
			DriveFileID:   "drive-aw",
			LegacyFileMD5: "abc123",
			FolderID:      "folder-1",
			ShouldSwap:    true,
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
// stubOutboxEnqueuer for required-step invariant tests
// ─────────────────────────────────────────────────────────────────────

type stubOutboxEnqueuer struct{}
