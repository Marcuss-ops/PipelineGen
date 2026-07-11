// Package script (api/script) — handler_deps.go owns the construction
// seam for ScriptFlowHandler: 2 typed-narrow dep bags
// (GenerateDeps / JobsDeps) + the slim top-level ScriptFlowDeps +
// the optional auto-harvest port interface
// (AutoHarvestService) + the canonical constructor
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
// ClipServices) + removed the 4 facade delegator methods on
// ScriptFlowHandler (GetVoiceoverService + GetGroupsResolver +
// ResolveDriveFolderID + MaybeCreateGoogleDoc) + removed the
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

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	mw "github.com/Marcuss-ops/PipelineGen/internal/application/middleware"
	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/usecase"
	jobservice "github.com/Marcuss-ops/PipelineGen/internal/domain/job"

	"go.uber.org/zap"
)

// AutoHarvestService is the auto-harvest trigger surface. Optional; nil
// disables the corresponding endpoint with a 503-equivalent sentinel.
// Moved from handler_flow.go with PR-SCRIPT-DEPENDENCIES-EXTRACT — the
// surface contract (EnqueueHarvest term+limit+preset → enqueued job
// id) is preserved byte-stable.
type AutoHarvestService interface {
	EnqueueHarvest(ctx context.Context, term string, limit int, preset string) (string, error)
}

// GenerateDeps groups the canonical constructor inputs for the
// /generate handler. Each field is required by the canonical
// POST /api/script/generate route:
//
//   - Jobs: the async job broker (script.generate child-job fan-out).
//   - Log: structured logger.
//   - Registry: the canonical job-type registry used by
//     EnqueueGenerationJob to source MaxRetries via
//     registry.DefaultMaxRetries(jType).
//   - Caps: SCRIPTCONTRACT-2026-07-08 PR-2 PreflightCaps. The
//     flat composition-time postprocessor-availability surface.
//     Built by the composition root from root.Domains.VoiceoverService
//   - root.Domains.ImageService + root.Drive.DocClient. Zero-value
//     is the conservative default (all false, fail-closed for any
//     user-requested processor).
//   - Validator: config-aware payload validator for
//     POST /api/script/generate.
type GenerateDeps struct {
	Jobs      jobservice.Service
	Log       *zap.Logger
	Registry  *appjobs.Registry
	Caps      PreflightCaps
	Validator *usecase.PayloadValidator
	Store     mw.IdempotencyStore
}

// JobsDeps groups the canonical constructor inputs for the
// /jobs/:id handler. Mirrors GenerateDeps intentionally (the 2
// routes can evolve independently — future JobsDeps additions
// like a status-query repo stay scoped to /jobs/* without
// polluting /generate).
//
// SCRIPTCONTRACT-2026-07-08 PR-2: JobsDeps intentionally does NOT
// have a Caps field. The preflight surface lives ONLY on
// GenerateDeps.Caps (the canonical SOLE owner). JobsHandler receives
// the preflight caps as a per-call parameter from
// ScriptFlowHandler.enqueueEnvelope (the canonical thread surface).
// godlike/06 SSOT: there is exactly ONE PreflightCaps instance per
// ScriptFlowHandler; storing it on JobsDeps would duplicate the
// canonical value and invite drift.
type JobsDeps struct {
	Jobs     jobservice.Service
	Log      *zap.Logger
	Registry *appjobs.Registry
}

// ScriptFlowDeps is the slim top-level bag assembled by Build.
// Was 23 fields; now 4 (Generate + Jobs + ClipsSearcher +
// AdminToken). godlike/07 minimum-blast-radius: Build still
// constructs NewScriptFlowHandler(ScriptFlowDeps{...}) so direct
// callers (test fixtures) compile unchanged in shape — only the
// field set slimmed.
type ScriptFlowDeps struct {
	// Generate is the dep bag for POST /generate.
	Generate GenerateDeps
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
// the 3 small dep bags. NewScriptFlowHandler has no fail-closed
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
//     ZERO external callers per audit-pin (flow.go:88); the
//     FacadeHandler type is preserved as the canonical owner
//     of the 4 real impls (godlike/06 SSOT) — only the
//     ScriptFlowHandler delegators are removed.
func NewScriptFlowHandler(deps ScriptFlowDeps) *ScriptFlowHandler {
	log := deps.Jobs.Log
	if log == nil {
		log = zap.NewNop()
	}

	// SCRIPTCONTRACT-2026-07-08 PR-2: there is exactly ONE
	// PreflightCaps instance per ScriptFlowHandler. The composition
	// root (internal/app/wire_script.go) builds it at startup from
	// root.Domains.VoiceoverService / root.Domains.ImageService /
	// root.Drive.DocClient. The canonical source is
	// `deps.Generate.Caps`; ScriptFlowHandler.caps carries it to
	// the request seam (enqueueEnvelopeFn) and to the legacy-
	// adapter thin-delegator path (h.jobs.EnqueueEnvelope).
	caps := deps.Generate.Caps

	return &ScriptFlowHandler{
		log:           log,
		adminToken:    deps.AdminToken,
		clipsSearcher: deps.ClipsSearcher,
		caps:          caps,

		// AZIONE 1 (July 2026): construct the 5-field HandlerGenerate
		// alongside the slim ScriptFlowHandler. POST /generate
		// delegates to h.gen.Generate(c).
		gen: NewHandlerGenerate(deps.Generate.Jobs, deps.Generate.Log, deps.Generate.Registry, caps, deps.Generate.Validator, deps.Generate.Store),

		// PR-SCRIPT-JOBS-EXTRACT (July 2026): construct the 3-field
		// JobsHandler. POST /api/script/jobs/:id mounts via
		// JobsHandler.RegisterJobRoutes; legacy adapters' h.enqueueEnvelope
		// thin-delegates to JobsHandler.EnqueueEnvelope (which
		// takes caps as a per-call parameter from h.caps).
		jobs: NewJobsHandler(deps.Jobs.Jobs, deps.Jobs.Log, deps.Jobs.Registry),
	}
}

// Compile-time guard: jobservice.Service + *appjobs.Registry
// surface drift in NewJobsHandler / NewHandlerGenerate signatures
// (godlike/06 SSOT — no separate sentinel needed; constructor
// calls fail-closed at build time).
var (
	_ jobservice.Service = jobservice.Service(nil)
	_ *appjobs.Registry  = (*appjobs.Registry)(nil)
)

// Compile-time guard: NewJobsHandler + NewHandlerGenerate accept
// jobservice.Service + *appjobs.Registry; drift in either signature
// surfaces here, not at first runtime call. The canonical Pattern 0
// build-failure lock per AGENTS.md is satisfied by the constructor
// calls in NewScriptFlowHandler (compile-time, no separate sentinel
// needed).
