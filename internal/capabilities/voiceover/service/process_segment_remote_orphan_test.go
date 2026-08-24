package voiceover

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	joboutbox "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs"
	outboxdispatcher "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/outbox"
	outboxevents "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/outboxevents"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/voiceover/service/persistence"
)

// remoteOrphanRepo delegates the real E2E repository to the finalizer
// contract but fails only InsertTx. This makes the Drive publish succeed
// while forcing the caller-owned finalization transaction to roll back.
type remoteOrphanRepo struct {
	persistence.Repository
	insertErr error
}

func (r *remoteOrphanRepo) InsertTx(_ context.Context, _ *sql.Tx, _ *persistence.VoiceoverRecord) error {
	return r.insertErr
}

var _ persistence.Repository = (*remoteOrphanRepo)(nil)

// TestProcessSegmentUseCase_RemoteDriveOrphan_FinalizeRollbackPersistsCleanupOutbox
// verifies the complete failure path with the real SQLite outbox dispatcher:
//
//  1. TTS creates a local artifact.
//  2. Publisher returns a remote Drive file ID.
//  3. DB finalization fails at InsertTx and its transaction is rolled back.
//  4. A fresh cleanup transaction commits voiceover.cleanup.requested.
//  5. The durable event contains the remote orphan ID and remains pending
//     for the cleanup worker; the voiceovers row does not exist.
//
// The existing FASE4/FASE6 tests validate the same flow with an outbox
// recorder. This test intentionally uses the production Dispatcher and
// queries outbox_events after the finalization rollback, which proves the
// cleanup event is durable rather than merely observed in memory.
func TestProcessSegmentUseCase_RemoteDriveOrphan_FinalizeRollbackPersistsCleanupOutbox(t *testing.T) {
	db := qdrantE2EDB(t)
	db.SetMaxOpenConns(1)
	ctx := context.Background()

	const (
		voiceoverID = "vo-remote-orphan-e2e"
		driveFileID = "drive-remote-orphan-e2e"
		fileHash    = "hash-remote-orphan-e2e"
	)

	baseRepo := newE2ERepoAdapter(db)
	repo := &remoteOrphanRepo{
		Repository: baseRepo,
		insertErr:  errors.New("sqlite: simulated constraint failure on voiceovers insert"),
	}
	outboxDispatcher := outboxdispatcher.NewDispatcher(
		nil,
		nil,
		outboxevents.NewRepository(db),
		nil,
		zap.NewNop(),
	)
	finalizer := NewVoiceoverFinalizer(
		repo,
		outboxDispatcher,
		&stubLifecycleProjectionUpserter{},
		nil,
		zap.NewNop(),
	)

	localPath := t.TempDir() + "/remote-orphan.mp3"
	require.NoError(t, os.WriteFile(localPath, []byte("orphan audio"), 0o600))
	cleanupDriver := &remoteOrphanCleanupDriver{}

	uc := NewProcessSegmentUseCase(ProcessSegmentDeps{
		TTSProvider: &e2eTTSProvider{
			localPath: localPath,
			fileHash:  fileHash,
		},
		Publisher:           &e2ePublisherStub{fileID: driveFileID},
		VoiceoverRepository: repo,
		Finalizer:           finalizer,
		TxOutboxEnqueuer:    outboxDispatcher,
		Logger:              zap.NewNop(),
	})

	out, err := uc.Execute(ctx, &ProcessSegmentCommand{
		JobID:     "job-remote-orphan-e2e",
		ID:        voiceoverID,
		RequestID: "request-remote-orphan-e2e",
		Text:      "Remote Drive orphan cleanup test.",
		Language:  "en",
		Voice:     "en-US-Test",
		Filename:  "remote-orphan.mp3",
		TextHash:  "hash-remote-orphan-text",
		Strategy:  "replace",
		Dest: &ResolvedDestination{
			FolderID:   "folder-remote-orphan-e2e",
			FolderPath: t.TempDir(),
		},
	})

	require.Error(t, err, "DB finalization failure must not report success")
	require.NotNil(t, out)
	require.Equal(t, StatusFailed, out.Status)
	require.Contains(t, out.Error, "finalize_failed:")
	require.Equal(t, driveFileID, out.DriveFileID,
		"the remote file ID must remain available for orphan cleanup")

	var voiceoverRows int
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM voiceovers WHERE id = ?`, voiceoverID).Scan(&voiceoverRows))
	require.Equal(t, 0, voiceoverRows,
		"the failed finalization transaction must not leave a voiceovers row")

	var (
		eventType string
		status    string
		payload   string
		eventKey  string
	)
	require.NoError(t, db.QueryRowContext(ctx, `
		SELECT event_type, status, payload_json, event_key
		  FROM outbox_events
		 WHERE event_type = ? AND aggregate_id = ?
		 ORDER BY id DESC LIMIT 1
	`, outboxevents.EventVoiceoverCleanupRequested, voiceoverID).
		Scan(&eventType, &status, &payload, &eventKey))

	require.Equal(t, outboxevents.EventVoiceoverCleanupRequested, eventType)
	require.Equal(t, "pending", status,
		"the committed cleanup event must remain pending for the cleanup worker")
	require.Equal(t, "voiceover_cleanup:"+voiceoverID+":"+driveFileID, eventKey)

	var envelope struct {
		SchemaVersion  string   `json:"schema_version"`
		VoiceoverID    string   `json:"voiceover_id"`
		OldDriveFileID string   `json:"old_drive_file_id"`
		NewDriveFileID string   `json:"new_drive_file_id"`
		OldLocalPaths  []string `json:"old_local_paths"`
		IdempotencyKey string   `json:"idempotency_key"`
	}
	require.NoError(t, json.Unmarshal([]byte(payload), &envelope))
	require.Equal(t, outboxdispatcher.VoiceoverCleanupSchemaVersion, envelope.SchemaVersion)
	require.Equal(t, voiceoverID, envelope.VoiceoverID)
	require.Equal(t, driveFileID, envelope.OldDriveFileID)
	require.Empty(t, envelope.NewDriveFileID)
	require.Equal(t, eventKey, envelope.IdempotencyKey)
	require.Len(t, envelope.OldLocalPaths, 1)
	require.Contains(t, envelope.OldLocalPaths[0], "remote-orphan.mp3")

	// Consume the durable event through the production handler. This
	// proves the event shape is actionable, not merely persisted.
	cleanupHandler := joboutbox.NewVoiceoverCleanupHandler(cleanupDriver, zap.NewNop())
	event := outboxevents.Event{
		EventType:   eventType,
		AggregateID: voiceoverID,
		PayloadJSON: payload,
		EventKey:    eventKey,
	}
	require.NoError(t, cleanupHandler.Handle(ctx, event))
	require.Equal(t, []string{driveFileID}, cleanupDriver.deleteCalls)
	_, statErr := os.Stat(localPath)
	require.ErrorIs(t, statErr, os.ErrNotExist)
}

type remoteOrphanCleanupDriver struct {
	deleteCalls []string
}

func (d *remoteOrphanCleanupDriver) DeleteFile(_ context.Context, fileID string) error {
	d.deleteCalls = append(d.deleteCalls, fileID)
	return nil
}
