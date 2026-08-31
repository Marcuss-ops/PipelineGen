package wiring

import (
	"context"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/mutations"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/persistence"
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
// Requires outbox.Dispatcher (injected via OutboxBundle, last arg).
func BuildDomainBundle(ctx context.Context, cfg *config.Config, dbs *Databases, log *zap.Logger, drive *DriveBundle, repos *RepoBundle, search *SearchBundle, process *ProcessBundle, ai *AIBundle, outbox *OutboxBundle, mediaConfig mediaexec.ExecutionConfig) (*DomainBundle, error) {
	// ── Shared deps ──────────────────────────────────────────
	var mutationsDisp mutations.AssetMutationDispatcher
	var canonicalCommitter persistence.AssetCommitter
	if outbox != nil && outbox.Dispatcher != nil && outbox.CanonicalWriter != nil {
		var err error
		mutationsDisp, err = newMutationsDispatcherAdapter(outbox.Dispatcher)
		if err != nil {
			return nil, fmt.Errorf("compose domains: %w", err)
		}
		canonicalCommitter = outbox.CanonicalWriter
	} else {
		return nil, fmt.Errorf("compose domains: outbox.Dispatcher is required — QDRANT-002 PR7 removed the legacy fallback; root.Outbox must be built first")
	}

	bundle := &DomainBundle{}

	// ── Media domain: YouTube clip pipeline ──────────────────
	voMetaWriter, clipWriter, err := buildDomainMediaServices(ctx, cfg, dbs, log, drive, repos, search, process, ai, outbox, canonicalCommitter, bundle, mediaConfig)
	if err != nil {
		return nil, fmt.Errorf("compose domains (media): %w", err)
	}
	bundle.CueWriter = clipWriter
	bundle.FolderPathWriter = clipWriter

	// ── Assets domain: voiceover, books, ingest, images, lessons ──
	if err := buildDomainAssetServices(buildDomainAssetServicesParams{
		ctx:                ctx,
		cfg:                cfg,
		dbs:                dbs,
		log:                log,
		drive:              drive,
		repos:              repos,
		search:             search,
		process:            process,
		ai:                 ai,
		outbox:             outbox,
		canonicalCommitter: canonicalCommitter,
		mutationsDisp:      mutationsDisp,
		voMetaWriter:       voMetaWriter,
		bundle:             bundle,
		mediaConfig:        mediaConfig,
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
