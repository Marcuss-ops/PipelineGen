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
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
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

func (r *stubProcessVoRepo) InsertTx(ctx context.Context, tx *sql.Tx, rec *VoiceoverRecord) error {
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
		rec.DriveLink, rec.DownloadLink, rec.FileHash, rec.Status, rec.Error, rec.Strategy,
		rec.Metadata, rec.IdempotencyKey, rec.JobID, rec.CreatedAt, rec.UpdatedAt,
	)
	return err
}

func (r *stubProcessVoRepo) DeleteByIDTx(ctx context.Context, tx *sql.Tx, id string) error {
	_, err := tx.ExecContext(ctx, `DELETE FROM voiceovers WHERE id = ?`, id)
	return err
}

func (r *stubProcessVoRepo) PreReadByID(_ context.Context, _ string) (*VoiceoverRecord, error) {
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

var _ VoiceoverRepository = (*stubProcessVoRepo)(nil)

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
			FileHash:    "abc123",
		},
	}
	dest := &stubProcessDestResolver{folderID: "folder-1"}
	audioPost := &stubProcessAudioPost{
		cannedOut: AudioPostOutput{CleanedPath: "/tmp/vo/cleaned_test_en.mp3"},
	}
	pub := &stubProcessPublisher{fileID: "drive-123"}
	finalizer := &stubProcessFinalizer{
		cannedRes: &FinalizeResult{ID: "test-id", Reused: false},
	}

	uc := NewProcessVoiceoverItemUseCase(ProcessVoiceoverItemDeps{
		TTSProvider:         tts,
		DestinationResolver: dest,
		AudioPostProcessor:  audioPost,
		Publisher:           pub,
		VoiceoverRepository: &stubProcessVoRepo{db: db},
		Finalizer:           finalizer,
		Logger:              zap.NewNop(),
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
}

// TestRemoveSilence_SkipsPostProcessingWhenFalse ensures that when
// item.RemoveSilence is false, AudioPostProcessor is NOT invoked at all.
func TestRemoveSilence_SkipsPostProcessingWhenFalse(t *testing.T) {
	db := openProcessTestDB(t)

	tts := &stubProcessTTS{
		cannedOut: TTSOutput{
			LocalPath: "/tmp/vo/test_en.mp3",
			Voice:     "en_female",
			FileHash:  "abc123",
		},
	}
	dest := &stubProcessDestResolver{folderID: "folder-1"}
	audioPost := &stubProcessAudioPost{} // never called — verify 0 invocations
	pub := &stubProcessPublisher{fileID: "drive-123"}
	finalizer := &stubProcessFinalizer{
		cannedRes: &FinalizeResult{ID: "test-id", Reused: false},
	}

	uc := NewProcessVoiceoverItemUseCase(ProcessVoiceoverItemDeps{
		TTSProvider:         tts,
		DestinationResolver: dest,
		AudioPostProcessor:  audioPost,
		Publisher:           pub,
		VoiceoverRepository: &stubProcessVoRepo{db: db},
		Finalizer:           finalizer,
		Logger:              zap.NewNop(),
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
			FileHash:  "abc123",
		},
	}
	dest := &stubProcessDestResolver{folderID: "folder-1"}
	pub := &stubProcessPublisher{fileID: "drive-123"}
	finalizer := &stubProcessFinalizer{
		cannedRes: &FinalizeResult{ID: "test-id", Reused: false},
	}

	uc := NewProcessVoiceoverItemUseCase(ProcessVoiceoverItemDeps{
		TTSProvider:         tts,
		DestinationResolver: dest,
		AudioPostProcessor:  nil,
		Publisher:           pub,
		VoiceoverRepository: &stubProcessVoRepo{db: db},
		Finalizer:           finalizer,
		Logger:              zap.NewNop(),
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
