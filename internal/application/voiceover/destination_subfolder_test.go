package voiceover

import (
	"context"
	"database/sql"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/lifecycle"
	"github.com/Marcuss-ops/PipelineGen/internal/application/voiceover/persistence"
)

// fakePublisher is the F2.7 stub for delivery.Publisher passed to
// lifecycle.NewService in this test. It returns a fixed upload result
// so destinationStage can complete without a real Drive backend.
//
// P0.4 Fase 3a (July 2026): lifecycleSvc (with this publisher) is
// now shared between destinationStage (UploadOnly → Publish) and
// the finalizer (UpsertVoiceoverProjectionTx — direct SQL, no
// publisher involvement). Folder resolution is internal to
// lifecycle/delivery — the test doesn't assert on the resolved
// folder ID because destinationStage no longer mutates dest.
type fakePublisher struct{}

var _ delivery.Publisher = (*fakePublisher)(nil)

func (*fakePublisher) Publish(_ context.Context, _ delivery.PublishRequest) (*delivery.PublishResult, error) {
	return &delivery.PublishResult{
		FileID:       "test-file-id",
		WebViewLink:  "https://drive.google.com/file/d/test-file-id/view",
		DownloadLink: "https://drive.google.com/uc?id=test-file-id",
		Action:       delivery.PublishActionCreated,
	}, nil
}

func (*fakePublisher) ResolveFolder(_ context.Context, _ delivery.PublishRequest) (string, error) {
	return "child-folder-id", nil
}

type testVoiceoverRepo struct {
	db *sql.DB
}

func (r *testVoiceoverRepo) BeginTx(ctx context.Context) (*sql.Tx, error) {
	return r.db.BeginTx(ctx, nil)
}

func (r *testVoiceoverRepo) InsertTx(ctx context.Context, tx *sql.Tx, rec *persistence.VoiceoverRecord) error {
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

func (r *testVoiceoverRepo) DeleteByIDTx(ctx context.Context, tx *sql.Tx, id string) error {
	_, err := tx.ExecContext(ctx, `DELETE FROM voiceovers WHERE id = ?`, id)
	return err
}

func (r *testVoiceoverRepo) PreReadByID(_ context.Context, _ string) (*persistence.VoiceoverRecord, error) {
	return nil, nil
}

func (r *testVoiceoverRepo) CountByDriveFileIDTx(_ context.Context, _ *sql.Tx, _ string, _ string) (string, int, error) {
	return "", 0, nil
}

var _ persistence.Repository = (*testVoiceoverRepo)(nil)

func TestDestinationStageCreatesAndPersistsSubfolder(t *testing.T) {
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
		);
		CREATE TABLE media_assets (
			id TEXT PRIMARY KEY,
			source TEXT NOT NULL DEFAULT '',
			name TEXT NOT NULL DEFAULT '',
			filename TEXT NOT NULL DEFAULT '',
			folder_id TEXT NOT NULL DEFAULT '',
			folder_path TEXT NOT NULL DEFAULT '',
			media_type TEXT NOT NULL DEFAULT '',
			local_path TEXT NOT NULL DEFAULT '',
			drive_file_id TEXT NOT NULL DEFAULT '',
			drive_link TEXT NOT NULL DEFAULT '',
			download_link TEXT NOT NULL DEFAULT '',
			file_hash TEXT NOT NULL DEFAULT '',
			language TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT '',
			metadata_json TEXT NOT NULL DEFAULT '',
			updated_at TEXT NOT NULL DEFAULT ''
		);
	`)
	require.NoError(t, err)

	// P0.4 Fase 3a (July 2026): finalizeStage now requires a
	// VoiceoverFinalizer. Wire a real finalizer backed by the same
	// in-memory DB so the test exercises the full 6-step sequence.
	//
	// Fase 4a fix (July 2026): wire the lifecycleService into BOTH
	// svc (for destinationStage.UploadOnly) AND the finalizer (for
	// Step 4 media_assets projection UPSERT). Pre-fix, LifecycleService
	// was nil in the finalizer, which caused the media_assets
	// projection to be skipped silently — the test then failed at the
	// media_assets SELECT because no row existed.
	lifecycleSvc := lifecycle.NewService(
		nil,
		&fakePublisher{},
		nil,
		nil,
		nil,
		nil,
		lifecycle.Config{},
		zap.NewNop(),
	)

	finalizer := newVoiceoverFinalizer(voiceoverFinalizerDeps{
		VoiceoverRepo:    &testVoiceoverRepo{db: db},
		Outbox:           nil,               // nil-safe (skip index + cleanup)
		LifecycleService: lifecycleSvc,      // wired for media_assets projection
		Logger:           zap.NewNop(),
	})

	svc := &Service{
		log:              zap.NewNop(),
		voiceoverRepo:    &testVoiceoverRepo{db: db},
		finalizer:        finalizer,
		lifecycleService: lifecycleSvc,
	}

	dest := &ResolvedDestination{
		FolderID:      "root-folder-id",
		FolderPath:    "",
		SubfolderName: "top-10-funny-moments",
	}
	item := BatchItem{
		ID:          "vo-1",
		Language:    "en",
		Filename:    "clip.mp3",
		LocalPath:   "/tmp/clip.mp3",
		CleanedPath: "/tmp/clip.mp3",
		Status:      StatusGenerated,
	}
	req := &BatchRequest{
		Text:     "hello world",
		Strategy: "replace",
	}

	out := svc.destinationStage(context.Background(), item, req, dest, []byte(`{"request_id":"req-1"}`))
	require.Equalf(t, StatusUploaded, out.Status, "destinationStage returned error: %s", out.Error)

	// P0.4 Fase 3a (July 2026): destinationStage no longer mutates
	// dest. Folder resolution/subfolder creation is delegated to
	// lifecycle.UploadOnly (via delivery.Publisher). dest is
	// read-only at this stage — the caller owns its lifetime.
	require.Equal(t, "root-folder-id", dest.FolderID, "dest.FolderID is NOT mutated by destinationStage post-Fase-3a")
	require.Equal(t, "", dest.FolderPath, "dest.FolderPath stays empty — subfolder resolution is internal to lifecycle")

	finalized := svc.finalizeStage(
		context.Background(),
		out,
		"req-1",
		"text-hash-1",
		"en",
		req,
		dest,
		[]byte(`{"request_id":"req-1"}`),
		false,
		"",
		"",
		"",
	)
	require.Equal(t, StatusCompleted, finalized.Status)

	row := db.QueryRow(`
		SELECT folder_id, folder_path
		FROM voiceovers
		WHERE id = ?
	`, item.ID)
	var folderID, folderPath string
	require.NoError(t, row.Scan(&folderID, &folderPath))
	require.Equal(t, "root-folder-id", folderID,
		"voiceovers.folder_id is dest.FolderID (not mutated by destinationStage post-Fase-3a)")
	require.Equal(t, "", folderPath,
		"voiceovers.folder_path is dest.FolderPath (empty — subfolder is internal to lifecycle)")

	assetRow := db.QueryRow(`
		SELECT folder_id, folder_path
		FROM media_assets
		WHERE id = ?
	`, item.ID)
	require.NoError(t, assetRow.Scan(&folderID, &folderPath))
	require.Equal(t, "root-folder-id", folderID,
		"media_assets.folder_id mirrors voiceovers.folder_id (same FinalizeCommand)")
	require.Equal(t, "", folderPath,
		"media_assets.folder_path mirrors voiceovers.folder_path")
}
