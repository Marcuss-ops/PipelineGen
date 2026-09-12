// Package voiceover — e2e_fixtures.go: shared test fixtures for the
// voiceover service tests that drive the production SQLite outbox
// dispatcher (in-memory DB, real repository adapter, TTS/Drive stubs).
//
// Extracted 2026-09-12 from the demolished qdrant_indexing_e2e_test.go
// (the IndexingHandler consumer pipeline it tested is retired with the
// PostgreSQL media cutover); the fixtures remain in use by surviving
// tests (e.g. process_segment_remote_orphan_test.go).
package voiceover

import (
	"context"
	"database/sql"
	"testing"

	sqassets "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/assets/imagesregistry"
	timeutil "github.com/Marcuss-ops/PipelineGen/pkg/timeutil"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/voiceover/service/persistence"
)

const qdrantE2ESchema = `
CREATE TABLE IF NOT EXISTS voiceovers (
    id TEXT PRIMARY KEY,
    request_id TEXT NOT NULL DEFAULT '',
    text_hash TEXT NOT NULL DEFAULT '',
    text_preview TEXT NOT NULL DEFAULT '',
    language TEXT NOT NULL DEFAULT 'it',
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
    fingerprint TEXT NOT NULL DEFAULT '',
    duration_seconds REAL NOT NULL DEFAULT 0.0,
    status TEXT NOT NULL DEFAULT 'pending',
    error TEXT NOT NULL DEFAULT '',
    strategy TEXT NOT NULL DEFAULT '',
    metadata TEXT NOT NULL DEFAULT '{}',
    idempotency_key TEXT NOT NULL DEFAULT '',
    job_id TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS outbox_events (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    event_type TEXT NOT NULL,
    aggregate_id TEXT NOT NULL DEFAULT '',
    aggregate_type TEXT NOT NULL DEFAULT '',
    payload_json TEXT NOT NULL DEFAULT '',
    event_key TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'pending',
    attempt_count INTEGER NOT NULL DEFAULT 0,
    max_attempts INTEGER NOT NULL DEFAULT 10,
    priority INTEGER NOT NULL DEFAULT 5,
    last_error TEXT NOT NULL DEFAULT '',
    next_attempt_at TEXT,
    worker_id TEXT NOT NULL DEFAULT '',
    lease_id TEXT NOT NULL DEFAULT '',
    lease_expiry TEXT,
    completed_at TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL DEFAULT ''
);

CREATE UNIQUE INDEX IF NOT EXISTS ux_outbox_events_event_key
    ON outbox_events(event_key);

CREATE TABLE IF NOT EXISTS media_assets (
    id TEXT PRIMARY KEY,
    source TEXT NOT NULL DEFAULT '',
    name TEXT NOT NULL DEFAULT '',
    filename TEXT NOT NULL DEFAULT '',
    media_type TEXT NOT NULL DEFAULT '',
    local_path TEXT NOT NULL DEFAULT '',
    drive_file_id TEXT NOT NULL DEFAULT '',
    drive_link TEXT NOT NULL DEFAULT '',
    download_link TEXT NOT NULL DEFAULT '',
    file_hash TEXT NOT NULL DEFAULT '',
    lifecycle_state TEXT NOT NULL DEFAULT '',
    index_state TEXT NOT NULL DEFAULT '',
    metadata_json TEXT NOT NULL DEFAULT '{}',
    created_at TEXT NOT NULL DEFAULT '',
    updated_at TEXT NOT NULL DEFAULT ''
);
`

// qdrantE2EDB spins up a fresh in-memory SQLite database with the
// minimal schema required by the voiceover → outbox → Qdrant chain.
func qdrantE2EDB(t *testing.T) *sql.DB {

	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open in-memory sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(qdrantE2ESchema); err != nil {
		t.Fatalf("create qdrant E2E schema: %v", err)
	}
	// media_assets included in qdrantE2ESchema above; no separate canonical constant needed.
	return db
}

// ─────────────────────────────────────────────────────────────────────
// Test-local persistence.Repository adapter.
//
// Mirrors the production useCaseRepoAdapter (internal/app/) without
// pulling the cross-package dependency into the test surface. The
// application-layer VoiceoverRecord (RFC3339 string timestamps) is
// converted to the infrastructure-layer sqassets.Record (time.Time)
// under the hood, exactly as the production adapter does.
// ─────────────────────────────────────────────────────────────────────

type e2eRepoAdapter struct {
	db   *sql.DB
	repo *sqassets.VoiceoversRepository
}

func newE2ERepoAdapter(db *sql.DB) *e2eRepoAdapter {
	return &e2eRepoAdapter{db: db, repo: sqassets.NewVoiceoversRepository(db)}
}

func (a *e2eRepoAdapter) BeginTx(ctx context.Context) (*sql.Tx, error) {
	return a.db.BeginTx(ctx, nil)
}

func (a *e2eRepoAdapter) CountByDriveFileIDTx(
	ctx context.Context,
	tx *sql.Tx,
	currentID string,
	driveFileID string,
) (string, int, error) {
	if driveFileID == "" || tx == nil {
		return "", 0, nil
	}
	row := tx.QueryRowContext(ctx, `
		SELECT id FROM voiceovers
		 WHERE drive_file_id = ? AND id != ?
		 LIMIT 1
	`, driveFileID, currentID)
	var matchedID string
	if err := row.Scan(&matchedID); err != nil {
		if err == sql.ErrNoRows {
			return "", 0, nil
		}
		return "", 0, err
	}
	var count int
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM voiceovers WHERE drive_file_id = ? AND id != ?`,
		driveFileID, currentID,
	).Scan(&count); err != nil {
		// Degrade to count=1 (mirrors the production adapter's
		// graceful-degrade contract on a transient COUNT failure).
		return matchedID, 1, nil
	}
	return matchedID, count, nil
}

func (a *e2eRepoAdapter) InsertTx(ctx context.Context, tx *sql.Tx, rec *persistence.VoiceoverRecord) error {
	if rec == nil {
		return errNilRecord
	}
	infraRec := &sqassets.Record{
		ID:              rec.ID,
		RequestID:       rec.RequestID,
		TextHash:        rec.TextHash,
		TextPreview:     rec.TextPreview,
		Language:        rec.Language,
		Voice:           rec.Voice,
		Filename:        rec.Filename,
		LocalPath:       rec.LocalPath,
		CleanedPath:     rec.CleanedPath,
		FolderID:        rec.FolderID,
		FolderPath:      rec.FolderPath,
		DriveFileID:     rec.DriveFileID,
		DriveLink:       rec.DriveLink,
		DownloadLink:    rec.DownloadLink,
		LegacyFileMD5:   rec.LegacyFileMD5,
		DurationSeconds: 0,
		Status:          rec.Status,
		Error:           rec.Error,
		Strategy:        rec.Strategy,
		Metadata:        rec.Metadata,
		Fingerprint:     "",
		CreatedAt:       parseTimeOrNow(rec.CreatedAt),
		UpdatedAt:       parseTimeOrNow(rec.UpdatedAt),
	}
	return a.repo.InsertTx(ctx, tx, infraRec)
}

func (a *e2eRepoAdapter) DeleteByIDTx(ctx context.Context, tx *sql.Tx, id string) error {
	return a.repo.DeleteByIDTx(ctx, tx, id)
}

func (a *e2eRepoAdapter) FindByIdempotencyKeyTx(ctx context.Context, tx *sql.Tx, idempotencyKey string) (string, error) {
	if idempotencyKey == "" {
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

func (a *e2eRepoAdapter) PreReadByID(ctx context.Context, id string) (*persistence.VoiceoverRecord, error) {
	r, err := a.repo.PreReadByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if r == nil {
		return nil, nil
	}
	return &persistence.VoiceoverRecord{
		ID:            r.ID,
		RequestID:     r.RequestID,
		TextHash:      r.TextHash,
		TextPreview:   r.TextPreview,
		Language:      r.Language,
		Voice:         r.Voice,
		Filename:      r.Filename,
		LocalPath:     r.LocalPath,
		CleanedPath:   r.CleanedPath,
		FolderID:      r.FolderID,
		FolderPath:    r.FolderPath,
		DriveFileID:   r.DriveFileID,
		DriveLink:     r.DriveLink,
		DownloadLink:  r.DownloadLink,
		LegacyFileMD5: r.LegacyFileMD5,
		Status:        r.Status,
		Error:         r.Error,
		Strategy:      r.Strategy,
		Metadata:      r.Metadata,
		CreatedAt:     timeutil.FormatRFC3339(r.CreatedAt),
		UpdatedAt:     timeutil.FormatRFC3339(r.UpdatedAt),
	}, nil
}

// ─────────────────────────────────────────────────────────────────────

// External-boundary stubs (TTS, Drive, Resolver, Qdrant).
// ─────────────────────────────────────────────────────────────────────

type e2eTTSProvider struct {
	localPath string
	fileHash  string
}

func (s *e2eTTSProvider) Synthesize(_ context.Context, in TTSInput) (TTSOutput, error) {
	return TTSOutput{
		LocalPath:     s.localPath,
		CleanedPath:   "",
		Voice:         in.Voice,
		LegacyFileMD5: s.fileHash,
	}, nil
}

// e2eDriveAdmin implements the Drive lifecycle port and canonical
// delivery.Publisher used by the voiceover test.
// Azione #1 (July 2026): the batch path now delegates to
// ProcessSegmentUseCase.Execute which requires a Publisher port.
type e2ePublisherStub struct {
	fileID string
}

func (p *e2ePublisherStub) Publish(_ context.Context, _ VoiceoverPublishCommand) (string, error) {
	return p.fileID, nil
}

var _ VoiceoverPublisher = (*e2ePublisherStub)(nil)
