// Package app — media-processor + VLM-client construction.
//
// Extracted from build_bundles_process.go (July 2026 sub-section
// split) per the documented layout in build_process_qdrant.go:
// this file owns ONLY wireMediaProcessor (canonical
// mutations.AssetMutationDispatcher adapter + InitMediaProcessor
// FFmpeg-backed wiring) + newVLMClient (cfg.VLM → *vlm.Client).
package wiring

import (
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaexec"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/ai/vlm"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/delivery"
)

// ── Media-processor wiring (moved from build_media_processor.go, Phase 5 consolidation, June 2026) ──

// F2.8 (June 2026): the trailing arg swaps from `*drive.Uploader`
// to `delivery.Publisher`. The Publisher is the canonical canal for
// every Drive write from the processor; the legacy direct-uploader
// bypass is closed. Compile-time assertion
// `var _ delivery.Publisher = (*drive.Uploader)(nil)` lives in
// internal/platform/drive/publisher.go (already pinned there)
// so this wiring is type-safe.
func wireMediaProcessor(
	outbox *OutboxBundle,
	repos *RepoBundle,
	dbs *Databases,
	cfg *config.Config,
	publisher delivery.Publisher,
	log *zap.Logger,
	mediaConfig mediaexec.ExecutionConfig,
) (detail.Processor, error) {
	if outbox == nil || outbox.Dispatcher == nil {
		log.Warn("BuildProcessBundle: outbox.Dispatcher is nil — MediaProcessor left nil (QDRANT-002 PR8 fail-closed)")
		return nil, nil
	}
	committer := newCanonicalAssetCommitter(dbs.Main.DB, outbox.EventsRepo, log)
	mp := InitMediaProcessor(
		cfg,
		dbs.Main,
		repos.Assets.Repository(),
		repos.Assets,
		repos.Assets.LocationRepository(),
		repos.Assets.ProcessingRepository(),
		committer,
		log,
		publisher,
		mediaConfig,
	)
	log.Info("PR 8: MediaProcessor constructed inline with canonical mutations.AssetMutationDispatcher (F2.8: publisher wired)")
	return mp, nil
}

// newVLMClient maps cfg.VLM to the canonical *vlm.Client concrete.
// No fail-closed gate here: cfg.VLM.Enabled=false produces a
// disabled client that returns a typed "VLM disabled" error from
// every method (godlike/07 no-fake-availability — the disabled state
// is an explicit capability, not a silent no-op success).
func newVLMClient(cfg *config.Config) *vlm.Client {
	return vlm.NewClient(vlm.Config{
		Enabled:      cfg.VLM.Enabled,
		Endpoint:     cfg.VLM.URL,
		Model:        cfg.VLM.Model,
		ModelVersion: cfg.VLM.ModelVersion,
		TimeoutMs:    cfg.VLM.TimeoutMs,
		Weight:       cfg.VLM.Weight,
	})
}
