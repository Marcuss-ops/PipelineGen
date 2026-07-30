package finalizer

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/persistence"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/finalization"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"go.uber.org/zap"
)

// finalizeWithCommitter uses persistence.AssetCommitter for the canonical
// asset commit, then writes asset_versions and asset_renditions on top.
func (s *AssetTxFinalizer) finalizeWithCommitter(
	ctx context.Context,
	tx finalization.Transaction,
	artifact finalization.PublishedArtifact,
	nowStr string,
) (finalization.ArtifactRef, []finalization.OutboxEvent, error) {
	req := s.buildCommitRequest(artifact)

	sqlTx, ok := UnwrapSQLTx(tx)
	if !ok {
		return finalization.ArtifactRef{}, nil, fmt.Errorf("asset finalizer: transaction is not *sql.Tx")
	}
	res, err := s.committer.CommitTx(ctx, sqlTx, req)
	if err != nil {
		return finalization.ArtifactRef{}, nil, fmt.Errorf("asset finalizer: commit asset: %w", err)
	}
	_ = res
	var events []finalization.OutboxEvent
	if res.OutboxEventKey != "" {
		var eventType, aggregateID, payload string
		if err := sqlTx.QueryRowContext(ctx, `
			SELECT event_type, aggregate_id, payload_json
			FROM outbox_events WHERE event_key = ?`, res.OutboxEventKey).
			Scan(&eventType, &aggregateID, &payload); err != nil {
			return finalization.ArtifactRef{}, nil, fmt.Errorf("asset finalizer: read committed outbox event: %w", err)
		}
		events = append(events, finalization.OutboxEvent{
			EventType:   eventType,
			AggregateID: aggregateID,
			EventKey:    res.OutboxEventKey,
			Payload:     json.RawMessage(payload),
		})
	}

	// Populate backward-compatible media_assets columns (width, height, local_path, source_provider, source_version)
	// for media querying scripts and tests that expect them at the top-level asset schema.
	var width, height int
	var localPath string
	for i := range artifact.Renditions {
		k := strings.ToLower(artifact.Renditions[i].Kind)
		if strings.Contains(k, "mezzanine") || strings.Contains(k, "master") {
			width = artifact.Renditions[i].Width
			height = artifact.Renditions[i].Height
			localPath = artifact.Renditions[i].URI
			if strings.Contains(k, "mezzanine") {
				break
			}
		}
	}
	if width == 0 {
		width = 1920
	}
	if height == 0 {
		height = 1080
	}
	sourceProvider := artifact.Source
	if sourceProvider == "" {
		sourceProvider = "artlist"
	}
	sourceVersion := artifact.SHA256

	_, err = sqlTx.ExecContext(ctx, `
		UPDATE media_assets
		SET width = ?, height = ?, local_path = ?, source_provider = ?, source_version = ?
		WHERE id = ?
	`, width, height, localPath, sourceProvider, sourceVersion, artifact.ArtifactID)
	if err != nil {
		return finalization.ArtifactRef{}, nil, fmt.Errorf("asset finalizer: update media_assets backward compat: %w", err)
	}

	versionNum, err := s.insertAssetVersion(ctx, tx, &artifact, nowStr)
	if err != nil {
		return finalization.ArtifactRef{}, nil, err
	}
	for i := range artifact.Renditions {
		if err := s.upsertRenditionLocation(ctx, tx, &artifact, &artifact.Renditions[i], nowStr); err != nil {
			return finalization.ArtifactRef{}, nil, err
		}
	}

	ref := finalization.ArtifactRef{
		ArtifactID:    artifact.ArtifactID,
		AssetID:       artifact.ArtifactID,
		Kind:          artifact.Kind,
		SourceVersion: int64(versionNum),
		ContentHash:   artifact.SHA256,
		Location:      artifact.Location,
	}

	s.log.Debug("asset finalised in tx (via AssetCommitter)",
		zap.String("artifact_id", artifact.ArtifactID),
		zap.Int("version", versionNum),
		zap.String("media_type", kindToMediaType(artifact.Kind)),
	)
	return ref, events, nil
}

// buildCommitRequest translates a PublishedArtifact into the canonical
// persistence.CommitRequest.
func (s *AssetTxFinalizer) buildCommitRequest(artifact finalization.PublishedArtifact) persistence.CommitRequest {
	source := artifact.Source
	if source == "" {
		source = string(artifact.Location.Action)
	}
	mediaType := kindToMediaType(artifact.Kind)

	metadata := persistence.TypedMetadata{
		Description:   artifact.Description,
		PublishAction: string(artifact.Location.Action),
		SizeBytes:     artifact.SizeBytes,
	}
	if len(artifact.ArtifactMetadata) > 0 {
		metadata.Extra = make(map[string]any, len(artifact.ArtifactMetadata))
		for k, v := range artifact.ArtifactMetadata {
			metadata.Extra[k] = v
		}
	}

	locations := []persistence.LocationCommit{
		{
			Kind:          artifact.Location.Provider,
			Provider:      artifact.Location.Provider,
			ExternalID:    artifact.Location.FileID,
			URI:           primaryURI(artifact.Location),
			WebViewLink:   artifact.Location.WebViewLink,
			DownloadURL:   artifact.Location.DownloadLink,
			MimeType:      artifact.MIMEType,
			FileSizeBytes: artifact.SizeBytes,
			FileHash:      artifact.SHA256,
			IsPrimary:     true,
		},
	}

	var durationMs int64
	if raw, ok := artifact.ArtifactMetadata["chunk_duration_sec"]; ok {
		if sec, conv := raw.(float64); conv && sec > 0 {
			durationMs = int64(sec * 1000)
		}
	} else if artifact.SizeBytes > 0 && artifact.MIMEType != "" {
		durationMs = artifact.SizeBytes / 250000
	}

	_, initIndex := asset.NewIndexableAssetState()
	return persistence.CommitRequest{
		AssetID:        artifact.ArtifactID,
		Source:         source,
		Name:           artifact.Filename,
		Filename:       artifact.Filename,
		MediaType:      mediaType,
		ContentHash:    artifact.SHA256,
		Description:    artifact.Description,
		DurationMs:     durationMs,
		LifecycleState: string(asset.StatePublished),
		IndexState:     string(initIndex),
		FolderID:       artifact.Location.FolderID,
		FolderPath:     artifact.Location.FolderPath,
		Metadata:       metadata,
		Locations:      locations,
		EmitIndexEvent: true,
		RequestedAt:    time.Now(),
	}
}

func primaryURI(loc finalization.AssetLocation) string {
	if loc.WebViewLink != "" {
		return loc.WebViewLink
	}
	return loc.DownloadLink
}
