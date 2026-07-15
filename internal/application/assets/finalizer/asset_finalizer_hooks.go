package finalizer

import (
	"context"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/finalization"
	"go.uber.org/zap"
)

// FirePostCommitHooks is the canonical post-commit fan-out hook. Callers must
// invoke it only after the transaction commit succeeds so workers cannot
// observe an asset before its canonical rows are durable.
func (s *AssetTxFinalizer) FirePostCommitHooks(
	ctx context.Context,
	artifact finalization.PublishedArtifact,
) {
	if s == nil || s.fanout == nil {
		return
	}
	if artifact.SourceTextHash == "" || artifact.SourceLanguage == "" {
		return
	}

	kinds := []asset.TextTrackKind{
		asset.TextTrackTranscript,
		asset.TextTrackDescription,
		asset.TextTrackSummary,
	}
	if err := s.fanout.EnqueueMaterializeOne(
		ctx,
		artifact.ArtifactID,
		artifact.SourceLanguage,
		artifact.SourceTextHash,
		kinds,
	); err != nil {
		if s.log != nil {
			s.log.Warn("AssetTxFinalizer.FirePostCommitHooks: fan-out enqueue failed (canonical asset row preserved; operator backfill will recover)",
				zap.String("artifact_id", artifact.ArtifactID),
				zap.String("source_language", artifact.SourceLanguage),
				zap.String("source_text_hash", artifact.SourceTextHash),
				zap.Error(err))
		}
		return
	}
	if s.log != nil {
		s.log.Info("AssetTxFinalizer.FirePostCommitHooks: asset.text.materialize enqueued",
			zap.String("artifact_id", artifact.ArtifactID),
			zap.String("source_language", artifact.SourceLanguage),
			zap.String("source_text_hash", artifact.SourceTextHash),
			zap.Int("kinds_count", len(kinds)),
		)
	}
}
