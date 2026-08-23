package app

import (
	"context"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/app/wiring"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/mutations"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/persistence"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaexec"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"

	"go.uber.org/zap"
)

// BuildDomainBundle builds the media-domain services by delegating to
// three per-domain helpers (media, assets, scripts).
//
// godlike/06 SSOT: each helper is the SOLE canonical owner of its
// domain's composition glue. The orchestrator owns ONLY the shared
// deps (mutations dispatcher) and the bundle assembly.
//
// Requires outbox.Dispatcher (injected via wiring.OutboxBundle, last arg).
func BuildDomainBundle(ctx context.Context, cfg *config.Config, dbs *wiring.Databases, log *zap.Logger, drive *wiring.DriveBundle, repos *wiring.RepoBundle, search *wiring.SearchBundle, process *wiring.ProcessBundle, ai *wiring.AIBundle, outbox *wiring.OutboxBundle, mediaConfig mediaexec.ExecutionConfig) (*wiring.DomainBundle, error) {
	// ── Shared deps ──────────────────────────────────────────
	var mutationsDisp mutations.AssetMutationDispatcher
	var canonicalCommitter persistence.AssetCommitter
	if outbox != nil && outbox.Dispatcher != nil {
		var err error
		mutationsDisp, err = newMutationsDispatcherAdapter(outbox.Dispatcher)
		if err != nil {
			return nil, fmt.Errorf("compose domains: %w", err)
		}
		canonicalCommitter = newCanonicalAssetCommitter(dbs.DualPool.Writer, outbox.EventsRepo, zap.NewNop())
	} else {
		return nil, fmt.Errorf("compose domains: outbox.Dispatcher is required — QDRANT-002 PR7 removed the legacy fallback; root.Outbox must be built first")
	}

	bundle := &wiring.DomainBundle{}

	// ── Media domain: YouTube clip pipeline ──────────────────
	voMetaWriter, clipWriter, err := buildDomainMediaServices(ctx, cfg, dbs, log, drive, repos, search, process, ai, outbox, canonicalCommitter, bundle, mediaConfig)
	if err != nil {
		return nil, fmt.Errorf("compose domains (media): %w", err)
	}
	bundle.CueWriter = clipWriter
	bundle.FolderPathWriter = clipWriter

	// ── Assets domain: voiceover, books, ingest, images, lessons ──
	if err := buildDomainAssetServices(buildDomainAssetServicesParams{
		ctx:           ctx,
		cfg:           cfg,
		dbs:           dbs,
		log:           log,
		drive:         drive,
		repos:         repos,
		search:        search,
		process:       process,
		ai:            ai,
		outbox:        outbox,
		mutationsDisp: mutationsDisp,
		voMetaWriter:  voMetaWriter,
		bundle:        bundle,
		mediaConfig:   mediaConfig,
	}); err != nil {
		return nil, fmt.Errorf("compose domains (assets): %w", err)
	}

	// ── Scripts domain: artifacts, segment-selection ──
	if err := buildDomainScriptServices(ctx, cfg, dbs, log, drive, repos, search, process, ai, bundle, bundle.ImageService /* *imgservice.Service */); err != nil {
		return nil, fmt.Errorf("compose domains (scripts): %w", err)
	}

	// ── Late-bindings (populated at composition.go) ──────────
	bundle.VoiceoverGenerateItemHandler = nil

	return bundle, nil
}
