// Package finalizer contains the canonical caller-owned transaction asset
// finalizer. Durable asset writes are delegated to the AssetCommitter port.
package finalizer

import (
	"context"
	"fmt"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/persistence"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/texttracks"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/finalization"
	timeutil "github.com/Marcuss-ops/PipelineGen/pkg/timeutil"
)

// AssetTxFinalizer implements finalization.AssetFinalizerTx without owning the
// transaction lifecycle. The caller opens, commits and rolls back the tx.
type AssetTxFinalizer struct {
	log       *zap.Logger
	fanout    *texttracks.MaterializeFanOut
	committer persistence.AssetCommitter
}

func NewAssetTxFinalizer(log *zap.Logger, committer persistence.AssetCommitter) *AssetTxFinalizer {
	if log == nil {
		log = zap.NewNop()
	}
	return &AssetTxFinalizer{log: log, committer: committer}
}

// WithFanOut attaches the sole post-commit text-track fan-out seam.
func (s *AssetTxFinalizer) WithFanOut(fanout *texttracks.MaterializeFanOut) *AssetTxFinalizer {
	if s == nil {
		return s
	}
	s.fanout = fanout
	return s
}

var _ finalization.AssetFinalizerTx = (*AssetTxFinalizer)(nil)

// FinalizeAsset validates the artifact and delegates all durable writes to the
// canonical committer path.
func (s *AssetTxFinalizer) FinalizeAsset(
	ctx context.Context,
	tx finalization.Transaction,
	artifact finalization.PublishedArtifact,
) (finalization.ArtifactRef, []finalization.OutboxEvent, error) {
	if artifact.ArtifactID == "" {
		return finalization.ArtifactRef{}, nil, fmt.Errorf("asset finalizer: ArtifactID is empty")
	}
	if s == nil || s.committer == nil {
		return finalization.ArtifactRef{}, nil, fmt.Errorf("asset finalizer: AssetCommitter is required")
	}

	if skipCatalogCommit(artifact) {
		// PR-STOCK-METADATA-LOCAL-ONLY (Sept 2026): a metadata artifact that
		// was deliberately never published (Stock SkipMetadataUpload) has no
		// Drive location and must not produce a media_assets row / asset_versions
		// row / index event. Returning a zero ArtifactRef (nil error) is the
		// "nothing durable was written" signal: artifact_writer drops zero refs
		// so the finalization result lists only artifacts that really exist.
		s.log.Debug("asset finalizer: unpublished metadata artifact skipped (no catalog row)",
			zap.String("artifact_id", artifact.ArtifactID))
		return finalization.ArtifactRef{}, nil, nil
	}

	nowStr := timeutil.FormatRFC3339(time.Now())
	return s.finalizeWithCommitter(ctx, tx, artifact, nowStr)
}

// skipCatalogCommit reports whether the artifact must stay out of the media
// catalog even though the run still declares it.
//
// Contract (exactly three conditions, all required):
//   - KindMetadata: only the run envelope is eligible today; a video/audio/
//     document artifact without a location is a publish FAILURE and must keep
//     failing loudly through the normal commit path.
//   - no remote location: an artifact with a FileID/WebViewLink is a real
//     remote asset and is always committed.
//   - drive_upload_skipped: the producer explicitly declared the omission
//     (Stock sets it when RuntimeConfig.SkipMetadataUpload is on), so the
//     absence of a location is a decision, not an accident.
func skipCatalogCommit(artifact finalization.PublishedArtifact) bool {
	if artifact.Kind != finalization.KindMetadata {
		return false
	}
	if artifact.Location.FileID != "" || artifact.Location.WebViewLink != "" {
		return false
	}
	skipped, ok := artifact.ArtifactMetadata["drive_upload_skipped"].(bool)
	return ok && skipped
}

// kindToMediaType maps the domain artifact kind to media_assets.media_type.
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
