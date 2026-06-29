// Package app — Media-processor wiring helpers (FASE 2.B PR2-followup, June 2026).
//
// Originally the media-processor init block (mutations dispatcher adapter +
// initMediaProcessor call) + the VLMClient init were inline in
// BuildProcessBundle inside build_process_qdrant.go. The PR2-followup
// extraction splits these into helpers in THIS file so that:
//
//   - this file          owns ONLY wireMediaProcessor (canonical
//     mutations.AssetMutationDispatcher adapter + initMediaProcessor
//     wiring) + newVLMClient (cfg.VLM → *vlm.Client wrapper).
//   - build_qdrant_runtime.go owns ONLY initQdrantProcessSubsystems
//     (Qdrant runtime → ProcessBundle mapping) + the 2 QDRANT-003
//     compile-time port assertions.
//   - build_process_qdrant.go is reduced to a thin BuildProcessBundle
//     orchestrator that calls the 3 helpers above and assembles the
//     canonical *ProcessBundle return value.
//
// Naming convention: `wire<X>` for adapters + helpers; `<verbNoun>` for
// narrow constructors; matches `wireArtlistLifecycle` in module_sources.go.
// The two helpers are referenced by BuildProcessBundle via package-level
// visibility (cross-file within `package app`); no alias wrappers.
//
// PR2-followup is MOVE-only: zero logic changes in any of the 3 files,
// zero call-site changes across the codebase.
package app

import (
	"fmt"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/vlm"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

// wireMediaProcessor builds the canonical asset.Processor by chaining
// newMutationsDispatcherAdapter (the QDRANT-asset-mutation isolation
// adapter from registry_adapters.go) + initMediaProcessor (the
// FFmpeg-backed media-processor wiring from registry_helpers.go).
//
// Fail-closed semantics (PR 8, June 2026, codex/qdrant-app-writers-fail-closed):
//   - nil outbox.Dispatcher → returns (nil, nil). The log.Warn mirrors the
//     pre-extraction behavior so worker / reprocess / ingest paths
//     surface the missing dep rather than silently defaulting to the
//     legacy path.
//   - non-nil dispatcher + adapter failure → returns (nil, err) so
//     BuildProcessBundle aborts composition (fail-closed wire-time check).
//   - success → returns the concrete asset.Processor and logs the
//     QDRANT-003 PR 8 success marker (`ops: PR 8: MediaProcessor ...`).
//
// The function preserves all 4 properties verbatim from the pre-extraction
// inline block in BuildProcessBundle (no logic delta).
func wireMediaProcessor(
	outbox *OutboxBundle,
	repos *RepoBundle,
	dbs *databases,
	cfg *config.Config,
	driveUploader *drive.Uploader,
	log *zap.Logger,
) (asset.Processor, error) {
	if outbox == nil || outbox.Dispatcher == nil {
		log.Warn("BuildProcessBundle: outbox.Dispatcher is nil — MediaProcessor left nil (QDRANT-002 PR8 fail-closed; worker + reprocess + ingest paths will surface the missing dep)")
		return nil, nil
	}
	mutationsDisp, err := newMutationsDispatcherAdapter(outbox.Dispatcher)
	if err != nil {
		return nil, fmt.Errorf("wireMediaProcessor: mutations dispatcher adapter: %w", err)
	}
	mp := initMediaProcessor(
		cfg,
		dbs.main,
		repos.Assets.Repository(),
		repos.Assets,
		repos.Assets.LocationRepository(),
		repos.Assets.ProcessingRepository(),
		mutationsDisp,
		log,
		driveUploader,
	)
	log.Info("PR 8: MediaProcessor constructed inline with canonical mutations.AssetMutationDispatcher (clipsRegistry UPSERT routed through outbox+tx)")
	return mp, nil
}

// newVLMClient is the narrow constructor for the VLM (Vision-Language
// Model) client used by BuildProcessBundle's VLMClient slot. The cfg.VLM
// block carries the 5 fields the vlm.Config struct expects (Enabled,
// Endpoint, Model, TimeoutMs, Weight) and nothing else. Extracted so
// BuildProcessBundle reads as a thin orchestrator rather than a
// 7-line constructor.
//
// No fail-closed path: cfg.VLM.Enabled=false returns a *vlm.Client
// that short-circuits on every call (see vlm.NewClient contract).
func newVLMClient(cfg *config.Config) *vlm.Client {
	return vlm.NewClient(vlm.Config{
		Enabled:   cfg.VLM.Enabled,
		Endpoint:  cfg.VLM.URL,
		Model:     cfg.VLM.Model,
		TimeoutMs: cfg.VLM.TimeoutMs,
		Weight:    cfg.VLM.Weight,
	})
}
