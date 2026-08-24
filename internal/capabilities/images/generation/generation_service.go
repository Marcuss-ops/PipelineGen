// Package images — generation_service.go: THIN orchestration shell for
// the AI image generation pipeline.
//
// PR-GODOBJ-3-IMAGES-GENERATION (July 2026): 548-LoC god service is
// decomposed per godlike/06 one-owner-per-fact into:
//
//   - generation_request.go   (pure normalization: pickImagePrompt
//   - GenerateImageRequest typed surface)
//   - provider_dispatch.go    (registry-only dispatch; KILL LIST a:
//     ErrNoGenerationProviderWired on nil registry;
//     NO legacy imageGen.Generate fallback)
//   - image_manifest.go       (typed ArtifactManifest builder:
//     buildImageManifest + RunManifest envelope)
//   - generation_usecase.go   (deterministic 5-step pipeline: style →
//     compose → dispatch → manifest; persistence-
//     agnostic)
//   - sync_generation.go      (sync adapter: calls usecase → drops
//     manifest → calls storage.IngestImage SOLE
//     sync ingest path per KILL LIST b)
//   - generation_job.go       (async adapter: payload → usecase →
//     manifest sidecar to runner finalizer SOLE
//     async persistence path per KILL LIST b;
//     register + handles dispatcher binding)
//
// This file owns:
//   - The GenerationService struct (registry + styles + log + storage).
//     godlike/07 KILL LIST a: the legacy imageGen field is REMOVED
//     (PR-IMAGES-SHIM-REMOVAL, 2026-07-04). Composition wires
//     NewGenerationProviderRegistry; a nil registry surfaces
//     ErrNoGenerationProviderWired at the usecase boundary (godlike/07
//     typed-error contract, typo in production wiring).
//   - The GenerateSmartImage method that delegates to sync_generation.go's
//     GenerateSync. KILL LIST (c) honored: account/project params
//     REMOVED from the canonical surface (PR-IMAGES-SHIM-REMOVAL,
//     2026-07-04). Tenant identity belongs in a separate auth/tenancy
//     port. The legacy GenerateSmartImageWithAccount method is GONE
//     (fake-availability retirement per godlike/07 no-fake-availability).
//   - The TriggerPrewarm method that satisfies the ImageSearchService
//     interface (Playwright tab-pool prewarm).
//
// godlike/07 typed-error contract (KILL LIST a): the constructor returns
// a *GenerationService even if registry is nil; the typed failure surface
// is at the usecase boundary (RunUsage → ErrNoGenerationProviderWired),
// NOT in the constructor (godlike/06 SSOT — composition root contracts
// stay fail-closed at the seam that actually decides dispatch).
//
// PR-IMAGES-SHIM-REMOVAL (2026-07-04): the canonical Service.HandleJob
// pattern is now `(s *Service) HandleJob(ctx, j, tools) -> s.JobHandler.HandleJob(ctx, j, tools)` —
// the JobHandler is held as a Service field, wired once at composition
// time via NewJobHandler(registry, styles, log). The pre-removal pattern
// of constructing a fresh JobHandler per call (g.HandleJob →
// NewJobHandler(g.registry, g.styles, g.log).HandleJob) is retired per
// godlike/07 minimal-blast-radius (composition root owns the wiring).
package generation

import (
	"context"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/generation"
	"github.com/Marcuss-ops/PipelineGen/internal/application/images/generated"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"go.uber.org/zap"
)

// GenerationService is the slim wiring shell. Each interface method
// delegates to a canonical file (sync → sync_generation.go's GenerateSync;
// prewarm → this file's TriggerPrewarm).
//
// PR-IMAGES-SHIM-REMOVAL: the dormant imageGen field is REMOVED. The
// canonical construction site is NewGenerationService(registry, styles,
// log, storage) — composition root wires the 4 args; the field set is
// exhausted by the constructor (no struct-literal callers remain after
// the migration).
type GenerationService struct {
	registry *generated.GenerationProviderRegistry
	styles   generation.StyleResolver
	log      *zap.Logger
	storage  *ImageStorageService
}

// NewGenerationService constructs the slim wiring shell. Composition
// root uses this constructor; the imageGen back-compat field is
// RETIRED (PR-IMAGES-SHIM-REMOVAL, 2026-07-04) so the constructor
// signature is final at 4 args.
func NewGenerationService(
	registry *generated.GenerationProviderRegistry,
	styles generation.StyleResolver,
	log *zap.Logger,
	storage *ImageStorageService,
) *GenerationService {
	return &GenerationService{
		registry: registry,
		styles:   styles,
		log:      log,
		storage:  storage,
	}
}

// GenerateSmartImage is the public sync entry point. Delegates to
// sync_generation.go's GenerateSync (which calls the usecase and
// routes through sync-only storage.IngestImage per KILL LIST b).
//
// PR-IMAGES-SHIM-REMOVAL: the canonical call signature has NO
// account/project argument. The legacy GenerateSmartImageWithAccount
// surface is GONE (fake-availability retirement per godlike/07).
// Tenant identity belongs in a separate auth/tenancy port.
func (g *GenerationService) GenerateSmartImage(
	ctx context.Context,
	subject string,
	topic string,
	style string,
	prompts []string,
	tags []string,
	width, height int,
	model string,
	skipDrive bool,
) (*asset.ImageAsset, error) {
	assetOut, err := GenerateSync(ctx, g, SyncCommand{
		Subject:   subject,
		Topic:     topic,
		Style:     style,
		Prompts:   prompts,
		Tags:      tags,
		Width:     width,
		Height:    height,
		Model:     model,
		SkipDrive: skipDrive,
	})
	if err == nil {
		return assetOut, nil
	}
	// Fail closed: Chrome/Slides generation must not pretend success
	// by synthesising a local PNG when the real provider is unavailable.
	// Callers that need a degraded path must opt into it explicitly
	// rather than receiving fake Chrome-backed output.
	return nil, err
}

// TriggerPrewarm satisfies the ImageSearchService interface so the
// script job handler can request a pre-warm of the Playwright tab
// pool. Preserved verbatim from the pre-split surface (no KILL
// LIST interaction).
func (g *GenerationService) TriggerPrewarm(ctx context.Context, jobID string, count int) {
	select {
	case <-ctx.Done():
		return
	default:
	}
	if g.registry != nil {
		g.registry.TriggerPrewarm(ctx, jobID, count)
	}
	if g.log != nil {
		g.log.Info("Google Slides: automation session tab pool prewarmed",
			zap.String("job_id", jobID),
			zap.Int("count", count),
		)
	}
}
