// Package texttracks — jobs_subtitles.go: subtitle-artifact delivery for the
// `asset.text.materialize` fast path.
//
// POSTGRES-MEDIA-CUTOVER follow-up (September 2026).
//
// The job handler has two paths:
//
//   - the automatic-ingest path (empty source hash) re-enters
//     BackfillService.ProcessAsset, which already ends with the
//     subtitle-artifact delivery step;
//   - the fast path (a committed source hash — what the direct YouTube
//     fan-out sends) runs the materializer only.
//
// The fast path is the one every freshly extracted YouTube clip takes when
// the acquisition chain found subtitles, so before this file a new clip got
// its configured language rows but none of the per-language subtitle files
// on Drive. The delivery is driven through the same canonical owner
// (BackfillService.MaterializeSubtitleArtifacts) so both paths cannot drift.
//
// Fail-soft by construction: the text tracks are already durable when this
// runs, so a missing asset, a disabled subtitle materializer or a failed
// upload is logged and reported, never turned into a job failure that would
// make a successful materialization look failed.
package texttracks

import (
	"context"

	"go.uber.org/zap"

	asset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
)

// deliverSubtitleArtifacts runs the canonical per-language subtitle delivery
// for a materialize job whose tracks are already durable. It returns nil when
// the delivery is not applicable (no backfill pipeline wired, or no transcript
// among the materialized kinds), so the caller can distinguish "not attempted"
// from "attempted and delivered nothing".
func (h *MaterializeJobHandler) deliverSubtitleArtifacts(
	ctx context.Context,
	cmd MaterializeJobPayload,
) *SubtitleDeliveryReport {
	if h == nil || h.backfill == nil {
		return nil
	}
	if !containsTextKind(cmd.TextKinds, detail.TextTrackTranscript) {
		return nil
	}

	assets, err := h.backfill.clips.List(ctx, asset.Filter{IDs: []string{cmd.AssetID}, Limit: 1})
	if err != nil {
		h.log.Warn("texttracks.materialize: load asset for subtitle delivery failed",
			zap.String("asset_id", cmd.AssetID), zap.Error(err))
		return nil
	}
	if len(assets) != 1 || assets[0] == nil {
		h.log.Warn("texttracks.materialize: asset not found for subtitle delivery",
			zap.String("asset_id", cmd.AssetID))
		return nil
	}

	rep, err := h.backfill.MaterializeSubtitleArtifacts(
		ctx, assets[0], cmd.SourceLanguage, cmd.TargetLanguages, detail.TextTrackTranscript,
	)
	if err != nil {
		h.log.Warn("texttracks.materialize: subtitle artifact delivery failed",
			zap.String("asset_id", cmd.AssetID), zap.Error(err))
	}
	h.log.Info("texttracks.materialize.subtitles_delivered",
		zap.String("asset_id", cmd.AssetID),
		zap.Int("delivered", rep.Delivered),
		zap.Int("failed", len(rep.Failed)),
		zap.Bool("skipped", rep.Skipped),
		zap.String("skip_reason", rep.SkipReason),
	)
	return &rep
}

// containsTextKind reports whether kinds includes kind. Kept local and
// dependency-free: it is a predicate over the job payload, not a policy
// decision.
func containsTextKind(kinds []string, kind detail.TextTrackKind) bool {
	for _, k := range kinds {
		if detail.TextTrackKind(k) == kind {
			return true
		}
	}
	return false
}
