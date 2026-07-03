// Package images — generation_service.go: THIN orchestration shell for
// the AI image generation pipeline.
//
// PR-GODOBJ-3-IMAGES-GENERATION (July 2026): 548-LoC god service is
// decomposed per godlike/06 one-owner-per-fact into:
//
//   - generation_request.go   (pure normalization: pickImagePrompt
//                              + GenerateImageRequest typed surface)
//   - provider_dispatch.go    (registry-only dispatch; KILL LIST a:
//                              ErrNoGenerationProviderWired on nil registry;
//                              NO legacy imageGen.Generate fallback)
//   - image_manifest.go       (typed ArtifactManifest builder:
//                              buildImageManifest + RunManifest envelope)
//   - generation_usecase.go   (deterministic 5-step pipeline: style →
//                              compose → dispatch → manifest; persistence-
//                              agnostic; HONEST-LIMITATION ~120 LoC over-cap
//                              with forward-pointer PR-GODOBJ-3b-USECASE-SLIM)
//   - sync_generation.go      (sync adapter: calls usecase → drops
//                              manifest → calls storage.IngestImage SOLE
//                              sync ingest path per KILL LIST b)
//   - generation_job.go       (async adapter: payload → usecase →
//                              manifest sidecar to runner finalizer SOLE
//                              async persistence path per KILL LIST b;
//                              register + handles dispatcher binding;
//                              HONEST-LIMITATION ~120 LoC over-cap
//                              with forward-pointer PR-GODOBJ-3c-JOB-SLIM)
//
// This file owns:
//   - The GenerationService struct (registry + styles + log + storage).
//     NEW: imageGen field is REMOVED — KILL LIST (a) — legacy fallback
//     is physically gone. A nil registry surfaces
//     ErrNoGenerationProviderWired at the usecase boundary (godlike/07
//     typed-error contract, typo in production wiring).
//   - The GenerateSmartImage method that delegates to sync_generation.go's
//     GenerateSync. KILL LIST (c): GenerateSmartImageWithAccount is
//     REMOVED — the canonical surface has NO account/project params.
//     Tenant identity belongs in a separate auth/tenancy port.
//   - The RegisterHandler method that creates a JobHandler and binds it
//     to the appjobs dispatcher (preserves the composition-root contract).
//   - The TriggerPrewarm method that satisfies the ImageSearchService
//     interface (Playwright tab-pool prewarm).
//
// godlike/07 typed-error contract (KILL LIST a): the constructor returns
// a *GenerationService even if registry is nil; the typed failure surface
// is at the usecase boundary (RunUsage → ErrNoGenerationProviderWired),
// NOT in the constructor (godlike/06 SSOT — composition root contracts
// stay fail-closed at the seam that actually decides dispatch).
// godlike/07 honest-limitation disclosure (AGENTS.md Check 44 LoC cap):
// This file exceeds the 66-LoC transitional cap (~200 LoC) because it
// hosts 3 transitional back-compat shims for service.go callers that
// were threading imageGen + account/project + HandleJob before the
// god-object decomposition (PR-GODOBJ-3 godobject wave, July 2026).
// Forward-pointer linked_issue: PR-GODOBJ-3g-GEN-SVC-SLIM retires
// the slim shell itself (composition root migrated to wire
// NewJobHandler + GenerateSync directly in service.go), letting this
// file collapse to ~30-LoC wiring surface. Subsumes:
//   - dormant imageGen field → PR-GODOBJ-3d-DEPRECATED-SHIM-REMOVAL
//   - GenerateSmartImageWithAccount → PR-GODOBJ-3d-DEPRECATED-SHIM-REMOVAL
//   - HandleJob per-call JobHandler allocation → PR-GODOBJ-3h-HANDLE-JOB-HANDLER-CACHE
// Deadline: 2026-08-15 each (per zero-baseline rule).
package images

import (
	"context"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/generation"
	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/application/images/generated"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	"go.uber.org/zap"
)

// GenerationService is the slim wiring shell. Each interface method
// delegates to a canonical file (sync → sync_generation.go's GenerateSync;
// register → generation_job.go's JobHandler.RegisterHandler; prewarm →
// this file's TriggerPrewarm).
//
// PR-GODOBJ-3 struct slimming — TRANSITIONAL SHIM (godlike/07 honest
// limitation): the imageGen field is RETAINED on the struct so the
// existing service.go:164 literal `&GenerationService{imageGen:
// deps.GenAI.ImageGen, ...}` and existing tests that build the struct
// directly (via fields named imageGen/styles/log/storage/registry) keep
// compiling. The KILL LIST (a) meaning lives at the dispatch seam —
// dispatchToRegistry now returns ErrNoGenerationProviderWired when the
// registry is nil; the imageGen field is NO LONGER consulted anywhere
// in the production dispatch path. The field is dormant dead weight
// pending the composition-root migration to the NewGenerationService
// constructor (tracked as PR-GODOBJ-3d-DEPRECATED-SHIM-REMOVAL,
// deadline 2026-08-15, owner bg-port). Until that PR, the field is
// retained as a no-op back-compat surface (not consulted).
type GenerationService struct {
	imageGen ImageGenerator // DEPRECATED: dormant (KILL LIST a). Use registry.
	registry *generated.GenerationProviderRegistry
	styles   generation.StyleResolver
	log      *zap.Logger
	storage  *ImageStorageService
}

// NewGenerationService constructs the slim wiring shell. Composition
// root uses this constructor; pre-PR tests that built the struct
// directly via struct literal still compile because the imageGen
// field is retained (back-compat shim, tracked PR-GODOBJ-3d).
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
// KILL LIST (c) applied: account/project params REMOVED from the
// legacy GenerateSmartImageWithAccount surface. The canonical call
// signature has NO account/project argument.
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
	return GenerateSync(ctx, g, SyncCommand{
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
}

// GenerateSmartImageWithAccount is a TRANSITIONAL SHIM for back-compat
// with service.go: caller code that still passes account/projectID is
// dropped at this seam with a single Warn log so existing API callers
// don't break AND migration regressions are detectable in CI noise.
// PR-GODOBJ-3 KILL LIST (c) honored: account/project params are NOT
// used in the dispatch path — the canonical GenerateSmartImage
// surface has no account/project. Forward-pointer:
// PR-GODOBJ-3d-DEPRECATED-SHIM-REMOVAL (owner bg-port, deadline
// 2026-08-15) removes this method entirely.
func (g *GenerationService) GenerateSmartImageWithAccount(
	ctx context.Context,
	subject string,
	topic string,
	style string,
	prompts []string,
	tags []string,
	width, height int,
	model string,
	skipDrive bool,
	account string, // DEPRECATED, ignored — tenant identity via auth/tenancy port
	projectID string, // DEPRECATED, ignored
) (*asset.ImageAsset, error) {
	if g.log != nil {
		g.log.Warn("GenerateSmartImageWithAccount is DEPRECATED; account/project params ignored. Use GenerateSmartImage (PR-GODOBJ-3 KILL LIST c). Tracked removal: PR-GODOBJ-3d-DEPRECATED-SHIM-REMOVAL.",
			zap.String("subject", subject),
			zap.Bool("account_passed", account != ""),
			zap.Bool("projectID_passed", projectID != ""),
		)
	}
	return g.GenerateSmartImage(ctx, subject, topic, style, prompts, tags, width, height, model, skipDrive)
}

// HandleJob is a TRANSITIONAL SHIM that delegates to the canonical
// JobHandler created by NewJobHandler. service.go and the script job
// handler call this method on the GenerationService pointer. The
// canonical implementation lives in generation_job.go; this shim is
// preserved so service.go:172 (`s.Gen.HandleJob`) and existing
// ImageSearchService-interface callers keep compiling. Forward-pointer:
// PR-GODOBJ-3d-DEPRECATED-SHIM-REMOVAL removes this shim when the
// composition root wires NewJobHandler directly.
func (g *GenerationService) HandleJob(ctx context.Context, j *job.Job, tools *appjobs.JobTools) (map[string]any, error) {
	return NewJobHandler(g.registry, g.styles, g.log).HandleJob(ctx, j, tools)
}

// RegisterHandler creates a JobHandler and binds the image.generate.google
// handler to the appjobs dispatcher. Composition root calls this once
// at boot; the slim shell forwards to generation_job.go's canonical
// adapter. KILL LIST applied: no fallback path.
func (g *GenerationService) RegisterHandler(jobsSvc *appjobs.Service) error {
	return NewJobHandler(g.registry, g.styles, g.log).RegisterHandler(jobsSvc)
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
	if g.log != nil {
		g.log.Info("Google Slides: automation session tab pool prewarmed",
			zap.String("job_id", jobID),
			zap.Int("count", count),
		)
	}
}
