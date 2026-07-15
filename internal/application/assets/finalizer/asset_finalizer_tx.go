// Package finalizer contains the canonical caller-owned transaction asset
// finalizer. SQL table writers, committer integration, legacy compatibility and
// post-commit fan-out are separated into focused sibling files.
package finalizer

import (
	"context"
	"fmt"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/persistence"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/texttracks"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/finalization"
	timeutil "github.com/Marcuss-ops/PipelineGen/pkg/timeutil"
	"go.uber.org/zap"
)

// AssetTxFinalizer implements finalization.AssetFinalizerTx without owning the
// transaction lifecycle. The caller opens, commits and rolls back the tx.
type AssetTxFinalizer struct {
	log       *zap.Logger
	fanout    *texttracks.MaterializeFanOut
	committer persistence.AssetCommitter
}

func NewAssetTxFinalizer(log *zap.Logger) *AssetTxFinalizer {
	if log == nil {
		log = zap.NewNop()
	}
	return &AssetTxFinalizer{log: log}
}

// WithFanOut attaches the sole post-commit text-track fan-out seam.
func (s *AssetTxFinalizer) WithFanOut(fanout *texttracks.MaterializeFanOut) *AssetTxFinalizer {
	if s == nil {
		return s
	}
	s.fanout = fanout
	return s
}

// WithCommitter attaches the canonical AssetCommitter. When present, the
// finalizer delegates canonical asset, location, metadata and index-event writes
// to that surface.
func (s *AssetTxFinalizer) WithCommitter(committer persistence.AssetCommitter) *AssetTxFinalizer {
	if s == nil {
		return s
	}
	s.committer = committer
	return s
}

var _ finalization.AssetFinalizerTx = (*AssetTxFinalizer)(nil)

// FinalizeAsset validates the artifact and selects the canonical committer path
// or the temporary legacy path. Both paths preserve the required write order:
// asset, version, primary location, renditions, then outbox.
func (s *AssetTxFinalizer) FinalizeAsset(
	ctx context.Context,
	tx finalization.Transaction,
	artifact finalization.PublishedArtifact,
) (finalization.ArtifactRef, []finalization.OutboxEvent, error) {
	if artifact.ArtifactID == "" {
		return finalization.ArtifactRef{}, nil, fmt.Errorf("asset finalizer: ArtifactID is empty")
	}

	nowStr := timeutil.FormatRFC3339(time.Now())
	if s.committer != nil {
		return s.finalizeWithCommitter(ctx, tx, artifact, nowStr)
	}
	return s.finalizeLegacy(ctx, tx, artifact, nowStr)
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
