package assets

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/persistence"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/finalization"
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

func artifactLocalPath(artifact finalization.PublishedArtifact) string {
	for i := range artifact.Renditions {
		kind := strings.ToLower(artifact.Renditions[i].Kind)
		if strings.Contains(kind, "mezzanine") || strings.Contains(kind, "master") {
			if artifact.Renditions[i].URI != "" {
				return artifact.Renditions[i].URI
			}
		}
	}
	if value, ok := artifact.ArtifactMetadata["local_path"].(string); ok {
		return value
	}
	return ""
}

func metadataInt(value any) int {
	switch v := value.(type) {
	case int:
		return v
	case int64:
		return int(v)
	case int32:
		return int(v)
	case float64:
		return int(v)
	case float32:
		return int(v)
	default:
		return 0
	}
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
	if value, ok := artifact.ArtifactMetadata["source_provider"].(string); ok {
		metadata.SourceProvider = value
	}
	if value, ok := artifact.ArtifactMetadata["source_video_id"].(string); ok {
		metadata.SourceVideoID = value
	}
	var sourceURL string
	if value, ok := artifact.ArtifactMetadata["source_url"].(string); ok {
		sourceURL = value
		metadata.Extra = ensureMetadataExtra(metadata.Extra)
		metadata.Extra["source_url"] = value
	}
	if value, ok := artifact.ArtifactMetadata["category"].(string); ok {
		metadata.Category = value
	}
	if value, ok := artifact.ArtifactMetadata["start_sec"].(float64); ok {
		metadata.StartSec = value
	}
	if value, ok := artifact.ArtifactMetadata["end_sec"].(float64); ok {
		metadata.EndSec = value
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
			LegacyFileMD5: artifact.SHA256,
			IsPrimary:     true,
		},
	}

	var durationMs int64
	if raw, ok := artifact.ArtifactMetadata["duration_ms"]; ok {
		durationMs = int64(metadataInt(raw))
	}
	if durationMs <= 0 {
		if raw, ok := artifact.ArtifactMetadata["chunk_duration_sec"]; ok {
			if sec, conv := raw.(float64); conv && sec > 0 {
				durationMs = int64(sec * 1000)
			}
		}
	}
	if durationMs <= 0 && artifact.SizeBytes > 0 && artifact.MIMEType != "" {
		durationMs = artifact.SizeBytes / 250000
	}

	_, initIndex := asset.NewIndexableAssetState()
	searchIndexable := mediaType == "video" || mediaType == "image"
	if !searchIndexable {
		initIndex = ""
	}
	lifecycleState := string(asset.StatePublished)
	if source == "youtube" {
		lifecycleState = string(asset.StateActive)
	}
	localPath := artifactLocalPath(artifact)
	sourceProvider := metadata.SourceProvider
	if sourceProvider == "" {
		sourceProvider = source
	}
	sourceVersion := metadata.SourceVersion
	if sourceVersion == "" {
		sourceVersion = artifact.SHA256
	}
	metadata.SourceVersion = sourceVersion
	metadata.SourceProvider = sourceProvider
	// Script-required acquisition: the stock pipeline finalizer emits
	// asset.index.requested events whose assets unblock script
	// generation. Stamp the outbox event with the high priority so
	// ClaimNext claims it before a bulk-folder-sync backlog (migration
	// 186 / outboxevents.PriorityHigh).
	indexPriority := 0
	if source == "youtube" || source == "stock" {
		indexPriority = persistence.IndexPriorityHigh
	}
	return persistence.CommitRequest{
		AssetID:        artifact.ArtifactID,
		Source:         source,
		Name:           artifact.Filename,
		Filename:       artifact.Filename,
		MediaType:      mediaType,
		Category:       metadata.Category,
		ContentHash:    artifact.SHA256,
		Description:    artifact.Description,
		DurationMs:     durationMs,
		LifecycleState: lifecycleState,
		IndexState:     string(initIndex),
		LocalPath:      localPath,
		FolderID:       artifact.Location.FolderID,
		FolderPath:     artifact.Location.FolderPath,
		SourceURL:      sourceURL,
		SourceProvider: sourceProvider,
		SourceVideoID:  metadata.SourceVideoID,
		StartMs:        int64(metadata.StartSec * 1000),
		EndMs:          int64(metadata.EndSec * 1000),
		Metadata:       metadata,
		Locations:      locations,
		EmitIndexEvent: searchIndexable,
		RequestedAt:    time.Now(),
		IndexPriority:  indexPriority,
	}
}

func ensureMetadataExtra(extra map[string]any) map[string]any {
	if extra == nil {
		return make(map[string]any)
	}
	return extra
}

func primaryURI(loc finalization.AssetLocation) string {
	if loc.WebViewLink != "" {
		return loc.WebViewLink
	}
	return loc.DownloadLink
}
