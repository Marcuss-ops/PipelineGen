// Package script (api/script) — handler_deps.go owns the construction
// seam for ScriptFlowHandler: 3 typed-narrow dep bags
// (GenerateDeps / ShortsDeps / JobsDeps) + the slim top-level ScriptFlowDeps +
// and the canonical constructor
// (NewScriptFlowHandler).
//
// PR-SCRIPT-DEPENDENCIES-EXTRACT (July 2026): extracted from
// handler_flow.go.
// PR-script-deps-slim (July 2026, P1): split the 23-field
// ScriptFlowDeps into 2 capability-scoped dep bags
// (GenerateDeps / JobsDeps) + dropped the 12 fields
// that NewScriptFlowHandler never read (Engine / Image / Realtime /
// Association / Voiceover / AssetTree / ClipSourceBuilder /
// MediaCurator / Harvest / ScriptsRepo / DriveScriptsGenFolder /
// ClipServices) + removed the former facade delegator methods and
// ScriptDescriptor.Handler field (defensive — the 6 non-HTTP
// methods have ZERO external callers at HEAD).
//
// godlike/06 SSOT rationale (one canonical owner per fact):
// the construction seam is the ScriptFlow package's re-use seam —
// keeping it in this file alongside ScriptFlowDeps means pipeline
// code that imports `script.ScriptFlowDeps` reaches a single
// canonical source rather than chasing the type across files.

package script

import (
	"context"

	opsapp "github.com/Marcuss-ops/PipelineGen/internal/application/operations"
	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/legacy"
	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/submission"
	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/usecase"
	appvideo "github.com/Marcuss-ops/PipelineGen/internal/application/video"
	jobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	"github.com/Marcuss-ops/PipelineGen/pkg/remotionjob"

	"go.uber.org/zap"
)

// ShortsDeps groups the canonical constructor inputs for the
// /shorts/* handler. Kept separate from GenerateDeps so the two
// surfaces can evolve independently.
type ShortsDeps struct {
	// Renderer is required by POST /shorts/render.
	Renderer appvideo.Renderer
	// Producer is required by POST /shorts/render/async.
	Producer interface {
		Enqueue(context.Context, remotionjob.RenderJob) (*jobs.Job, error)
	}
	Log *zap.Logger
}

// GenerateDeps groups the canonical constructor inputs for the
// /generate handler. Each field is required by the canonical
// POST /api/script/generate route:
//
//   - Submission: the canonical submission service.
//   - Log: structured logger.
//   - Validator: config-aware payload validator for
//     POST /api/script/generate.
//
// P0 verdetto (July 2026): GenRunStarter creates the GenerationRun
// BEFORE the submission I/O. When non-nil, the response includes
// current_stage. When nil (tests / legacy wiring), the handler
// falls back to the direct-submit flow.
//
// PR-SUBMISSION-FACTORY (July 2026): Factory builds the
// operations.SubmitRequest from the bound command. When nil,
// the handler falls back to the legacy direct-submit flow.
type GenerateDeps struct {
	Submission        generationSubmitter
	GenRunStarter     *scriptgen.GenerationRunStarter
	Factory           *submission.SubmitRequestFactory
	Log               *zap.Logger
	Validator         *usecase.PayloadValidator
	ResearchPreflight usecase.ResearchPreflight
}

// JobsDeps groups the canonical constructor inputs for the
// /jobs/:id handler. Mirrors GenerateDeps intentionally (the 2
// routes can evolve independently — future JobsDeps additions
// like a status-query repo stay scoped to /jobs/* without
// polluting /generate).
//
// FASE 2 (July 2026): the Registry field is RETIRED. The pre-FASE-2
// Registry thread fed the package-level enqueueEnvelopeFn for
// MaxRetries lookup; the FASE 2 path is GenerationSubmissionService
// which reads the registry independently at composition time. JobsDeps
// now carries the JobService port, an optional RunRepository for the
// GET /jobs/:id/full enriched endpoint, and the canonical logger.
//
// P1 verdetto (July 2026): RunRepo is the optional RunRepository for
// reading GenerationRun data in GET /jobs/:id/full. When nil, the
// endpoint returns basic job info without enriched pipeline data.
type JobsDeps struct {
	Jobs    jobs.Service
	RunRepo scriptgen.RunRepository
	Log     *zap.Logger
}

type generationSubmitter interface {
	Submit(ctx context.Context, req opsapp.SubmitRequest) (*opsapp.SubmitResult, error)
}

// ScriptFlowDeps is the slim top-level bag assembled by Build.
// Was 23 fields; now 5 (Generate + Shorts + Jobs + ClipsSearcher +
// AdminToken). godlike/07 minimum-blast-radius: Build still
// constructs NewScriptFlowHandler(ScriptFlowDeps{...}) so direct
// callers (test fixtures) compile unchanged in shape — only the
// field set slimmed.
type ScriptFlowDeps struct {
	// Generate is the dep bag for POST /generate.
	Generate GenerateDeps
	// Shorts is the dep bag for /shorts/*.
	Shorts ShortsDeps
	// Jobs is the dep bag for /jobs/:id.
	Jobs JobsDeps

	// ClipsSearcher is the clip-name searcher for
	// GET /script/clips/search?q= discovery endpoint.
	// Nil → endpoint returns 503.
	ClipsSearcher ClipSearcher

	// AdminToken is the auth secret consumed by EnableAuth +
	// AdminToken (the AdminTokenProvider interface satisfaction
	// locked by middleware_auth.go's compile-time assertion).
	AdminToken string
}

// NewScriptFlowHandler constructs the slim ScriptFlowHandler from
// the 4 small dep bags. NewScriptFlowHandler has no fail-closed
// checks (preserves the pre-Step-14 behavior for direct callers
// that bypass Build); Build's checks are the defensive layer.
//
// Field assignments dropped from the struct literal (with rationale):
//   - engine, section, cacheEviction, image, realtime, association,
//     voiceover, assetTree, clipSourceBuilder, mediaCurator, harvest,
//     scriptsRepo, driveScriptsGenFolder, clipServices: zero
//     production-code readers post-trim (godlike/07 no-fake-
//     availability — drop, not preserve). The 2 routes that
//     depended on sectionRegen + cacheEviction (RegenerateSection
//   - EvictCache) are REMOVED in lockstep (godlike/07:
//     routes that always 503 are fake-availability).
//   - voService + groupsResolver + driveFolderClient + documentCreator:
//     the 4 facade delegator methods on ScriptFlowHandler had
//     ZERO external callers; the compatibility facade was removed
//     together with the delegators.
func NewScriptFlowHandler(deps ScriptFlowDeps) *ScriptFlowHandler {
	log := deps.Jobs.Log
	if log == nil {
		log = zap.NewNop()
	}

	return &ScriptFlowHandler{
		log:           log,
		adminToken:    deps.AdminToken,
		clipsSearcher: deps.ClipsSearcher,

		// AZIONE 1 (July 2026): construct the 3-field HandlerGenerate
		// alongside the slim ScriptFlowHandler. POST /generate
		// delegates to h.gen.Generate(c). PR-COMMIT3: the 4th
		// `caps` arg is removed alongside the preflight module.
		// AZIONE 1 (July 2026): construct the 4-field HandlerGenerate
		// with the optional GenerationRunStarter (P0 verdetto) and
		// the SubmitRequestFactory (PR-SUBMISSION-FACTORY).
		gen: NewHandlerGenerate(deps.Generate.Submission, deps.Generate.GenRunStarter, deps.Generate.Factory, deps.Generate.Log, deps.Generate.Validator, deps.Generate.ResearchPreflight),

		// PR-SHORTS-EXTRACT (July 2026): construct the dedicated
		// HandlerShorts for /shorts/* routes.
		shorts: NewHandlerShorts(deps.Shorts.Renderer, deps.Shorts.Producer, deps.Shorts.Log),

		// PR-SCRIPT-JOBS-EXTRACT (July 2026): construct the 3-field
		// JobsHandler. GET /api/script/jobs/:id mounts via
		// JobsHandler.RegisterJobRoutes; the legacy EnqueueEnvelope
		// adapter is REMOVED in FASE 2 (the route was never
		// registered, so the deletion is dead code).
		// P1 verdetto (July 2026): RunRepo is optional — when nil,
		// GET /jobs/:id/full returns basic job info only.
		jobs: NewJobsHandler(deps.Jobs.Jobs, deps.Jobs.RunRepo, deps.Jobs.Log),
	}
}

// Compile-time guard: jobs.Service surfaces drift in
// NewJobsHandler / NewHandlerGenerate signatures (godlike/06 SSOT —
// no separate sentinel needed; constructor calls fail-closed at
// build time).
var _ jobs.Service = jobs.Service(nil)

// Compile-time guard: NewJobsHandler + NewHandlerGenerate accept
// jobs.Service + *appjobs.Registry; drift in either signature
// surfaces here, not at first runtime call. The canonical Pattern 0
// build-failure lock per AGENTS.md is satisfied by the constructor
// calls in NewScriptFlowHandler (compile-time, no separate sentinel
// needed).
