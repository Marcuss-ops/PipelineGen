package finalizer

import (
	"context"
	detail "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/finalization"
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
	sourceLanguage := artifact.SourceLanguage
	if sourceLanguage == "" {
		sourceLanguage = s.fanout.DefaultSourceLanguage()
	}
	if sourceLanguage == "" {
		return
	}

	kinds := []detail.TextTrackKind{
		detail.TextTrackTranscript,
		detail.TextTrackDescription,
		detail.TextTrackSummary,
	}
	var err error
	if artifact.SourceTextHash == "" {
		err = s.fanout.EnqueueAcquireOne(ctx, artifact.ArtifactID, sourceLanguage, []detail.TextTrackKind{detail.TextTrackTranscript})
	} else {
		err = s.fanout.EnqueueMaterializeOne(ctx, artifact.ArtifactID, sourceLanguage, artifact.SourceTextHash, kinds)
	}
	if err != nil {
		if s.log != nil {
			s.log.Warn("AssetTxFinalizer.FirePostCommitHooks: fan-out enqueue failed (canonical asset row preserved; operator backfill will recover)",
				zap.String("artifact_id", artifact.ArtifactID),
				zap.String("source_language", sourceLanguage),
				zap.String("source_text_hash", artifact.SourceTextHash),
				zap.Error(err))
		}
		return
	}
	if s.log != nil {
		s.log.Info("AssetTxFinalizer.FirePostCommitHooks: automatic transcript/materialization job enqueued",
			zap.String("artifact_id", artifact.ArtifactID),
			zap.String("source_language", sourceLanguage),
			zap.String("source_text_hash", artifact.SourceTextHash),
			zap.Int("kinds_count", len(kinds)),
		)
	}
}
