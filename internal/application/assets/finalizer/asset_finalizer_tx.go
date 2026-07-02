package finalizer

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/finalization"
	timeutil "github.com/Marcuss-ops/PipelineGen/pkg/timeutil"
	"go.uber.org/zap"
)

// AssetTxFinalizer is the concrete implementation of
// finalization.AssetFinalizerTx.
//
// It writes the canonical media_assets, asset_versions, and asset_locations
// records inside the caller's transaction. It participates in the
// JobFinalizer's transaction via the finalization.Transaction interface —
// it never opens its own transaction.
//
// For each PublishedArtifact, it produces:
//   - ArtifactRef (lightweight reference for downstream consumers)
//   - []OutboxEvent (indexing requests for Qdrant)
//
// Schema tables written (inside the caller's tx):
//
//	media_assets    — INSERT ... ON CONFLICT(id) DO UPDATE (canonical asset)
//	asset_versions  — INSERT with MAX(version_number)+1 (sequential version)
//	asset_locations — INSERT ... ON CONFLICT(asset_id, location_kind) DO UPDATE
//
// Canonical reference: Piano d'Azione Completo § 5.1–5.2.
type AssetTxFinalizer struct {
	log *zap.Logger
}

// NewAssetTxFinalizer creates an AssetTxFinalizer.
func NewAssetTxFinalizer(log *zap.Logger) *AssetTxFinalizer {
	if log == nil {
		log = zap.NewNop()
	}
	return &AssetTxFinalizer{log: log}
}

// Compile-time assertion.
var _ finalization.AssetFinalizerTx = (*AssetTxFinalizer)(nil)

// FinalizeAsset writes the canonical asset, version, and location records
// for a published artifact inside the caller's transaction.
//
// The caller (JobFinalizer) owns the transaction lifecycle — AssetTxFinalizer
// only executes SQL via the Transaction surface.
func (s *AssetTxFinalizer) FinalizeAsset(
	ctx context.Context,
	tx finalization.Transaction,
	artifact finalization.PublishedArtifact,
) (finalization.ArtifactRef, []finalization.OutboxEvent, error) {
	if artifact.ArtifactID == "" {
		return finalization.ArtifactRef{}, nil,
			fmt.Errorf("asset finalizer: ArtifactID is empty")
	}

	nowStr := timeutil.FormatRFC3339(time.Now())

	// 1. UPSERT media_assets — canonical asset row.
	if err := s.upsertMediaAsset(ctx, tx, &artifact, nowStr); err != nil {
		return finalization.ArtifactRef{}, nil, err
	}

	// 2. INSERT asset_versions — new version row.
	versionNum, err := s.insertAssetVersion(ctx, tx, &artifact, nowStr)
	if err != nil {
		return finalization.ArtifactRef{}, nil, err
	}

	// 3. UPSERT asset_locations — canonical location row.
	if err := s.upsertAssetLocation(ctx, tx, &artifact, nowStr); err != nil {
		return finalization.ArtifactRef{}, nil, err
	}

	// 4. Build ArtifactRef and outbox events.
	ref := finalization.ArtifactRef{
		ArtifactID:    artifact.ArtifactID,
		AssetID:       artifact.ArtifactID, // AssetID = ArtifactID (logical identity)
		Kind:          artifact.Kind,
		SourceVersion: int64(versionNum),
		ContentHash:   artifact.SHA256,
		Location:      artifact.Location,
	}

	// Outbox event: index this asset in Qdrant.
	indexPayload, _ := json.Marshal(map[string]any{
		"asset_id":    artifact.ArtifactID,
		"kind":        string(artifact.Kind),
		"sha256":      artifact.SHA256,
		"location":    artifact.Location.Provider,
		"file_id":     artifact.Location.FileID,
		"version":     versionNum,
		"idempotency_key": artifact.IdempotencyKey,
	})
	events := []finalization.OutboxEvent{
		{
			EventType:   "asset.index_requested.v1",
			AggregateID: artifact.ArtifactID,
			EventKey:    fmt.Sprintf("index:%s:v%d", artifact.ArtifactID, versionNum),
			Payload:     json.RawMessage(indexPayload),
		},
	}

	s.log.Debug("asset finalised in tx",
		zap.String("artifact_id", artifact.ArtifactID),
		zap.Int("version", versionNum),
		zap.String("media_type", kindToMediaType(artifact.Kind)),
	)

	return ref, events, nil
}

// ── SQL helpers ─────────────────────────────────────────────────────

// upsertMediaAsset inserts or updates the canonical media_assets row.
func (s *AssetTxFinalizer) upsertMediaAsset(
	ctx context.Context,
	tx finalization.Transaction,
	a *finalization.PublishedArtifact,
	nowStr string,
) error {
	mediaType := kindToMediaType(a.Kind)
	metadata := map[string]any{
		"publish_action": string(a.Location.Action),
		"source_version": a.SourceVersion,
		"size_bytes":     a.SizeBytes,
	}
	metadataJSON, _ := json.Marshal(metadata)
	actionStr := string(a.Location.Action)

	_, err := tx.ExecContext(ctx, `
		INSERT INTO media_assets (
			id, source, name, filename, media_type,
			file_hash, drive_file_id, drive_link, download_link,
			folder_id, folder_path, lifecycle_state,
			metadata_json, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'ACTIVE', ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			filename = excluded.filename,
			media_type = excluded.media_type,
			file_hash = excluded.file_hash,
			drive_file_id = excluded.drive_file_id,
			drive_link = excluded.drive_link,
			download_link = excluded.download_link,
			folder_id = excluded.folder_id,
			folder_path = excluded.folder_path,
			metadata_json = excluded.metadata_json,
			updated_at = excluded.updated_at
	`,
		a.ArtifactID,
		actionStr,
		a.Filename,
		a.Filename,
		mediaType,
		a.SHA256,
		a.Location.FileID,
		a.Location.WebViewLink,
		a.Location.DownloadLink,
		a.Location.FolderID,
		a.Location.FolderPath,
		string(metadataJSON),
		nowStr,
		nowStr,
	)
	if err != nil {
		return fmt.Errorf("asset finalizer: upsert media_asset %s: %w", a.ArtifactID, err)
	}
	return nil
}

// insertAssetVersion inserts a new version row with MAX(version_number)+1.
func (s *AssetTxFinalizer) insertAssetVersion(
	ctx context.Context,
	tx finalization.Transaction,
	a *finalization.PublishedArtifact,
	nowStr string,
) (int, error) {
	// Compute next version_number inside the transaction.
	var nextVer int
	row := tx.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(version_number), 0) + 1 FROM asset_versions WHERE asset_id = ?`,
		a.ArtifactID,
	)
	if err := row.Scan(&nextVer); err != nil {
		return 0, fmt.Errorf("asset finalizer: compute next version for %s: %w", a.ArtifactID, err)
	}

	_, err := tx.ExecContext(ctx, `
		INSERT INTO asset_versions
			(asset_id, version_number, source_uri, file_hash, file_size_bytes, mime_type, metadata_json, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`,
		a.ArtifactID,
		nextVer,
		a.Location.FileID, // source_uri — where this version came from
		a.SHA256,
		a.SizeBytes,
		a.MIMEType,
		"{}",
		nowStr,
	)
	if err != nil {
		return 0, fmt.Errorf("asset finalizer: insert version %d for %s: %w", nextVer, a.ArtifactID, err)
	}

	return nextVer, nil
}

// upsertAssetLocation inserts or updates the canonical asset_locations row.
func (s *AssetTxFinalizer) upsertAssetLocation(
	ctx context.Context,
	tx finalization.Transaction,
	a *finalization.PublishedArtifact,
	nowStr string,
) error {
	locationKind := a.Location.Provider
	if locationKind == "" {
		locationKind = "drive"
	}

	_, err := tx.ExecContext(ctx, `
		INSERT INTO asset_locations
			(asset_id, location_kind, uri, external_id, web_view_link, download_url,
			 mime_type, file_size_bytes, file_hash, is_primary, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 1, ?, ?)
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
	`,
		a.ArtifactID,
		locationKind,
		a.Location.FileID,
		a.Location.FileID,
		a.Location.WebViewLink,
		a.Location.DownloadLink,
		a.MIMEType,
		a.SizeBytes,
		a.SHA256,
		nowStr,
		nowStr,
	)
	if err != nil {
		return fmt.Errorf("asset finalizer: upsert location for %s: %w", a.ArtifactID, err)
	}
	return nil
}

// ── Helpers ─────────────────────────────────────────────────────────

// kindToMediaType maps a domain ArtifactKind to a media_type string
// suitable for the media_assets.media_type column.
func kindToMediaType(k finalization.ArtifactKind) string {
	switch k {
	case finalization.KindVideo:
		return "video"
	case finalization.KindImage:
		return "image"
	case finalization.KindAudio, finalization.KindVoiceover, finalization.KindSoundEffect:
		return "audio"
	case finalization.KindDocument:
		return "document"
	case finalization.KindScript:
		return "text"
	case finalization.KindMetadata:
		return "metadata"
	case finalization.KindArchive:
		return "archive"
	default:
		return "other"
	}
}

