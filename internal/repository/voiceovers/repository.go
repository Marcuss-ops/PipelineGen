package voiceovers

import (
	"context"
	"database/sql"
	"time"
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
	FileHash        string
	DurationSeconds float64
	Status          string
	Error           string
	Strategy        string
	Metadata        string
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

type Repository struct {
	db *sql.DB
}

func NewRepository(db *sql.DB) *Repository {
	return &Repository{db: db}
}

// DB returns the underlying database connection
func (r *Repository) DB() *sql.DB {
	return r.db
}

func (r *Repository) Upsert(ctx context.Context, rec *Record) error {
	now := time.Now().Format(time.RFC3339)

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
			drive_link, download_link, file_hash, duration_seconds, status,
			error, strategy, metadata, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
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
			file_hash = excluded.file_hash,
			duration_seconds = excluded.duration_seconds,
			status = excluded.status,
			error = excluded.error,
			strategy = excluded.strategy,
			metadata = excluded.metadata,
			updated_at = excluded.updated_at
	`, rec.ID, rec.RequestID, rec.TextHash, rec.TextPreview, rec.Language, rec.Voice,
		rec.Filename, rec.LocalPath, rec.CleanedPath, rec.FolderID, rec.FolderPath,
		rec.DriveFileID, rec.DriveLink, rec.DownloadLink, rec.FileHash, rec.DurationSeconds,
		rec.Status, rec.Error, rec.Strategy, rec.Metadata, rec.CreatedAt.Format(time.RFC3339), now)

	return err
}

func (r *Repository) GetByID(ctx context.Context, id string) (*Record, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT id, COALESCE(request_id, ''), COALESCE(text_hash, ''), COALESCE(text_preview, ''), COALESCE(language, ''), COALESCE(voice, ''), COALESCE(filename, ''),
			COALESCE(local_path, ''), COALESCE(cleaned_path, ''), COALESCE(folder_id, ''), COALESCE(folder_path, ''), COALESCE(drive_file_id, ''),
			COALESCE(drive_link, ''), COALESCE(download_link, ''), COALESCE(file_hash, ''), duration_seconds, COALESCE(status, ''),
			COALESCE(error, ''), COALESCE(strategy, ''), COALESCE(metadata, '{}'), created_at, updated_at
		FROM voiceovers WHERE id = ?`, id)

	var rec Record
	var createdAt, updatedAt string
	err := row.Scan(
		&rec.ID, &rec.RequestID, &rec.TextHash, &rec.TextPreview, &rec.Language,
		&rec.Voice, &rec.Filename, &rec.LocalPath, &rec.CleanedPath, &rec.FolderID,
		&rec.FolderPath, &rec.DriveFileID, &rec.DriveLink, &rec.DownloadLink,
		&rec.FileHash, &rec.DurationSeconds, &rec.Status, &rec.Error, &rec.Strategy,
		&rec.Metadata, &createdAt, &updatedAt,
	)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	rec.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	rec.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)

	return &rec, nil
}

func (r *Repository) FindExisting(ctx context.Context, textHash, language, folderID string) (*Record, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT id, COALESCE(request_id, ''), COALESCE(text_hash, ''), COALESCE(text_preview, ''), COALESCE(language, ''), COALESCE(voice, ''), COALESCE(filename, ''),
			COALESCE(local_path, ''), COALESCE(cleaned_path, ''), COALESCE(folder_id, ''), COALESCE(folder_path, ''), COALESCE(drive_file_id, ''),
			COALESCE(drive_link, ''), COALESCE(download_link, ''), COALESCE(file_hash, ''), duration_seconds, COALESCE(status, ''),
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
		&rec.FileHash, &rec.DurationSeconds, &rec.Status, &rec.Error, &rec.Strategy,
		&rec.Metadata, &createdAt, &updatedAt,
	)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	rec.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	rec.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)

	return &rec, nil
}

func (r *Repository) MarkStatus(ctx context.Context, id, status, errMsg string) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE voiceovers SET status = ?, error = ?, updated_at = ?
		WHERE id = ?
	`, status, errMsg, time.Now().Format(time.RFC3339), id)
	return err
}

func (r *Repository) ListByRequestID(ctx context.Context, requestID string) ([]*Record, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, COALESCE(request_id, ''), COALESCE(text_hash, ''), COALESCE(text_preview, ''), COALESCE(language, ''), COALESCE(voice, ''), COALESCE(filename, ''),
			COALESCE(local_path, ''), COALESCE(cleaned_path, ''), COALESCE(folder_id, ''), COALESCE(folder_path, ''), COALESCE(drive_file_id, ''),
			COALESCE(drive_link, ''), COALESCE(download_link, ''), COALESCE(file_hash, ''), duration_seconds, COALESCE(status, ''),
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
			&rec.FileHash, &rec.DurationSeconds, &rec.Status, &rec.Error, &rec.Strategy,
			&rec.Metadata, &createdAt, &updatedAt,
		)
		if err != nil {
			return nil, err
		}
		rec.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
		rec.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
		records = append(records, &rec)
	}
	return records, rows.Err()
}

func (r *Repository) ListByFolderID(ctx context.Context, folderID string) ([]*Record, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, COALESCE(request_id, ''), COALESCE(text_hash, ''), COALESCE(text_preview, ''), COALESCE(language, ''), COALESCE(voice, ''), COALESCE(filename, ''),
			COALESCE(local_path, ''), COALESCE(cleaned_path, ''), COALESCE(folder_id, ''), COALESCE(folder_path, ''), COALESCE(drive_file_id, ''),
			COALESCE(drive_link, ''), COALESCE(download_link, ''), COALESCE(file_hash, ''), duration_seconds, COALESCE(status, ''),
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
			&rec.FileHash, &rec.DurationSeconds, &rec.Status, &rec.Error, &rec.Strategy,
			&rec.Metadata, &createdAt, &updatedAt,
		)
		if err != nil {
			return nil, err
		}
		rec.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
		rec.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
		records = append(records, &rec)
	}
	return records, rows.Err()
}

func (r *Repository) Delete(ctx context.Context, id string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM voiceovers WHERE id = ?`, id)
	return err
}

func (r *Repository) GetByDriveFileID(ctx context.Context, fileID string) (*Record, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT id, COALESCE(request_id, ''), COALESCE(text_hash, ''), COALESCE(text_preview, ''), COALESCE(language, ''), COALESCE(voice, ''), COALESCE(filename, ''),
			COALESCE(local_path, ''), COALESCE(cleaned_path, ''), COALESCE(folder_id, ''), COALESCE(folder_path, ''), COALESCE(drive_file_id, ''),
			COALESCE(drive_link, ''), COALESCE(download_link, ''), COALESCE(file_hash, ''), duration_seconds, COALESCE(status, ''),
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
		&rec.FileHash, &rec.DurationSeconds, &rec.Status, &rec.Error, &rec.Strategy,
		&rec.Metadata, &createdAt, &updatedAt,
	)
	if err != nil {
		return nil, err
	}
	rec.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	rec.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
	return &rec, nil
}

func (r *Repository) ListAll(ctx context.Context) ([]*Record, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, COALESCE(request_id, ''), COALESCE(text_hash, ''), COALESCE(text_preview, ''), COALESCE(language, ''), COALESCE(voice, ''), COALESCE(filename, ''),
			COALESCE(local_path, ''), COALESCE(cleaned_path, ''), COALESCE(folder_id, ''), COALESCE(folder_path, ''), COALESCE(drive_file_id, ''),
			COALESCE(drive_link, ''), COALESCE(download_link, ''), COALESCE(file_hash, ''), duration_seconds, COALESCE(status, ''),
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
			&rec.FileHash, &rec.DurationSeconds, &rec.Status, &rec.Error, &rec.Strategy,
			&rec.Metadata, &createdAt, &updatedAt,
		)
		if err != nil {
			return nil, err
		}
		rec.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
		rec.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
		records = append(records, &rec)
	}
	return records, rows.Err()
}
