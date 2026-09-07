package voiceover

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestProcessSegmentUseCase_Execute_Stage3_Publisher_EmptyProject(t *testing.T) {
	db := openProcessTestDB(t)
	tts := &stubProcessTTS{
		cannedOut: TTSOutput{
			LocalPath:     "/tmp/vo/stage3-empty-proj.mp3",
			Voice:         "it-IT-ElsaNeural",
			LegacyFileMD5: "hash-stage3-ep-001",
		},
	}
	pub := &stubProcessPublisher{fileID: "drive-stage3-empty-proj"}
	finalizer := &stubProcessFinalizer{
		cannedRes: &FinalizeResult{ID: "vo-stage3-empty-proj", Reused: false},
	}

	dest := &stubProcessDestResolver{folderID: "dest-stage3-ep"}
	resolvedDest, err := dest.Resolve(context.Background(),
		&DestinationRequest{FolderID: "dest-stage3-ep"})
	require.NoError(t, err)

	uc := NewProcessSegmentUseCase(ProcessSegmentDeps{
		TTSProvider:         tts,
		Publisher:           pub,
		VoiceoverRepository: &stubProcessVoRepo{db: db},
		Finalizer:           finalizer,
		Logger:              zap.NewNop(),
	})

	cmd := &ProcessSegmentCommand{
		ID:       "vo-stage3-empty-proj",
		Language: "it-IT",
		Text:     "Testo con Project vuoto.",
		Voice:    "it-IT-ElsaNeural",
		Filename: "stage3-empty-proj.mp3",
		Project:  "", // empty — Publisher sees empty string verbatim
		Dest:     resolvedDest,
	}

	out, err := uc.Execute(context.Background(), cmd)

	require.NoError(t, err)
	assert.Equal(t, StatusCompleted, out.Status,
		"empty Project must NOT prevent pipeline completion")

	// FASE 2 contract #5: empty Project MUST be forwarded verbatim.
	require.Len(t, pub.published, 1,
		"Publisher.Publish must be invoked exactly once")
	got := pub.published[0]
	assert.Equal(t, "", got.Project,
		"FASE 2 contract #5: empty cmd.Project MUST be forwarded as empty VoiceoverPublishCommand.Project (verbatim propagation)")
	assert.Equal(t, "it-IT", got.Language,
		"Language must still be forwarded correctly even when Project is empty")

	// Finalizer MUST be invoked exactly once (symmetry with Test 9).
	require.Len(t, finalizer.calls, 1,
		"Finalizer.Finalize must be invoked exactly once even when Project is empty")
}

// ────────────────────────────────────────────────────────────────────────────
// Test 16: FASE 4 — Drive upload OK + Finalize FAIL → cleanup event emitted
// ────────────────────────────────────────────────────────────────────────────

// TestProcessSegmentUseCase_Execute_FASE4_DriveUploadOK_FinalizeFail_EmitsCleanup
// pins the FASE 4 transaction-boundary contract: when Stage 3 (Drive
// upload) succeeds but Stage 4 (Finalize inside a caller-owned tx)
// fails, the use case MUST emit a voiceover.cleanup.requested outbox
// event in a SEPARATE tx (the Finalize tx was rolled back) so the
// orphaned Drive file is eventually cleaned up.
func TestProcessSegmentUseCase_Execute_FASE4_DriveUploadOK_FinalizeFail_EmitsCleanup(t *testing.T) {
	db := openProcessTestDB(t)
	tts := &stubProcessTTS{
		cannedOut: TTSOutput{
			LocalPath:     "/tmp/vo/fase4-orphan.mp3",
			CleanedPath:   "/tmp/vo/fase4-orphan-cleaned.mp3",
			Voice:         "en-US-RogerNeural",
			LegacyFileMD5: "hash-fase4-001",
		},
	}
	pub := &stubProcessPublisher{fileID: "drive-fase4-orphan"}
	finalizer := &stubProcessFinalizer{
		cannedErr: fmt.Errorf("finalizer: simulated DB write failure"),
	}
	outboxStub := &stubTxOutboxEnqueuer{}

	dest := &stubProcessDestResolver{folderID: "dest-fase4"}
	resolvedDest, err := dest.Resolve(context.Background(),
		&DestinationRequest{FolderID: "dest-fase4"})
	require.NoError(t, err)

	uc := NewProcessSegmentUseCase(ProcessSegmentDeps{
		TTSProvider:         tts,
		Publisher:           pub,
		VoiceoverRepository: &stubProcessVoRepo{db: db},
		Finalizer:           finalizer,
		TxOutboxEnqueuer:    outboxStub,
		Logger:              zap.NewNop(),
	})

	cmd := &ProcessSegmentCommand{
		ID:       "vo-fase4-orphan",
		Language: "en",
		Text:     "This voiceover will be orphaned.",
		Voice:    "en-US-RogerNeural",
		Filename: "fase4-orphan.mp3",
		Dest:     resolvedDest,
	}

	out, err := uc.Execute(context.Background(), cmd)

	require.Error(t, err, "FASE 4: Finalize failure MUST return error")
	require.NotNil(t, out)
	assert.Equal(t, StatusFailed, out.Status)
	assert.Contains(t, out.Error, "finalize_failed:")

	// Stage 3 succeeded: DriveFileID is set.
	assert.Equal(t, "drive-fase4-orphan", out.DriveFileID)

	// Publisher + Finalizer were both invoked.
	require.Len(t, pub.published, 1)
	require.Len(t, finalizer.calls, 1)

	// FASE 4 contract: cleanup event emitted in a SEPARATE tx.
	require.Len(t, outboxStub.cleanupEvents, 1,
		"FASE 4: EnqueueCleanupEvent MUST be called exactly once for the orphaned Drive file")
	ce := outboxStub.cleanupEvents[0]
	assert.Equal(t, "vo-fase4-orphan", ce.voiceoverID)
	assert.Equal(t, "drive-fase4-orphan", ce.oldDriveFileID,
		"FASE 4: oldDriveFileID is the cleanup target when no row was finalized")
	assert.Equal(t, "", ce.newDriveFileID,
		"FASE 4: newDriveFileID must be empty because no replacement was finalized")
	assert.Contains(t, ce.oldLocalPaths, "/tmp/vo/fase4-orphan.mp3",
		"FASE 4: oldLocalPaths must contain the TTS local path")
	assert.Contains(t, ce.oldLocalPaths, "/tmp/vo/fase4-orphan-cleaned.mp3",
		"FASE 4: oldLocalPaths must contain the cleaned path")

	// The cleanup tx is separate from the Finalize tx
	// (guaranteed by the production code opening a fresh BeginTx).
	require.NotNil(t, ce.tx, "FASE 4: cleanup event must carry a non-nil tx (fresh BeginTx)")

	// Index events must NOT have been emitted.
	assert.Len(t, outboxStub.indexEvents, 0,
		"FASE 4: EnqueueIndexEvent must NOT be called on orphan-cleanup path")
}

// TestProcessSegmentUseCase_Execute_FASE4_Stage0Failure_NoCleanupEvent
// pins the FASE 4 nil-guard: when Execute fails BEFORE Stage 3
// (e.g. Stage 0 missing-folder), DriveFileID is empty and NO cleanup
// event is emitted (no orphaned Drive file exists).
func TestProcessSegmentUseCase_Execute_FASE4_Stage0Failure_NoCleanupEvent(t *testing.T) {
	db := openProcessTestDB(t)
	tts := &stubProcessTTS{cannedOut: TTSOutput{LocalPath: "/tmp/unused.mp3"}}
	pub := &stubProcessPublisher{fileID: "unused"}
	finalizer := &stubProcessFinalizer{cannedRes: &FinalizeResult{ID: "unused"}}
	outboxStub := &stubTxOutboxEnqueuer{}

	uc := NewProcessSegmentUseCase(ProcessSegmentDeps{
		TTSProvider:         tts,
		Publisher:           pub,
		VoiceoverRepository: &stubProcessVoRepo{db: db},
		Finalizer:           finalizer,
		TxOutboxEnqueuer:    outboxStub,
		Logger:              zap.NewNop(),
	})

	cmd := &ProcessSegmentCommand{
		ID:       "vo-fase4-stage0",
		Language: "en",
		Filename: "stage0.mp3",
		Dest:     nil, // Stage 0 short-circuit
	}

	out, err := uc.Execute(context.Background(), cmd)

	require.Error(t, err)
	require.NotNil(t, out)
	assert.Equal(t, StatusFailed, out.Status)
	assert.Empty(t, out.DriveFileID)

	assert.Len(t, outboxStub.cleanupEvents, 0,
		"FASE 4: Stage 0 failure must NOT emit cleanup (no Drive upload)")
	assert.Len(t, outboxStub.indexEvents, 0)
}

// TestProcessSegmentUseCase_Execute_FASE4_NilOutboxEnqueuer_NoPanic
// pins the FASE 4 nil-safe contract: when TxOutboxEnqueuer is nil
// (pre-FASE-4 callers), the orphan-cleanup path is silently skipped.
