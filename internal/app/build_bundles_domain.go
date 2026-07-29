package app

import (
	"github.com/Marcuss-ops/PipelineGen/internal/app/wiring"
	"context"
	"fmt"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/mutations"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

// BuildDomainBundle builds the media-domain services by delegating to
// three per-domain helpers (media, assets, scripts).
//
// godlike/06 SSOT: each helper is the SOLE canonical owner of its
// domain's composition glue. The orchestrator owns ONLY the shared
// deps (mutations dispatcher) and the bundle assembly.
//
// Requires outbox.Dispatcher (injected via wiring.OutboxBundle, last arg).
func BuildDomainBundle(ctx context.Context, cfg *config.Config, dbs *databases, log *zap.Logger, drive *wiring.DriveBundle, repos *wiring.RepoBundle, search *wiring.SearchBundle, process *wiring.ProcessBundle, ai *wiring.AIBundle, outbox *wiring.OutboxBundle) (*wiring.DomainBundle, error) {
	// ── Shared deps ──────────────────────────────────────────
	var mutationsDisp mutations.AssetMutationDispatcher
	if outbox != nil && outbox.Dispatcher != nil {
		var err error
		mutationsDisp, err = newMutationsDispatcherAdapter(outbox.Dispatcher)
		if err != nil {
			return nil, fmt.Errorf("compose domains: %w", err)
		}
	} else {
		return nil, fmt.Errorf("compose domains: outbox.Dispatcher is required — QDRANT-002 PR7 removed the legacy fallback; root.Outbox must be built first")
	}

	bundle := &wiring.DomainBundle{}

	// ── Media domain: YouTube clip pipeline ──────────────────
	voMetaWriter, clipWriter, err := buildDomainMediaServices(ctx, cfg, dbs, log, drive, repos, search, process, ai, outbox, mutationsDisp, bundle)
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
	}); err != nil {
		return nil, fmt.Errorf("compose domains (assets): %w", err)
	}

	// ── Scripts domain: artifacts, extract-important-clips ──
	if err := buildDomainScriptServices(ctx, cfg, dbs, log, drive, repos, search, process, ai, bundle, bundle.ImageService /* *imgservice.Service */, clipWriter); err != nil {
		return nil, fmt.Errorf("compose domains (scripts): %w", err)
	}

	// ── Late-bindings (populated at composition.go) ──────────
	bundle.VoiceoverGenerateItemHandler = nil

	return bundle, nil
}
