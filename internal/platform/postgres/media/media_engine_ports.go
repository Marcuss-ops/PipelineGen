package media

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/artifacts"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// EngineHandle is the typed read side of the same PostgreSQL engine that owns
// the canonical media writes. The composition root resolves ONE handle and
// derives every read adapter from it; no caller type-asserts an anonymous
// DB() surface.
type EngineHandle interface {
	MediaDetailsReader() *AssetDetailsReader
	MediaDriveFileLister() *MediaDriveFileLister
	MediaYouTubeClipLister() *MediaYouTubeClipLister
	MediaEnrichStateStore() *MediaEnrichStateStore
	MediaDuplicateGroupReader() *MediaDuplicateGroupReader
	MediaVoiceoverMediaReader() *MediaVoiceoverMediaReader
	MediaArtifactVerifier() (*MediaArtifactVerifier, error)
	ClipsMediaReader() *ClipsMediaReader
	FolderPathWriter() *FolderPathWriter
}

var _ EngineHandle = (*PostgresMediaCommitter)(nil)

func (c *PostgresMediaCommitter) MediaDetailsReader() *AssetDetailsReader {
	if c == nil || c.db == nil {
		return nil
	}
	return NewAssetDetailsReader(NewMediaSearcher(c.db))
}

func (c *PostgresMediaCommitter) MediaDriveFileLister() *MediaDriveFileLister {
	if c == nil || c.db == nil {
		return nil
	}
	return NewMediaDriveFileLister(c.db)
}

func (c *PostgresMediaCommitter) MediaYouTubeClipLister() *MediaYouTubeClipLister {
	if c == nil || c.db == nil {
		return nil
	}
	return NewMediaYouTubeClipLister(c.db)
}

func (c *PostgresMediaCommitter) MediaEnrichStateStore() *MediaEnrichStateStore {
	if c == nil || c.db == nil {
		return nil
	}
	return NewMediaEnrichStateStore(c.db)
}

func (c *PostgresMediaCommitter) MediaDuplicateGroupReader() *MediaDuplicateGroupReader {
	if c == nil || c.db == nil {
		return nil
	}
	return NewMediaDuplicateGroupReader(c.db)
}

func (c *PostgresMediaCommitter) MediaVoiceoverMediaReader() *MediaVoiceoverMediaReader {
	if c == nil || c.db == nil {
		return nil
	}
	return NewMediaVoiceoverMediaReader(c.db)
}

func (c *PostgresMediaCommitter) MediaArtifactVerifier() (*MediaArtifactVerifier, error) {
	if c == nil || c.db == nil {
		return nil, sql.ErrConnDone
	}
	return NewMediaArtifactVerifier(c.db)
}

func (c *PostgresMediaCommitter) ClipsMediaReader() *ClipsMediaReader {
	if c == nil || c.db == nil {
		return nil
	}
	return NewClipsMediaReader(c.db)
}

func (c *PostgresMediaCommitter) FolderPathWriter() *FolderPathWriter {
	if c == nil || c.db == nil {
		return nil
	}
	return &FolderPathWriter{committer: c}
}

// ClipsMediaReader is the PostgreSQL media-SSOT read side consumed by the
// artifacts ClipsRegistry. It owns every media_assets statement the capability
// used to run itself, so the capability names no engine and carries no SQL.
type ClipsMediaReader struct {
	db *sql.DB
}

func NewClipsMediaReader(db *sql.DB) *ClipsMediaReader {
	if db == nil {
		panic("media.NewClipsMediaReader: db is required")
	}
	return &ClipsMediaReader{db: db}
}

func (r *ClipsMediaReader) Get(ctx context.Context, id string) (*artifacts.MediaRecord, error) {
	if r == nil || r.db == nil {
		return nil, sql.ErrConnDone
	}
	record, err := scanClipsMediaRecord(r.db.QueryRowContext(ctx, `SELECT `+clipsMediaRecordColumns+` FROM media_assets WHERE id = $1`, id))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return record, nil
}

func (r *ClipsMediaReader) FindByPHash(ctx context.Context, phash string) (string, error) {
	if r == nil || r.db == nil {
		return "", sql.ErrConnDone
	}
	var id string
	err := r.db.QueryRowContext(ctx, `SELECT id FROM media_assets WHERE phash = $1 AND lifecycle_state != 'DELETED' LIMIT 1`, phash).Scan(&id)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return id, nil
}

func (r *ClipsMediaReader) FindByContentHash(ctx context.Context, sha256 string) (*artifacts.MediaRecord, error) {
	digestValue := strings.ToLower(strings.TrimSpace(sha256))
	if digestValue == "" {
		return nil, nil
	}
	if r == nil || r.db == nil {
		return nil, sql.ErrConnDone
	}
	var id string
	err := r.db.QueryRowContext(ctx, `
		SELECT id FROM media_assets
		WHERE (binary_sha256 = $1 OR content_sha256 = $1) AND UPPER(lifecycle_state) != 'DELETED'
		ORDER BY id ASC LIMIT 1
	`, digestValue).Scan(&id)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return r.Get(ctx, id)
}

func (r *ClipsMediaReader) GetAllWithDriveFileID(ctx context.Context) ([]*artifacts.MediaRecord, error) {
	if r == nil || r.db == nil {
		return nil, sql.ErrConnDone
	}
	rows, err := r.db.QueryContext(ctx, `SELECT `+clipsMediaRecordColumns+
		` FROM media_assets WHERE drive_file_id IS NOT NULL AND drive_file_id != $1 AND lifecycle_state != $2`,
		"", string(asset.StateDeleted))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []*artifacts.MediaRecord
	for rows.Next() {
		record, scanErr := scanClipsMediaRecord(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		out = append(out, record)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// clipsMediaRecordColumns is the single canonical projection for every
// media_assets read in this package. Both the single-row and list reads build
// their statement from it, so the column list and scan order cannot drift.
const clipsMediaRecordColumns = `
		id, COALESCE(source,''), COALESCE(name,''), COALESCE(filename,''),
		COALESCE(media_type,''), COALESCE(category,''), COALESCE(group_name,''),
		COALESCE(lifecycle_state,''), COALESCE(index_state,''),
		COALESCE(metadata_json,'{}'), COALESCE(search_text,''),
		COALESCE(drive_file_id,''), COALESCE(drive_link,''),
		COALESCE(download_link,''), COALESCE(local_path,''),
		COALESCE(NULLIF(binary_sha256,''), NULLIF(content_sha256,'')),
		COALESCE(content_sha256,''), COALESCE(phash,'')`

type clipsMediaRecordScanner interface {
	Scan(dest ...any) error
}

func scanClipsMediaRecord(scanner clipsMediaRecordScanner) (*artifacts.MediaRecord, error) {
	var (
		id, source, name, filename, mediaType, category, groupName  string
		lifecycleState, indexState, metadataJSON                    string
		searchText, driveFileID, driveLink, downloadLink, localPath string
		contentHash, legacyMD5, phash                               string
	)
	if err := scanner.Scan(
		&id, &source, &name, &filename, &mediaType, &category, &groupName,
		&lifecycleState, &indexState, &metadataJSON, &searchText,
		&driveFileID, &driveLink, &downloadLink, &localPath, &contentHash, &legacyMD5, &phash); err != nil {
		return nil, err
	}
	record := &artifacts.MediaRecord{
		ID: id, Source: source, Name: name, Filename: filename,
		MediaType: mediaType, Category: category, Group: groupName,
		Metadata: metadataJSON, ContentHash: contentHash, ContentSHA256: legacyMD5, PHash: phash,
		DriveFileID: driveFileID, DriveLink: driveLink, DownloadLink: downloadLink,
		LocalPath: localPath, Status: "ACTIVE",
	}
	if lifecycleState == string(asset.StateDeleted) {
		record.Status = "DELETED"
	}
	return record, nil
}

// FolderPathWriter is the transaction-bound folder-path write surface the
// texttracks port needs. It lives in platform so the composition root no longer
// opens a transaction or reads source_version directly from the media schema.
type FolderPathWriter struct {
	committer *PostgresMediaCommitter
}

func (w *FolderPathWriter) UpdateFolderPath(ctx context.Context, assetID, folderPath string) error {
	if w == nil || w.committer == nil || w.committer.db == nil {
		return fmt.Errorf("folder path writer: committer not wired")
	}
	tx, err := w.committer.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	var sourceVersion string
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(source_version,'') FROM media_assets WHERE id=$1`, assetID).Scan(&sourceVersion); err != nil {
		return fmt.Errorf("folder path writer: asset: %w", err)
	}
	updatedAt := time.Now().UTC().Format(time.RFC3339)
	if err := w.committer.UpdateFolderPathTx(ctx, tx, assetID, "", folderPath, updatedAt); err != nil {
		return err
	}
	if err := w.committer.CommitIndexEventTx(ctx, tx, assetID, "youtube", sourceVersion, "video"); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}
