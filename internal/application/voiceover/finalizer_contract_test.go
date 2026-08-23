// Package voiceover — finalizer_test.go (P0.4 Fase 3a, July 2026).
//
// Tests the unified VoiceoverFinalizer delegation through both paths.
// Uses in-memory SQLite for real tx lifecycle (BeginTx/Commit/Rollback)
// so the test exercises the full finalizeStage flow without panicking
// on a zero-value *sql.Tx.
package voiceover

import (
	"context"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

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
		ID:            "vo-new-001",
		RequestID:     "req-1",
		TextHash:      "hash",
		Text:          "hello",
		Language:      "en",
		Voice:         "en_female",
		Filename:      "test.mp3",
		LocalPath:     "/tmp/test.mp3",
		DriveFileID:   "drive-1",
		LegacyFileMD5: "abc123",
		FolderID:      "folder-1",
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
		ID:            "vo-amb-001",
		DriveFileID:   "drive-amb",
		LegacyFileMD5: "hash",
		FolderID:      "folder-1",
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
		LegacyFileMD5:  "abc123",
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
		LegacyFileMD5:  "abc123",
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
		ID:            "vo-proj-001",
		Text:          "This is a test voiceover for the projection contract.",
		Filename:      "projection-test.mp3",
		FolderID:      "folder-proj-001",
		FolderPath:    "/tmp/vo/proj",
		LocalPath:     "/tmp/vo/proj/output.mp3",
		DriveFileID:   "drive-proj-001",
		DriveLink:     "https://drive.google.com/file/d/drive-proj-001/view",
		DownloadLink:  "https://drive.google.com/uc?id=drive-proj-001",
		LegacyFileMD5: "sha256-proj-hash-001",
		Language:      "it-IT",
		MetaJSON:      []byte(`{"style_group":"cinematic"}`),
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
	assert.Equal(t, "sha256-proj-hash-001", in.LegacyFileMD5)
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
//	Step 5 (index outbox): EnqueueIndexEvent(ctx, tx, cmd.ID, cmd.LegacyFileMD5)
//	  — the assetID is the voiceover's canonical ID; contentHash is
//	    cmd.LegacyFileMD5 for the Qdrant supersede gate.
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
		LegacyFileMD5:  "abc123hash",
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
		"index outbox: contentHash must equal cmd.LegacyFileMD5 (Qdrant supersede gate input)")

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
// contract: when cmd.LegacyFileMD5 is empty, Step 5 is guard-skipped
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
		LegacyFileMD5:  "", // empty → Step 5 guard-skipped
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
		if s == "index_outbox: guarded (empty LegacyFileMD5)" {
			foundGuard = true
			break
		}
	}
	assert.True(t, foundGuard, "FASE 2: empty LegacyFileMD5 MUST surface guard-skip marker in RequiredSteps")

	// Index outbox must NOT have been emitted.
	assert.Empty(t, outbox.indexCalls, "empty LegacyFileMD5 → no EnqueueIndexEvent call")

	// Cleanup outbox (Step 6) MUST still execute independently.
	require.Len(t, outbox.cleanupCalls, 1, "cleanup outbox MUST execute even when index outbox guard-skipped")
}
