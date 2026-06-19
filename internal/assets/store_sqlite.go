package assets

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	hashutil "github.com/Marcuss-ops/PipelineGen/internal/platform/files"
	timeutil "github.com/Marcuss-ops/PipelineGen/internal/platform"
	"go.uber.org/zap"
)

// ── AssetStoreSQLite ──────────────────────────────────────────────────

type AssetStoreSQLite struct {
	db  *sql.DB
	log *zap.Logger
}

func NewAssetStoreSQLite(db *sql.DB, log *zap.Logger) *AssetStoreSQLite {
	if log == nil {
		log = zap.NewNop()
	}
	return &AssetStoreSQLite{db: db, log: log}
}

var _ Store = (*AssetStoreSQLite)(nil)

// Get retrieves a single asset, joining with its primary/available locations, processing records, and versions.
func (s *AssetStoreSQLite) Get(ctx context.Context, id string) (*Details, error) {
	if id == "" {
		return nil, errors.New("invalid id")
	}

	var a Asset
	var tagsJSON, searchTermsJSON, metadataStr sql.NullString
	var grp sql.NullString
	var createdAtStr, updatedAtStr, deletedAtStr sql.NullString
	var lifecycleStr sql.NullString
	var duration sql.NullInt64

	// Additional asset columns from migration 059
	var folderID, parentFolderID, folderPath sql.NullString
	var depth, isFolder, childCount sql.NullInt64
	var sceneType, usableForJSON, avoidForJSON, phash, lastUsedAt sql.NullString
	var qualityScore sql.NullFloat64
	var reuseCount sql.NullInt64

	// Embedding columns
	var embeddingJSON, visualEmb, transcriptEmb sql.NullString

	err := s.db.QueryRowContext(ctx, `
		SELECT id, COALESCE(source, '') AS source, COALESCE(name, '') AS name, COALESCE(filename, '') AS filename,
			COALESCE(media_type, '') AS media_type, COALESCE(category, '') AS category, COALESCE(group_name, '') AS grp,
			COALESCE(url, '') AS source_url, COALESCE(clip_page_url, '') AS clip_page_url, COALESCE(thumbnail_url, '') AS thumbnail_url,
			COALESCE(duration_ms, 0) AS duration_ms, COALESCE(tags, '[]') AS tags, COALESCE(search_terms, '[]') AS search_terms,
			COALESCE(search_text, '') AS search_text, COALESCE(lifecycle_state, 'ready') AS lifecycle_state, deleted_at,
			COALESCE(metadata_json, '{}') AS metadata_json, created_at, updated_at,
			COALESCE(folder_id, '') AS folder_id, COALESCE(parent_folder_id, '') AS parent_folder_id, COALESCE(folder_path, '') AS folder_path,
			COALESCE(depth, 0) AS depth, is_folder, COALESCE(child_count, 0) AS child_count,
			COALESCE(scene_type, '') AS scene_type, COALESCE(usable_for, '[]') AS usable_for, COALESCE(avoid_for, '[]') AS avoid_for,
			COALESCE(phash, '') AS phash, COALESCE(last_used_at, '') AS last_used_at, COALESCE(quality_score, 0.0) AS quality_score,
			COALESCE(reuse_count, 0) AS reuse_count, COALESCE(embedding_json, '') AS embedding_json,
			COALESCE(visual_embedding, '') AS visual_embedding, COALESCE(transcript_embedding, '') AS transcript_embedding
		FROM media_assets WHERE id = ?
	`, id).Scan(
		&a.ID, &a.Source, &a.Name, &a.Filename,
		&a.MediaType, &a.Category, &grp,
		&a.SourceURL, &a.ClipPageURL, &a.ThumbnailURL,
		&duration, &tagsJSON, &searchTermsJSON,
		&a.SearchText, &lifecycleStr, &deletedAtStr,
		&metadataStr, &createdAtStr, &updatedAtStr,
		&folderID, &parentFolderID, &folderPath,
		&depth, &isFolder, &childCount,
		&sceneType, &usableForJSON, &avoidForJSON,
		&phash, &lastUsedAt, &qualityScore,
		&reuseCount, &embeddingJSON,
		&visualEmb, &transcriptEmb,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("assets: get %s: %w", id, err)
	}

	// Load metadata_json first
	if metadataStr.Valid && metadataStr.String != "" {
		_ = json.Unmarshal([]byte(metadataStr.String), &a.Metadata)
	}

	if grp.Valid {
		a.Group = grp.String
	}
	if duration.Valid {
		a.Duration = time.Duration(duration.Int64) * time.Millisecond
	}
	if folderID.Valid {
		a.SetFolderID(folderID.String)
	}
	if parentFolderID.Valid {
		a.SetParentFolderID(parentFolderID.String)
	}
	if folderPath.Valid {
		a.SetFolderPath(folderPath.String)
	}
	if depth.Valid {
		a.SetDepth(int(depth.Int64))
	}
	if sceneType.Valid {
		a.SetSceneType(sceneType.String)
	}
	if phash.Valid {
		a.SetPHash(phash.String)
	}
	if lastUsedAt.Valid {
		a.SetLastUsedAt(lastUsedAt.String)
	}
	if qualityScore.Valid {
		a.SetQualityScore(qualityScore.Float64)
	}
	if reuseCount.Valid {
		a.SetReuseCount(int(reuseCount.Int64))
	}
	if embeddingJSON.Valid {
		a.SetEmbeddingJSON(embeddingJSON.String)
	}
	if visualEmb.Valid {
		a.SetVisualEmbedding(visualEmb.String)
	}
	if transcriptEmb.Valid {
		a.SetTranscriptEmbedding(transcriptEmb.String)
	}

	a.LifecycleState = LifecycleState(lifecycleStr.String)
	if deletedAtStr.Valid && deletedAtStr.String != "" {
		t := timeutil.ParseRFC3339(deletedAtStr.String)
		if !t.IsZero() {
			a.DeletedAt = &t
		}
	}
	a.CreatedAt = timeutil.ParseRFC3339(createdAtStr.String)
	a.UpdatedAt = timeutil.ParseRFC3339(updatedAtStr.String)

	if tagsJSON.Valid && tagsJSON.String != "" && tagsJSON.String != "[]" {
		_ = json.Unmarshal([]byte(tagsJSON.String), &a.Tags)
	}
	if searchTermsJSON.Valid && searchTermsJSON.String != "" && searchTermsJSON.String != "[]" {
		_ = json.Unmarshal([]byte(searchTermsJSON.String), &a.SearchTerms)
	}

	// Load physical locations
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, asset_id, location_kind, uri, external_id, web_view_link, download_url, mime_type, file_size_bytes, file_hash, is_primary, created_at, updated_at
		FROM asset_locations WHERE asset_id = ?
	`, id)
	var locations []*Location
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var loc Location
			var kind, uri, extID, webLink, dlURL, mime, hash, createdAtStr, updatedAtStr sql.NullString
			var isPrimaryInt sql.NullInt64
			if err := rows.Scan(&loc.ID, &loc.AssetID, &kind, &uri, &extID, &webLink, &dlURL, &mime, &loc.FileSizeBytes, &hash, &isPrimaryInt, &createdAtStr, &updatedAtStr); err == nil {
				loc.LocationKind = LocationKind(kind.String)
				loc.URI = uri.String
				loc.ExternalID = extID.String
				loc.AccessURL = webLink.String
				loc.DownloadURL = dlURL.String
				loc.MimeType = mime.String
				loc.FileHash = hash.String
				loc.IsPrimary = isPrimaryInt.Int64 == 1
				loc.CreatedAt = timeutil.ParseRFC3339(createdAtStr.String)
				loc.UpdatedAt = timeutil.ParseRFC3339(updatedAtStr.String)
				locations = append(locations, &loc)

				if loc.LocationKind == "local" {
					a.SetLocalPath(loc.URI)
					if loc.FileHash != "" {
						a.SetFileHash(loc.FileHash)
					}
				} else if loc.LocationKind == "drive" {
					a.SetDriveFileID(loc.ExternalID)
					a.SetDriveLink(loc.AccessURL)
					a.SetDownloadLink(loc.DownloadURL)
					if a.FileHash() == "" {
						a.SetFileHash(loc.FileHash)
					}
				}
			}
		}
	}

	// Load processing records
	var processing []*ProcessingRecord
	rowsProc, err := s.db.QueryContext(ctx, `
		SELECT asset_id, step, status, started_at, completed_at, error_message, attempt_count, metadata_json
		FROM asset_processing WHERE asset_id = ?
	`, id)
	if err == nil {
		defer rowsProc.Close()
		for rowsProc.Next() {
			var proc ProcessingRecord
			var statusStr, errStr, metaStr, startStr, compStr sql.NullString
			if err := rowsProc.Scan(&proc.AssetID, &proc.Step, &statusStr, &startStr, &compStr, &errStr, &proc.AttemptCount, &metaStr); err == nil {
				proc.Status = ProcessingStatus(statusStr.String)
				proc.ErrorMessage = errStr.String
				proc.MetadataJSON = metaStr.String
				if startStr.Valid && startStr.String != "" {
					t := timeutil.ParseRFC3339(startStr.String)
					proc.StartedAt = &t
				}
				if compStr.Valid && compStr.String != "" {
					t := timeutil.ParseRFC3339(compStr.String)
					proc.CompletedAt = &t
				}
				processing = append(processing, &proc)
			}
		}
	}

	// Load versions
	var versions []*Version
	rowsVer, err := s.db.QueryContext(ctx, `
		SELECT id, asset_id, version_number, source_uri, file_hash, file_size_bytes, mime_type, metadata_json, created_at
		FROM asset_versions WHERE asset_id = ?
	`, id)
	if err == nil {
		defer rowsVer.Close()
		for rowsVer.Next() {
			var ver Version
			var sourceURI, fileHash, mimeType, metaStr, createdAtStr sql.NullString
			if err := rowsVer.Scan(&ver.ID, &ver.AssetID, &ver.VersionNumber, &sourceURI, &fileHash, &ver.FileSizeBytes, &mimeType, &metaStr, &createdAtStr); err == nil {
				ver.SourceURI = sourceURI.String
				ver.FileHash = fileHash.String
				ver.MimeType = mimeType.String
				ver.MetadataJSON = metaStr.String
				ver.CreatedAt = timeutil.ParseRFC3339(createdAtStr.String)
				versions = append(versions, &ver)
			}
		}
	}

	return &Details{
		Asset:      &a,
		Locations:  locations,
		Processing: processing,
		Versions:   versions,
	}, nil
}

// List queries assets based on filter.
func (s *AssetStoreSQLite) List(ctx context.Context, filter Filter) ([]*Summary, error) {
	conds := []string{"1=1"}
	args := []any{}

	if filter.Source != "" {
		conds = append(conds, "a.source = ?")
		args = append(args, filter.Source)
	}
	if filter.Category != "" {
		conds = append(conds, "a.category = ?")
		args = append(args, filter.Category)
	}
	if filter.Group != "" {
		conds = append(conds, "a.group_name = ?")
		args = append(args, filter.Group)
	}

	query := `
		SELECT a.id, COALESCE(a.source, '') AS source, COALESCE(a.name, '') AS name, COALESCE(a.filename, '') AS filename,
			COALESCE(a.media_type, '') AS media_type, COALESCE(a.category, '') AS category, COALESCE(a.lifecycle_state, 'ready') AS lifecycle_state,
			COALESCE(l.uri, '') AS primary_uri, a.created_at, a.updated_at
		FROM media_assets a
		LEFT JOIN asset_locations l ON a.id = l.asset_id AND l.is_primary = 1
		WHERE ` + strings.Join(conds, " AND ") + `
		ORDER BY a.created_at DESC
	`
	if filter.Limit > 0 {
		query += " LIMIT ?"
		args = append(args, filter.Limit)
		if filter.Offset > 0 {
			query += " OFFSET ?"
			args = append(args, filter.Offset)
		}
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("assets: list query: %w", err)
	}
	defer rows.Close()

	var out []*Summary
	for rows.Next() {
		var sObj Summary
		var srcStr, mediaStr, lifecycleStr, primaryURI, createdAtStr, updatedAtStr sql.NullString
		if err := rows.Scan(&sObj.ID, &srcStr, &sObj.Name, &sObj.Filename, &mediaStr, &sObj.Category, &lifecycleStr, &primaryURI, &createdAtStr, &updatedAtStr); err != nil {
			return nil, err
		}
		sObj.Source = Source(srcStr.String)
		sObj.MediaType = MediaType(mediaStr.String)
		sObj.LifecycleState = LifecycleState(lifecycleStr.String)
		sObj.PrimaryURI = primaryURI.String
		sObj.CreatedAt = timeutil.ParseRFC3339(createdAtStr.String)
		sObj.UpdatedAt = timeutil.ParseRFC3339(updatedAtStr.String)
		out = append(out, &sObj)
	}
	return out, nil
}

// Save inserts or updates an asset aggregate (media_assets, asset_locations, asset_processing, asset_versions).
func (s *AssetStoreSQLite) Save(ctx context.Context, details *Details) error {
	if details == nil || details.Asset == nil || details.Asset.ID == "" {
		return errors.New("invalid details or asset")
	}
	a := details.Asset

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("assets: save begin: %w", err)
	}
	defer tx.Rollback()

	now := time.Now().UTC()
	nowStr := timeutil.FormatRFC3339(now)

	if a.CreatedAt.IsZero() {
		a.CreatedAt = now
	}
	a.UpdatedAt = now

	// Sync local location variables from locations list if primary
	for _, loc := range details.Locations {
		if loc.IsPrimary || len(details.Locations) == 1 {
			if loc.LocationKind == "local" {
				a.SetLocalPath(loc.URI)
				if loc.FileHash != "" {
					a.SetFileHash(loc.FileHash)
				}
			} else if loc.LocationKind == "drive" {
				a.SetDriveFileID(loc.ExternalID)
				a.SetDriveLink(loc.AccessURL)
				a.SetDownloadLink(loc.DownloadURL)
				if a.FileHash() == "" {
					a.SetFileHash(loc.FileHash)
				}
			}
		}
	}

	tagsJSON, _ := json.Marshal(a.Tags)
	searchTermsJSON, _ := json.Marshal(a.SearchTerms)
	metadataJSON, _ := json.Marshal(a.Metadata)

	deletedAtStr := ""
	if a.DeletedAt != nil {
		deletedAtStr = timeutil.FormatRFC3339(*a.DeletedAt)
	}

	_, err = tx.ExecContext(ctx, `
		INSERT INTO media_assets (
			id, source, name, filename, media_type, category, group_name,
			url, clip_page_url, thumbnail_url, duration_ms, tags, search_terms,
			search_text, lifecycle_state, deleted_at, metadata_json,
			created_at, updated_at, folder_id, parent_folder_id, folder_path,
			scene_type, phash, last_used_at, quality_score, reuse_count,
			embedding_json, visual_embedding, transcript_embedding
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			source = excluded.source,
			name = excluded.name,
			filename = excluded.filename,
			media_type = excluded.media_type,
			category = excluded.category,
			group_name = excluded.group_name,
			url = excluded.url,
			clip_page_url = excluded.clip_page_url,
			thumbnail_url = excluded.thumbnail_url,
			duration_ms = excluded.duration_ms,
			tags = excluded.tags,
			search_terms = excluded.search_terms,
			search_text = excluded.search_text,
			lifecycle_state = excluded.lifecycle_state,
			deleted_at = excluded.deleted_at,
			metadata_json = excluded.metadata_json,
			updated_at = excluded.updated_at,
			folder_id = excluded.folder_id,
			parent_folder_id = excluded.parent_folder_id,
			folder_path = excluded.folder_path,
			scene_type = excluded.scene_type,
			phash = excluded.phash,
			last_used_at = excluded.last_used_at,
			quality_score = excluded.quality_score,
			reuse_count = excluded.reuse_count,
			embedding_json = excluded.embedding_json,
			visual_embedding = excluded.visual_embedding,
			transcript_embedding = excluded.transcript_embedding
	`,
		a.ID, string(a.Source), a.Name, a.Filename, string(a.MediaType), a.Category, a.Group,
		a.SourceURL, a.ClipPageURL, a.ThumbnailURL, a.Duration.Milliseconds(), string(tagsJSON), string(searchTermsJSON),
		a.SearchText, string(a.LifecycleState), deletedAtStr, string(metadataJSON),
		timeutil.FormatRFC3339(a.CreatedAt), nowStr, a.FolderID(), a.ParentFolderID(), a.FolderPath(),
		a.SceneType(), a.PHash(), a.LastUsedAt(), a.QualityScore(), a.ReuseCount(),
		a.EmbeddingJSON(), a.VisualEmbedding(), a.TranscriptEmbedding(),
	)
	if err != nil {
		return fmt.Errorf("assets: save asset row: %w", err)
	}

	// Save locations
	for _, loc := range details.Locations {
		if loc.CreatedAt.IsZero() {
			loc.CreatedAt = now
		}
		isPrimaryVal := 0
		if loc.IsPrimary {
			isPrimaryVal = 1
		}
		_, err = tx.ExecContext(ctx, `
			INSERT INTO asset_locations (asset_id, location_kind, uri, external_id, web_view_link, download_url, mime_type, file_size_bytes, file_hash, is_primary, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(asset_id, location_kind) DO UPDATE SET
				uri = excluded.uri,
				external_id = excluded.external_id,
				web_view_link = excluded.web_view_link,
				download_url = excluded.download_url,
				mime_type = excluded.mime_type,
				file_size_bytes = excluded.file_size_bytes,
				file_hash = excluded.file_hash,
				is_primary = excluded.is_primary,
				updated_at = excluded.updated_at
		`, a.ID, string(loc.LocationKind), loc.URI, loc.ExternalID, loc.AccessURL, loc.DownloadURL, loc.MimeType, loc.FileSizeBytes, loc.FileHash, isPrimaryVal, timeutil.FormatRFC3339(loc.CreatedAt), nowStr)
		if err != nil {
			return fmt.Errorf("assets: save location %s: %w", loc.LocationKind, err)
		}
	}

	// Save processing records
	for _, proc := range details.Processing {
		var startStr, compStr sql.NullString
		if proc.StartedAt != nil {
			startStr = sql.NullString{String: timeutil.FormatRFC3339(*proc.StartedAt), Valid: true}
		}
		if proc.CompletedAt != nil {
			compStr = sql.NullString{String: timeutil.FormatRFC3339(*proc.CompletedAt), Valid: true}
		}
		_, err = tx.ExecContext(ctx, `
			INSERT INTO asset_processing (asset_id, step, status, started_at, completed_at, error_message, attempt_count, metadata_json)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(asset_id, step) DO UPDATE SET
				status = excluded.status,
				started_at = excluded.started_at,
				completed_at = excluded.completed_at,
				error_message = excluded.error_message,
				attempt_count = excluded.attempt_count,
				metadata_json = excluded.metadata_json
		`, a.ID, proc.Step, string(proc.Status), startStr, compStr, proc.ErrorMessage, proc.AttemptCount, proc.MetadataJSON)
		if err != nil {
			return fmt.Errorf("assets: save processing %s: %w", proc.Step, err)
		}
	}

	// Save versions
	for _, ver := range details.Versions {
		if ver.CreatedAt.IsZero() {
			ver.CreatedAt = now
		}
		_, err = tx.ExecContext(ctx, `
			INSERT INTO asset_versions (asset_id, version_number, source_uri, file_hash, file_size_bytes, mime_type, metadata_json, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		`, a.ID, ver.VersionNumber, ver.SourceURI, ver.FileHash, ver.FileSizeBytes, ver.MimeType, ver.MetadataJSON, timeutil.FormatRFC3339(ver.CreatedAt))
		if err != nil {
			return fmt.Errorf("assets: save version %d: %w", ver.VersionNumber, err)
		}
	}

	// Write outbox event
	payloadJSON, _ := json.Marshal(a)
	evtID := fmt.Sprintf("outbox_%d_%s", time.Now().UnixNano(), hashutil.RandomString(6))
	_, err = tx.ExecContext(ctx, `
		INSERT INTO outbox_events (id, aggregate_id, event_type, payload_json, created_at)
		VALUES (?, ?, 'asset.upserted', ?, ?)
	`, evtID, a.ID, string(payloadJSON), nowStr)
	if err != nil {
		return fmt.Errorf("assets: save outbox: %w", err)
	}

	return tx.Commit()
}

// Delete marks the asset as DELETED.
func (s *AssetStoreSQLite) Delete(ctx context.Context, id string) error {
	nowStr := timeutil.FormatRFC3339(time.Now())
	_, err := s.db.ExecContext(ctx, `
		UPDATE media_assets
		SET lifecycle_state = 'DELETED', deleted_at = ?, updated_at = ?
		WHERE id = ?
	`, nowStr, nowStr, id)
	return err
}

// ── ArtifactStoreSQLite ───────────────────────────────────────────────

type ArtifactStoreSQLite struct {
	db *sql.DB
}

func NewArtifactStoreSQLite(db *sql.DB) *ArtifactStoreSQLite {
	return &ArtifactStoreSQLite{db: db}
}

var _ ArtifactStore = (*ArtifactStoreSQLite)(nil)

func (s *ArtifactStoreSQLite) Create(ctx context.Context, a *Artifact) error {
	now := time.Now().UTC()
	if a.CreatedAt.IsZero() {
		a.CreatedAt = now
	}
	a.UpdatedAt = now

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO artifacts (id, job_id, kind, status, storage_backend,
			storage_key, sha256, size_bytes, mime_type,
			duration_ms, width, height, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, a.ID, a.JobID, a.Kind, string(a.Status), a.StorageBackend,
		a.StorageKey, a.SHA256, a.SizeBytes, a.MimeType,
		a.DurationMs, a.Width, a.Height,
		timeutil.FormatRFC3339(a.CreatedAt), timeutil.FormatRFC3339(a.UpdatedAt))
	if err != nil {
		return fmt.Errorf("artifacts: create %s: %w", a.ID, err)
	}
	return nil
}

func (s *ArtifactStoreSQLite) Get(ctx context.Context, id string) (*Artifact, error) {
	var a Artifact
	var createdAt, updatedAt string
	var verifiedAt, lastAccessedAt sql.NullString
	var durationMs, width, height sql.NullInt64
	var statusStr string

	err := s.db.QueryRowContext(ctx, `
		SELECT id, job_id, kind, status, storage_backend,
			storage_key, sha256, size_bytes, mime_type,
			duration_ms, width, height,
			created_at, updated_at, verified_at, last_accessed_at
		FROM artifacts WHERE id = ?
	`, id).Scan(
		&a.ID, &a.JobID, &a.Kind, &statusStr, &a.StorageBackend,
		&a.StorageKey, &a.SHA256, &a.SizeBytes, &a.MimeType,
		&durationMs, &width, &height,
		&createdAt, &updatedAt, &verifiedAt, &lastAccessedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("artifacts: get %s: %w", id, err)
	}

	a.Status = ArtifactStatus(statusStr)
	a.CreatedAt = timeutil.ParseRFC3339(createdAt)
	a.UpdatedAt = timeutil.ParseRFC3339(updatedAt)
	if verifiedAt.Valid && verifiedAt.String != "" {
		t := timeutil.ParseRFC3339(verifiedAt.String)
		a.VerifiedAt = &t
	}
	if durationMs.Valid {
		a.DurationMs = int(durationMs.Int64)
	}
	if width.Valid {
		a.Width = int(width.Int64)
	}
	if height.Valid {
		a.Height = int(height.Int64)
	}
	if lastAccessedAt.Valid && lastAccessedAt.String != "" {
		t := timeutil.ParseRFC3339(lastAccessedAt.String)
		a.LastAccessedAt = &t
	}
	return &a, nil
}

func (s *ArtifactStoreSQLite) GetBySHA256(ctx context.Context, sha256 string) (*Artifact, error) {
	var a Artifact
	var createdAt, updatedAt string
	var verifiedAt, lastAccessedAt sql.NullString
	var durationMs, width, height sql.NullInt64
	var statusStr string

	err := s.db.QueryRowContext(ctx, `
		SELECT id, job_id, kind, status, storage_backend,
			storage_key, sha256, size_bytes, mime_type,
			duration_ms, width, height,
			created_at, updated_at, verified_at, last_accessed_at
		FROM artifacts WHERE sha256 = ? AND status != 'DELETED'
		LIMIT 1
	`, sha256).Scan(
		&a.ID, &a.JobID, &a.Kind, &statusStr, &a.StorageBackend,
		&a.StorageKey, &a.SHA256, &a.SizeBytes, &a.MimeType,
		&durationMs, &width, &height,
		&createdAt, &updatedAt, &verifiedAt, &lastAccessedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("artifacts: get by sha256: %w", err)
	}

	a.Status = ArtifactStatus(statusStr)
	a.CreatedAt = timeutil.ParseRFC3339(createdAt)
	a.UpdatedAt = timeutil.ParseRFC3339(updatedAt)
	if verifiedAt.Valid && verifiedAt.String != "" {
		t := timeutil.ParseRFC3339(verifiedAt.String)
		a.VerifiedAt = &t
	}
	if durationMs.Valid {
		a.DurationMs = int(durationMs.Int64)
	}
	if width.Valid {
		a.Width = int(width.Int64)
	}
	if height.Valid {
		a.Height = int(height.Int64)
	}
	if lastAccessedAt.Valid && lastAccessedAt.String != "" {
		t := timeutil.ParseRFC3339(lastAccessedAt.String)
		a.LastAccessedAt = &t
	}
	return &a, nil
}

func (s *ArtifactStoreSQLite) UpdateStatus(ctx context.Context, id string, status ArtifactStatus, sha256 string, sizeBytes int64) error {
	now := timeutil.FormatRFC3339(time.Now().UTC())
	_, err := s.db.ExecContext(ctx, `
		UPDATE artifacts
		SET status = ?, sha256 = ?, size_bytes = ?, updated_at = ?,
			verified_at = CASE WHEN ? = 'READY' THEN ? ELSE verified_at END
		WHERE id = ?
	`, string(status), sha256, sizeBytes, now, string(status), now, id)
	if err != nil {
		return fmt.Errorf("artifacts: update status %s: %w", id, err)
	}
	return nil
}

func (s *ArtifactStoreSQLite) ListByJob(ctx context.Context, jobID string) ([]*Artifact, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, job_id, kind, status, storage_backend,
			storage_key, sha256, size_bytes, mime_type,
			duration_ms, width, height,
			created_at, updated_at, verified_at, last_accessed_at
		FROM artifacts WHERE job_id = ? ORDER BY created_at
	`, jobID)
	if err != nil {
		return nil, fmt.Errorf("artifacts: list by job %s: %w", jobID, err)
	}
	defer rows.Close()

	var list []*Artifact
	for rows.Next() {
		var a Artifact
		var createdAt, updatedAt string
		var verifiedAt, lastAccessedAt sql.NullString
		var durationMs, width, height sql.NullInt64
		var statusStr string
		if err := rows.Scan(
			&a.ID, &a.JobID, &a.Kind, &statusStr, &a.StorageBackend,
			&a.StorageKey, &a.SHA256, &a.SizeBytes, &a.MimeType,
			&durationMs, &width, &height,
			&createdAt, &updatedAt, &verifiedAt, &lastAccessedAt,
		); err != nil {
			return nil, fmt.Errorf("artifacts: scan list: %w", err)
		}
		a.Status = ArtifactStatus(statusStr)
		a.CreatedAt = timeutil.ParseRFC3339(createdAt)
		a.UpdatedAt = timeutil.ParseRFC3339(updatedAt)
		if verifiedAt.Valid && verifiedAt.String != "" {
			t := timeutil.ParseRFC3339(verifiedAt.String)
			a.VerifiedAt = &t
		}
		if durationMs.Valid {
			a.DurationMs = int(durationMs.Int64)
		}
		if width.Valid {
			a.Width = int(width.Int64)
		}
		if height.Valid {
			a.Height = int(height.Int64)
		}
		if lastAccessedAt.Valid && lastAccessedAt.String != "" {
			t := timeutil.ParseRFC3339(lastAccessedAt.String)
			a.LastAccessedAt = &t
		}
		list = append(list, &a)
	}
	return list, nil
}

// ── DeliveryStoreSQLite ───────────────────────────────────────────────

type DeliveryStoreSQLite struct {
	db *sql.DB
}

func NewDeliveryStoreSQLite(db *sql.DB) *DeliveryStoreSQLite {
	return &DeliveryStoreSQLite{db: db}
}

var _ DeliveryStore = (*DeliveryStoreSQLite)(nil)

func (s *DeliveryStoreSQLite) Create(ctx context.Context, d *Delivery) error {
	now := time.Now().UTC()
	if d.CreatedAt.IsZero() {
		d.CreatedAt = now
	}
	d.UpdatedAt = now

	if d.IdempotencyKey == "" {
		raw := d.ArtifactID + d.DestinationID + d.Provider
		h := sha256.Sum256([]byte(raw))
		d.IdempotencyKey = hex.EncodeToString(h[:])
	}

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO deliveries (id, artifact_id, destination_id, provider, status,
			attempt_count, max_attempts, next_attempt_at, locked_by, locked_until,
			remote_id, remote_url, last_error_code, last_error_message,
			idempotency_key, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, d.ID, d.ArtifactID, d.DestinationID, d.Provider, string(d.Status),
		d.AttemptCount, d.MaxAttempts, timeutil.FormatPtrRFC3339(d.NextAttemptAt),
		d.LockedBy, timeutil.FormatPtrRFC3339(d.LockedUntil), d.RemoteID, d.RemoteURL,
		d.LastErrorCode, d.LastErrorMessage, d.IdempotencyKey,
		timeutil.FormatRFC3339(d.CreatedAt), timeutil.FormatRFC3339(d.UpdatedAt))
	if err != nil {
		return fmt.Errorf("deliveries: create %s: %w", d.ID, err)
	}
	return nil
}

func (s *DeliveryStoreSQLite) Get(ctx context.Context, id string) (*Delivery, error) {
	var d Delivery
	var nextAttempt, lockedUntil, completedAt sql.NullString
	var createdAt, updatedAt string
	var statusStr string

	err := s.db.QueryRowContext(ctx, `
		SELECT id, artifact_id, destination_id, provider, status,
			attempt_count, max_attempts, next_attempt_at, locked_by, locked_until,
			remote_id, remote_url, last_error_code, last_error_message,
			idempotency_key, created_at, updated_at, completed_at
		FROM deliveries WHERE id = ?
	`, id).Scan(
		&d.ID, &d.ArtifactID, &d.DestinationID, &d.Provider, &statusStr,
		&d.AttemptCount, &d.MaxAttempts, &nextAttempt, &d.LockedBy, &lockedUntil,
		&d.RemoteID, &d.RemoteURL, &d.LastErrorCode, &d.LastErrorMessage,
		&d.IdempotencyKey, &createdAt, &updatedAt, &completedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("deliveries: get %s: %w", id, err)
	}

	d.Status = DeliveryStatus(statusStr)
	d.CreatedAt = timeutil.ParseRFC3339(createdAt)
	d.UpdatedAt = timeutil.ParseRFC3339(updatedAt)
	if nextAttempt.Valid && nextAttempt.String != "" {
		t := timeutil.ParseRFC3339(nextAttempt.String)
		d.NextAttemptAt = &t
	}
	if lockedUntil.Valid && lockedUntil.String != "" {
		t := timeutil.ParseRFC3339(lockedUntil.String)
		d.LockedUntil = &t
	}
	if completedAt.Valid && completedAt.String != "" {
		t := timeutil.ParseRFC3339(completedAt.String)
		d.CompletedAt = &t
	}
	return &d, nil
}

func (s *DeliveryStoreSQLite) Update(ctx context.Context, d *Delivery) error {
	now := time.Now().UTC()
	d.UpdatedAt = now
	completedAtStr := ""
	if d.CompletedAt != nil {
		completedAtStr = timeutil.FormatRFC3339(*d.CompletedAt)
	}

	_, err := s.db.ExecContext(ctx, `
		UPDATE deliveries
		SET status = ?, attempt_count = ?, max_attempts = ?, next_attempt_at = ?,
			locked_by = ?, locked_until = ?, remote_id = ?, remote_url = ?,
			last_error_code = ?, last_error_message = ?, updated_at = ?, completed_at = ?
		WHERE id = ?
	`, string(d.Status), d.AttemptCount, d.MaxAttempts, timeutil.FormatPtrRFC3339(d.NextAttemptAt),
		d.LockedBy, timeutil.FormatPtrRFC3339(d.LockedUntil), d.RemoteID, d.RemoteURL,
		d.LastErrorCode, d.LastErrorMessage, timeutil.FormatRFC3339(d.UpdatedAt), completedAtStr, d.ID)
	if err != nil {
		return fmt.Errorf("deliveries: update %s: %w", d.ID, err)
	}
	return nil
}

func (s *DeliveryStoreSQLite) ListPending(ctx context.Context) ([]*Delivery, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id FROM deliveries
		WHERE status IN ('PENDING', 'RETRY_WAIT')
			AND (next_attempt_at IS NULL OR next_attempt_at <= ?)
		ORDER BY created_at ASC
	`, timeutil.FormatRFC3339(time.Now().UTC()))
	if err != nil {
		return nil, fmt.Errorf("deliveries: list pending: %w", err)
	}
	defer rows.Close()

	var list []*Delivery
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		d, err := s.Get(ctx, id)
		if err != nil {
			return nil, err
		}
		if d != nil {
			list = append(list, d)
		}
	}
	return list, nil
}
