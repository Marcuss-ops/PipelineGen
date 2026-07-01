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
// P0.4 Fase 3b: SkippedSteps tracking
// ─────────────────────────────────────────────────────────────────────

// TestFinalizeResult_TracksSkippedSteps exercises the real
// voiceoverFinalizer with various dep configurations and asserts
// that SkippedSteps correctly records which optional steps were
// bypassed. The test uses in-memory SQLite so Commit/Rollback work.
func TestFinalizeResult_TracksSkippedSteps(t *testing.T) {
	db := openFinalizerTestDB(t)
	repo := &finalizerTestRepo{db: db}

	t.Run("all wired with DriveFileID and FileHash", func(t *testing.T) {
		// Full wiring: all steps should execute, SkippedSteps empty.
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
		assert.Empty(t, res.SkippedSteps, "all deps wired + all fields present → no steps skipped")
	})

	t.Run("empty DriveFileID skips dedupe", func(t *testing.T) {
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
			ID:         "vo-2",
			RequestID:  "req-2",
			TextHash:   "hash",
			Text:       "hello",
			Language:   "en",
			Voice:      "en_female",
			Filename:   "test.mp3",
			LocalPath:  "/tmp/test.mp3",
			DriveFileID: "", // empty → dedupe skipped
			FileHash:    "abc123",
			FolderID:    "folder-1",
		})
		require.NoError(t, err)
		assert.Contains(t, res.SkippedSteps, "dedupe: empty DriveFileID")
		// media_assets + index should still run
		assert.NotContains(t, res.SkippedSteps, "media_assets_projection")
		assert.NotContains(t, res.SkippedSteps, "index_outbox")
	})

	t.Run("unwired LifecycleService skips projection", func(t *testing.T) {
		f := newVoiceoverFinalizer(voiceoverFinalizerDeps{
			VoiceoverRepo:    repo,
			Outbox:           &stubOutboxEnqueuer{},
			LifecycleService: nil, // unwired → projection skipped
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
			FileHash:    "abc123",
			FolderID:    "folder-1",
		})
		require.NoError(t, err)
		assert.Contains(t, res.SkippedSteps, "media_assets_projection: unwired")
	})

	t.Run("unwired Outbox skips both index and cleanup", func(t *testing.T) {
		f := newVoiceoverFinalizer(voiceoverFinalizerDeps{
			VoiceoverRepo:    repo,
			Outbox:           nil, // unwired → both outbox steps skipped
			LifecycleService: &stubProjectionUpserter{},
			Logger:           zap.NewNop(),
		})

		tx, err := repo.BeginTx(context.Background())
		require.NoError(t, err)
		defer func() { _ = tx.Rollback() }()

		res, err := f.Finalize(context.Background(), tx, &FinalizeCommand{
			ID:          "vo-4",
			RequestID:   "req-4",
			TextHash:    "hash",
			Text:        "hello",
			Language:    "en",
			Voice:       "en_female",
			Filename:    "test.mp3",
			LocalPath:   "/tmp/test.mp3",
			DriveFileID: "drive-3",
			FileHash:    "abc123",
			FolderID:    "folder-1",
		})
		require.NoError(t, err)
		assert.Contains(t, res.SkippedSteps, "index_outbox: unwired")
		assert.Contains(t, res.SkippedSteps, "cleanup_outbox: ShouldSwap=false")
	})

	t.Run("empty FileHash skips index outbox only", func(t *testing.T) {
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
			ID:          "vo-5",
			RequestID:   "req-5",
			TextHash:    "hash",
			Text:        "hello",
			Language:    "en",
			Voice:       "en_female",
			Filename:    "test.mp3",
			LocalPath:   "/tmp/test.mp3",
			DriveFileID: "drive-4",
			FileHash:    "", // empty → index outbox skipped
			FolderID:    "folder-1",
		})
		require.NoError(t, err)
		assert.Contains(t, res.SkippedSteps, "index_outbox: empty FileHash")
		// cleanup is also skipped because ShouldSwap=false
		assert.NotContains(t, res.SkippedSteps, "index_outbox: unwired")
	})

	t.Run("ShouldSwap false skips cleanup outbox", func(t *testing.T) {
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
			ShouldSwap:  false, // cleanup skipped
		})
		require.NoError(t, err)
		assert.Contains(t, res.SkippedSteps, "cleanup_outbox: ShouldSwap=false")
		// index should still run
		assert.NotContains(t, res.SkippedSteps, "index_outbox")
	})

	t.Run("ShouldSwap true with unwired Outbox skips cleanup", func(t *testing.T) {
		f := newVoiceoverFinalizer(voiceoverFinalizerDeps{
			VoiceoverRepo:    repo,
			Outbox:           nil, // unwired
			LifecycleService: &stubProjectionUpserter{},
			Logger:           zap.NewNop(),
		})

		tx, err := repo.BeginTx(context.Background())
		require.NoError(t, err)
		defer func() { _ = tx.Rollback() }()

		res, err := f.Finalize(context.Background(), tx, &FinalizeCommand{
			ID:             "vo-7",
			RequestID:      "req-7",
			TextHash:       "hash",
			Text:           "hello",
			Language:       "en",
			Voice:          "en_female",
			Filename:       "test.mp3",
			LocalPath:      "/tmp/test.mp3",
			DriveFileID:    "drive-6",
			FileHash:       "abc123",
			FolderID:       "folder-1",
			ShouldSwap:     true,
			OldDriveFileID: "old-drive-1",
			OldLocalPath:   "/tmp/old.mp3",
		})
		require.NoError(t, err)
		assert.Contains(t, res.SkippedSteps, "index_outbox: unwired")
		assert.Contains(t, res.SkippedSteps, "cleanup_outbox: unwired")
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
