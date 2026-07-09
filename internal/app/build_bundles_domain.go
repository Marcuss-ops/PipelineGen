package app

import (
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
// Requires outbox.Dispatcher (injected via OutboxBundle, last arg).
func BuildDomainBundle(ctx context.Context, cfg *config.Config, dbs *databases, log *zap.Logger, drive *DriveBundle, repos *RepoBundle, search *SearchBundle, process *ProcessBundle, ai *AIBundle, outbox *OutboxBundle) (*DomainBundle, error) {
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

	bundle := &DomainBundle{}

	// ── Media domain: YouTube clip pipeline ──────────────────
	voMetaWriter, clipWriter, err := buildDomainMediaServices(ctx, cfg, dbs, log, drive, repos, search, process, ai, outbox, mutationsDisp, bundle)
	if err != nil {
		return nil, fmt.Errorf("compose domains (media): %w", err)
	}

	// ── Assets domain: voiceover, books, ingest, images, lessons ──
	if err := buildDomainAssetServices(ctx, cfg, dbs, log, drive, repos, search, process, ai, outbox, mutationsDisp, voMetaWriter, bundle); err != nil {
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
