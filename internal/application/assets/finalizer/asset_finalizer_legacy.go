package finalizer

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/finalization"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outboxevents"
	"go.uber.org/zap"
)

// finalizeLegacy is the pre-AssetCommitter implementation. It remains for
// callers that have not completed the canonical committer cutover.
func (s *AssetTxFinalizer) finalizeLegacy(
	ctx context.Context,
	tx finalization.Transaction,
	artifact finalization.PublishedArtifact,
	nowStr string,
) (finalization.ArtifactRef, []finalization.OutboxEvent, error) {
	if err := s.upsertMediaAsset(ctx, tx, &artifact, nowStr); err != nil {
		return finalization.ArtifactRef{}, nil, err
	}
	versionNum, err := s.insertAssetVersion(ctx, tx, &artifact, nowStr)
	if err != nil {
		return finalization.ArtifactRef{}, nil, err
	}
	if err := s.upsertAssetLocation(ctx, tx, &artifact, nowStr); err != nil {
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

	eventID := uuid.NewString()
	eventKey := fmt.Sprintf("index:%s:%s", artifact.ArtifactID, artifact.SHA256)
	source := artifact.Source
	if source == "" {
		source = string(artifact.Location.Action)
	}
	indexPayload, err := json.Marshal(map[string]any{
		"schema_version":  outboxevents.ReindexEnvelopeV1Schema,
		"event_id":        eventID,
		"asset_id":        artifact.ArtifactID,
		"operation":       "UPSERT",
		"source":          source,
		"media_type":      kindToMediaType(artifact.Kind),
		"source_version":  artifact.SHA256,
		"idempotency_key": eventKey,
	})
	if err != nil {
		return finalization.ArtifactRef{}, nil, fmt.Errorf("asset finalizer: marshal index payload: %w", err)
	}
	events := []finalization.OutboxEvent{
		{
			EventType:   outboxevents.EventAssetIndexRequested,
			AggregateID: artifact.ArtifactID,
			EventKey:    eventKey,
			Payload:     json.RawMessage(indexPayload),
		},
	}
	if err := s.insertOutboxEvent(ctx, tx, events[0], nowStr); err != nil {
		return finalization.ArtifactRef{}, nil, err
	}

	s.log.Debug("asset finalised in tx",
		zap.String("artifact_id", artifact.ArtifactID),
		zap.Int("version", versionNum),
		zap.String("media_type", kindToMediaType(artifact.Kind)),
	)
	return ref, events, nil
}
