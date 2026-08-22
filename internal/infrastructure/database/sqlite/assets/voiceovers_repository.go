package assets

import (
	"context"
	"database/sql"
	"time"

	timeutil "github.com/Marcuss-ops/PipelineGen/pkg/timeutil"
)

type Record struct {
	ID              string
	RequestID       string
	TextHash        string
	TextPreview     string
	Language        string
	Voice           string
	Filename        string
	LocalPath       string
	CleanedPath     string
	FolderID        string
	FolderPath      string
	DriveFileID     string
	DriveLink       string
	DownloadLink    string
	LegacyFileMD5        string
	DurationSeconds float64
	Status          string
	Error           string
	Strategy        string
	Metadata        string
	// Fingerprint is the P0.4 deterministic cache key for the
	// pre-TTS idempotence gate. Computed by
	// voiceover.ComputeVoiceoverFingerprint (sha256 over workspace +
	// text_hash + language + voice + destination_signature +
	// remove_silence + provider_version). Empty for legacy rows
	// predating migration 113 — those are cache-MISS on the first
	// P0.4 lookup.
	Fingerprint string

	// IdempotencyKey is the FASE 3 (July 2026) deterministic retry-safe
	// deduplication key. Populated from the voiceover pipeline's
	// BuildVoiceoverIdempotencyKey(jobID, language, textHash).
	// The UNIQUE INDEX idx_voiceovers_idempotency (migration 132)
	// enforces ONE row per non-empty key.
	// The coarser UNIQUE INDEX idx_voiceovers_job_language (migration 133)
	// enforces ONE row per (job_id, language) pair — distinct jobs
	// with the same content produce distinct keys but the job-level
	// constraint still guarantees at most one voiceover per language.
	IdempotencyKey string

	// JobID is the canonical job identifier that produced this voiceover
	// item (FASE 3, July 2026). Enables operator audit-trail correlation.
	// The UNIQUE INDEX idx_voiceovers_job_language (migration 133)
	// ensures at most ONE voiceover row per (job_id, language) pair.
	JobID string

	CreatedAt time.Time
	UpdatedAt time.Time
}

type VoiceoversRepository struct {
	db *sql.DB
}

func NewVoiceoversRepository(db *sql.DB) *VoiceoversRepository {
	return &VoiceoversRepository{db: db}
}

// DB returns the underlying database connection
func (r *VoiceoversRepository) DB() *sql.DB {
	return r.db
}

func (r *VoiceoversRepository) Upsert(ctx context.Context, rec *Record) error {
	now := timeutil.FormatRFC3339(time.Now())

	if rec.CreatedAt.IsZero() {
		rec.CreatedAt = time.Now()
	}
	if rec.UpdatedAt.IsZero() {
		rec.UpdatedAt = time.Now()
	}

	_, err := r.db.ExecContext(ctx, `
		INSERT INTO voiceovers (
			id, request_id, text_hash, text_preview, language, voice, filename,
			local_path, cleaned_path, folder_id, folder_path, drive_file_id,
			drive_link, download_link, legacy_file_md5, duration_seconds, status,
			error, strategy, metadata, fingerprint, idempotency_key, job_id,
			created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			request_id = excluded.request_id,
			text_hash = excluded.text_hash,
			text_preview = excluded.text_preview,
			language = excluded.language,
			voice = excluded.voice,
			filename = excluded.filename,
			local_path = excluded.local_path,
			cleaned_path = excluded.cleaned_path,
			folder_id = excluded.folder_id,
			folder_path = excluded.folder_path,
			drive_file_id = excluded.drive_file_id,
			drive_link = excluded.drive_link,
			download_link = excluded.download_link,
			legacy_file_md5 = excluded.legacy_file_md5,
			duration_seconds = excluded.duration_seconds,
			status = excluded.status,
			error = excluded.error,
			strategy = excluded.strategy,
			metadata = excluded.metadata,
			fingerprint = excluded.fingerprint,
			idempotency_key = excluded.idempotency_key,
			job_id = excluded.job_id,
			updated_at = excluded.updated_at
	`, rec.ID, rec.RequestID, rec.TextHash, rec.TextPreview, rec.Language, rec.Voice,
		rec.Filename, rec.LocalPath, rec.CleanedPath, rec.FolderID, rec.FolderPath,
		rec.DriveFileID, rec.DriveLink, rec.DownloadLink, rec.LegacyFileMD5, rec.DurationSeconds,
		rec.Status, rec.Error, rec.Strategy, rec.Metadata, rec.Fingerprint,
		rec.IdempotencyKey, rec.JobID,
		timeutil.FormatRFC3339(rec.CreatedAt), now)

	return err
}

func (r *VoiceoversRepository) GetByID(ctx context.Context, id string) (*Record, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT id, COALESCE(request_id, ''), COALESCE(text_hash, ''), COALESCE(text_preview, ''), COALESCE(language, ''), COALESCE(voice, ''), COALESCE(filename, ''),
			COALESCE(local_path, ''), COALESCE(cleaned_path, ''), COALESCE(folder_id, ''), COALESCE(folder_path, ''), COALESCE(drive_file_id, ''),
			COALESCE(drive_link, ''), COALESCE(download_link, ''), COALESCE(legacy_file_md5, ''), duration_seconds, COALESCE(status, ''),
			COALESCE(error, ''), COALESCE(strategy, ''), COALESCE(metadata, '{}'), created_at, updated_at
		FROM voiceovers WHERE id = ?`, id)

	var rec Record
	var createdAt, updatedAt string
	err := row.Scan(
		&rec.ID, &rec.RequestID, &rec.TextHash, &rec.TextPreview, &rec.Language,
		&rec.Voice, &rec.Filename, &rec.LocalPath, &rec.CleanedPath, &rec.FolderID,
		&rec.FolderPath, &rec.DriveFileID, &rec.DriveLink, &rec.DownloadLink,
		&rec.LegacyFileMD5, &rec.DurationSeconds, &rec.Status, &rec.Error, &rec.Strategy,
		&rec.Metadata, &createdAt, &updatedAt,
	)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	rec.CreatedAt = timeutil.ParseRFC3339(createdAt)
	rec.UpdatedAt = timeutil.ParseRFC3339(updatedAt)

	return &rec, nil
}

func (r *VoiceoversRepository) FindExisting(ctx context.Context, textHash, language, folderID string) (*Record, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT id, COALESCE(request_id, ''), COALESCE(text_hash, ''), COALESCE(text_preview, ''), COALESCE(language, ''), COALESCE(voice, ''), COALESCE(filename, ''),
			COALESCE(local_path, ''), COALESCE(cleaned_path, ''), COALESCE(folder_id, ''), COALESCE(folder_path, ''), COALESCE(drive_file_id, ''),
			COALESCE(drive_link, ''), COALESCE(download_link, ''), COALESCE(legacy_file_md5, ''), duration_seconds, COALESCE(status, ''),
			COALESCE(error, ''), COALESCE(strategy, ''), COALESCE(metadata, '{}'), created_at, updated_at
		FROM voiceovers
		WHERE text_hash = ? AND language = ? AND folder_id = ?
		ORDER BY created_at DESC LIMIT 1
	`, textHash, language, folderID)

	var rec Record
	var createdAt, updatedAt string
	err := row.Scan(
		&rec.ID, &rec.RequestID, &rec.TextHash, &rec.TextPreview, &rec.Language,
		&rec.Voice, &rec.Filename, &rec.LocalPath, &rec.CleanedPath, &rec.FolderID,
		&rec.FolderPath, &rec.DriveFileID, &rec.DriveLink, &rec.DownloadLink,
		&rec.LegacyFileMD5, &rec.DurationSeconds, &rec.Status, &rec.Error, &rec.Strategy,
		&rec.Metadata, &createdAt, &updatedAt,
	)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	rec.CreatedAt = timeutil.ParseRFC3339(createdAt)
	rec.UpdatedAt = timeutil.ParseRFC3339(updatedAt)

	return &rec, nil
}

func (r *VoiceoversRepository) MarkStatus(ctx context.Context, id, status, errMsg string) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE voiceovers SET status = ?, error = ?, updated_at = ?
		WHERE id = ?
	`, status, errMsg, timeutil.FormatRFC3339(time.Now()), id)
	return err
}

func (r *VoiceoversRepository) ListByRequestID(ctx context.Context, requestID string) ([]*Record, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, COALESCE(request_id, ''), COALESCE(text_hash, ''), COALESCE(text_preview, ''), COALESCE(language, ''), COALESCE(voice, ''), COALESCE(filename, ''),
			COALESCE(local_path, ''), COALESCE(cleaned_path, ''), COALESCE(folder_id, ''), COALESCE(folder_path, ''), COALESCE(drive_file_id, ''),
			COALESCE(drive_link, ''), COALESCE(download_link, ''), COALESCE(legacy_file_md5, ''), duration_seconds, COALESCE(status, ''),
			COALESCE(error, ''), COALESCE(strategy, ''), COALESCE(metadata, '{}'), created_at, updated_at
		FROM voiceovers WHERE request_id = ? ORDER BY created_at`, requestID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var records []*Record
	for rows.Next() {
		var rec Record
		var createdAt, updatedAt string
		err := rows.Scan(
			&rec.ID, &rec.RequestID, &rec.TextHash, &rec.TextPreview, &rec.Language,
			&rec.Voice, &rec.Filename, &rec.LocalPath, &rec.CleanedPath, &rec.FolderID,
			&rec.FolderPath, &rec.DriveFileID, &rec.DriveLink, &rec.DownloadLink,
			&rec.LegacyFileMD5, &rec.DurationSeconds, &rec.Status, &rec.Error, &rec.Strategy,
			&rec.Metadata, &createdAt, &updatedAt,
		)
		if err != nil {
			return nil, err
		}
		rec.CreatedAt = timeutil.ParseRFC3339(createdAt)
		rec.UpdatedAt = timeutil.ParseRFC3339(updatedAt)
		records = append(records, &rec)
	}
	return records, rows.Err()
}

func (r *VoiceoversRepository) ListByFolderID(ctx context.Context, folderID string) ([]*Record, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, COALESCE(request_id, ''), COALESCE(text_hash, ''), COALESCE(text_preview, ''), COALESCE(language, ''), COALESCE(voice, ''), COALESCE(filename, ''),
			COALESCE(local_path, ''), COALESCE(cleaned_path, ''), COALESCE(folder_id, ''), COALESCE(folder_path, ''), COALESCE(drive_file_id, ''),
			COALESCE(drive_link, ''), COALESCE(download_link, ''), COALESCE(legacy_file_md5, ''), duration_seconds, COALESCE(status, ''),
			COALESCE(error, ''), COALESCE(strategy, ''), COALESCE(metadata, '{}'), created_at, updated_at
		FROM voiceovers WHERE folder_id = ? ORDER BY created_at`, folderID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var records []*Record
	for rows.Next() {
		var rec Record
		var createdAt, updatedAt string
		err := rows.Scan(
			&rec.ID, &rec.RequestID, &rec.TextHash, &rec.TextPreview, &rec.Language,
			&rec.Voice, &rec.Filename, &rec.LocalPath, &rec.CleanedPath, &rec.FolderID,
			&rec.FolderPath, &rec.DriveFileID, &rec.DriveLink, &rec.DownloadLink,
			&rec.LegacyFileMD5, &rec.DurationSeconds, &rec.Status, &rec.Error, &rec.Strategy,
			&rec.Metadata, &createdAt, &updatedAt,
		)
		if err != nil {
			return nil, err
		}
		rec.CreatedAt = timeutil.ParseRFC3339(createdAt)
		rec.UpdatedAt = timeutil.ParseRFC3339(updatedAt)
		records = append(records, &rec)
	}
	return records, rows.Err()
}

func (r *VoiceoversRepository) Delete(ctx context.Context, id string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM voiceovers WHERE id = ?`, id)
	return err
}

// DeleteByIDTx deletes a voiceovers row inside a caller-owned transaction.
// Used by voiceover.GenerateVoiceoversUseCase for the PR-VO-A2 atomic
// swap (Delete + Insert in one tx).
func (r *VoiceoversRepository) DeleteByIDTx(ctx context.Context, tx *sql.Tx, id string) error {
	_, err := tx.ExecContext(ctx, `DELETE FROM voiceovers WHERE id = ?`, id)
	return err
}

// InsertTx inserts a voiceovers row inside a caller-owned transaction
// (plain INSERT — atomicity is enforced by the caller's
// DELETE-then-INSERT sequence in the same tx; the caller is the
// canonical source of truth on swap semantics).
func (r *VoiceoversRepository) InsertTx(ctx context.Context, tx *sql.Tx, rec *Record) error {
	if rec.CreatedAt.IsZero() {
		rec.CreatedAt = time.Now()
	}
	rec.UpdatedAt = time.Now()
	_, err := tx.ExecContext(ctx, `
		INSERT INTO voiceovers (
			id, request_id, text_hash, text_preview, language, voice, filename,
			local_path, cleaned_path, folder_id, folder_path, drive_file_id,
			drive_link, download_link, legacy_file_md5, duration_seconds, status,
			error, strategy, metadata, fingerprint, idempotency_key, job_id,
			created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		rec.ID, rec.RequestID, rec.TextHash, rec.TextPreview, rec.Language, rec.Voice,
		rec.Filename, rec.LocalPath, rec.CleanedPath, rec.FolderID, rec.FolderPath,
		rec.DriveFileID, rec.DriveLink, rec.DownloadLink, rec.LegacyFileMD5, rec.DurationSeconds,
		rec.Status, rec.Error, rec.Strategy, rec.Metadata, rec.Fingerprint,
		rec.IdempotencyKey, rec.JobID,
		timeutil.FormatRFC3339(rec.CreatedAt), timeutil.FormatRFC3339(rec.UpdatedAt),
	)
	return err
}

// PreReadByID reads a voiceovers row without claiming any tx lock.
// Called BEFORE the atomic swap so the use case can capture the OLD row's
// orphan paths (Drive file ID + local paths) for the post-commit cleanup
// goroutine. Thin alias of GetByID; the explicit name surfaces intent
// at the use-case boundary.
func (r *VoiceoversRepository) PreReadByID(ctx context.Context, id string) (*Record, error) {
	return r.GetByID(ctx, id)
}

// FindByFingerprint looks up voiceover rows by exact P0.4 fingerprint
// match (cache-key match for the pre-TTS idempotence gate). Returns
// the FIRST matching row sorted by created_at DESCENDING (most
// recent first when replace-mode swaps have produced 2+ rows).
//
// P0.4 idempotence contract: callers MUST filter on status before
// accepting the row as a cache hit (see voiceover.IsReusableStatus —
// completed | partial | uploaded → reuse; failed | processing |
// generated → cache MISS). The repository deliberately does NOT
// filter: callers have different reuse semantics and a hard-coded
// SQL filter would force a fork.
//
// Empty fingerprint → returns (nil, nil) (legacy rows predating
// migration 113 → cache-MISS, which is correct: their fingerprint
// column is NULL).
func (r *VoiceoversRepository) FindByFingerprint(ctx context.Context, fingerprint string) (*Record, error) {
	if fingerprint == "" {
		return nil, nil
	}
	row := r.db.QueryRowContext(ctx, `
		SELECT id, COALESCE(request_id, ''), COALESCE(text_hash, ''), COALESCE(text_preview, ''), COALESCE(language, ''), COALESCE(voice, ''), COALESCE(filename, ''),
			COALESCE(local_path, ''), COALESCE(cleaned_path, ''), COALESCE(folder_id, ''), COALESCE(folder_path, ''), COALESCE(drive_file_id, ''),
			COALESCE(drive_link, ''), COALESCE(download_link, ''), COALESCE(legacy_file_md5, ''), duration_seconds, COALESCE(status, ''),
			COALESCE(error, ''), COALESCE(strategy, ''), COALESCE(metadata, '{}'), COALESCE(fingerprint, ''), created_at, updated_at
		FROM voiceovers
		WHERE fingerprint = ?
		ORDER BY created_at DESC LIMIT 1
	`, fingerprint)

	var rec Record
	var createdAt, updatedAt string
	err := row.Scan(
		&rec.ID, &rec.RequestID, &rec.TextHash, &rec.TextPreview, &rec.Language,
		&rec.Voice, &rec.Filename, &rec.LocalPath, &rec.CleanedPath, &rec.FolderID,
		&rec.FolderPath, &rec.DriveFileID, &rec.DriveLink, &rec.DownloadLink, &rec.LegacyFileMD5,
		&rec.DurationSeconds, &rec.Status, &rec.Error, &rec.Strategy, &rec.Metadata,
		&rec.Fingerprint, &createdAt, &updatedAt,
	)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	rec.CreatedAt = timeutil.ParseRFC3339(createdAt)
	rec.UpdatedAt = timeutil.ParseRFC3339(updatedAt)
	return &rec, nil
}

func (r *VoiceoversRepository) GetByDriveFileID(ctx context.Context, fileID string) (*Record, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT id, COALESCE(request_id, ''), COALESCE(text_hash, ''), COALESCE(text_preview, ''), COALESCE(language, ''), COALESCE(voice, ''), COALESCE(filename, ''),
			COALESCE(local_path, ''), COALESCE(cleaned_path, ''), COALESCE(folder_id, ''), COALESCE(folder_path, ''), COALESCE(drive_file_id, ''),
			COALESCE(drive_link, ''), COALESCE(download_link, ''), COALESCE(legacy_file_md5, ''), duration_seconds, COALESCE(status, ''),
			COALESCE(error, ''), COALESCE(strategy, ''), COALESCE(metadata, '{}'), created_at, updated_at
		FROM voiceovers
		WHERE drive_file_id = ? OR drive_link LIKE ? OR download_link LIKE ?`,
		fileID, "%"+fileID+"%", "%"+fileID+"%")

	var rec Record
	var createdAt, updatedAt string
	err := row.Scan(
		&rec.ID, &rec.RequestID, &rec.TextHash, &rec.TextPreview, &rec.Language,
		&rec.Voice, &rec.Filename, &rec.LocalPath, &rec.CleanedPath, &rec.FolderID,
		&rec.FolderPath, &rec.DriveFileID, &rec.DriveLink, &rec.DownloadLink,
		&rec.LegacyFileMD5, &rec.DurationSeconds, &rec.Status, &rec.Error, &rec.Strategy,
		&rec.Metadata, &createdAt, &updatedAt,
	)
	if err != nil {
		return nil, err
	}
	rec.CreatedAt = timeutil.ParseRFC3339(createdAt)
	rec.UpdatedAt = timeutil.ParseRFC3339(updatedAt)
	return &rec, nil
}

func (r *VoiceoversRepository) ListAll(ctx context.Context) ([]*Record, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, COALESCE(request_id, ''), COALESCE(text_hash, ''), COALESCE(text_preview, ''), COALESCE(language, ''), COALESCE(voice, ''), COALESCE(filename, ''),
			COALESCE(local_path, ''), COALESCE(cleaned_path, ''), COALESCE(folder_id, ''), COALESCE(folder_path, ''), COALESCE(drive_file_id, ''),
			COALESCE(drive_link, ''), COALESCE(download_link, ''), COALESCE(legacy_file_md5, ''), duration_seconds, COALESCE(status, ''),
			COALESCE(error, ''), COALESCE(strategy, ''), COALESCE(metadata, '{}'), created_at, updated_at
		FROM voiceovers ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var records []*Record
	for rows.Next() {
		var rec Record
		var createdAt, updatedAt string
		err := rows.Scan(
			&rec.ID, &rec.RequestID, &rec.TextHash, &rec.TextPreview, &rec.Language,
			&rec.Voice, &rec.Filename, &rec.LocalPath, &rec.CleanedPath, &rec.FolderID,
			&rec.FolderPath, &rec.DriveFileID, &rec.DriveLink, &rec.DownloadLink,
			&rec.LegacyFileMD5, &rec.DurationSeconds, &rec.Status, &rec.Error, &rec.Strategy,
			&rec.Metadata, &createdAt, &updatedAt,
		)
		if err != nil {
			return nil, err
		}
		rec.CreatedAt = timeutil.ParseRFC3339(createdAt)
		rec.UpdatedAt = timeutil.ParseRFC3339(updatedAt)
		records = append(records, &rec)
	}
	return records, rows.Err()
}
