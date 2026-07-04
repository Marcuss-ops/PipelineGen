// Package script (api/script) — handler_deps.go owns the construction
// seam for ScriptFlowHandler: the typed-narrow dep bag (ScriptFlowDeps)
// + the optional auto-harvest port interface (AutoHarvestService) +
// the canonical constructor (NewScriptFlowHandler).
//
// Extracted from handler_flow.go via PR-SCRIPT-DEPENDENCIES-EXTRACT
// (architecture/current.yaml#SCRIPT-FLOW-SPLIT.linked_issues[PR-SCRIPT-DEPENDENCIES-EXTRACT]).
// ScriptFlowDeps signature is byte-stable across this move — only the
// file location changes, so module.go::Build + composition root +
// any direct callers (test fixtures) compile unchanged.
//
// godlike/06 SSOT rationale (one canonical owner per fact):
// the construction seam is the ScriptFlow package's re-use seam —
// keeping it in this file alongside ScriptFlowDeps means pipeline
// code that imports `script.ScriptFlowDeps` reaches a single
// canonical source rather than chasing the type across files.

package script

import (
	"context"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/assettree"
	"github.com/Marcuss-ops/PipelineGen/internal/application/images"
	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/adapters"
	scriptdto "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/dto"
	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/usecase"
	"github.com/Marcuss-ops/PipelineGen/internal/application/voiceover"
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

// ScriptFlowDeps groups all constructor inputs.
//
// Signature is byte-stable: the field set, names, and types are
// preserved verbatim from pre-PR-SCRIPT-DEPENDENCIES-EXTRACT.
// NewScriptFlowHandler assigns the 8 essential fields onto
// ScriptFlowHandler (gen / jobs / facade / jobsSvc / log / registry /
// adminToken / clipsSearcher) and IGNORES the remaining 12 deps
// (Engine / Image / Realtime / Association / Voiceover-only-via-facade /
// AssetTree / ClipSourceBuilder / MediaCurator / Harvest / ScriptsRepo /
// DriveFolderClient-via-facade / DocumentCreator-via-facade / ClipServices /
// DriveScriptsGenFolder). The ignored deps survive on ScriptFlowDeps
// because Build() (internal/api/script/module.go) still wires them from
// the higher-level Dependencies — module-wide contract preserved.
type ScriptFlowDeps struct {
	Engine        *usecase.Engine
	Section       *usecase.SectionRegenerator
	CacheEviction *usecase.CacheEvictionUseCase

	Image *images.Service
	// Wave 16 (June 2026): typed ports — replace the `interface{}`
	// carrier for the script-side realtime + association consumers
	// (packages removed in commit d61068b3; fields stay typed-nil).
	// Compile-time enforcement replaces the prior runtime safety net.
	Realtime    usecase.RealtimeSearchService
	Association usecase.AssocSearchService
	Voiceover   *voiceover.Service
	AssetTree   *assettree.Service

	ClipSourceBuilder *usecase.ClipSourceBuilder
	MediaCurator      *scriptdto.MediaCurator
	Harvest           AutoHarvestService

	ScriptsRepo adapters.ScriptRepository
	// Commit H Phase 2 (June 2026): Memory field dropped (gemmamemory
	// gemmamemory gate service gone).
	Jobs jobservice.Service
	// Issue 4 (June 2026, P1): optional canonical job-type registry
	// used by EnqueueGenerationJob to source MaxRetries from
	// registry.DefaultMaxRetries(jType). Optional — nil preserves
	// the legacy hard-coded 3-retry fallback path through the
	// JobsService. Composition root will pass appjobs.Compose().
	Registry *appjobs.Registry

	// PR-FIX (June 2026): optional clip-name searcher for
	// GET /script/clips/search?q= discovery endpoint.
	// Nil → endpoint returns 503.
	ClipsSearcher ClipSearcher

	AdminToken            string
	DriveFolderClient     DriveFolderClient
	DocumentCreator       DocumentCreator
	DriveScriptsGenFolder string
	ClipServices          usecase.ClipServices // pre-built in wire_script.go
	Log                   *zap.Logger
}

// NewScriptFlowHandler constructs the slim ScriptFlowHandler (8 fields
// post-PR-SCRIPT-DEPENDENCIES-EXTRACT) from the byte-stable ScriptFlowDeps.
//
// Field assignments dropped from the struct literal (with rationale):
//   - engine: zero production-code readers post-trim (godlike/07
//     no-fake-availability — drop, not preserve).
//   - imgService: zero readers.
//   - realtimeSvc/associationSvc: typed-nil per Wave 16 comment;
//     godlike/07 no-fake — drop rather than keep a fake-availability
//     field that the runtime never reads.
//   - voService: thin delegator GetVoiceoverService hops to h.facade.X.
//   - groupsResolver/assetTreeSvc: groupsResolver lives on facade (canonical);
//     assetTreeSvc was used only inside the constructor body to build
//     groupsResolver — kept as a local variable below.
//   - clipSourceBuilder/mediaCurator/sectionRegen/cacheEviction/
//     insightBuilder/clipServices/scriptsRepo/harvestSvc/driveFolderID:
//     zero production-code readers post-trim.
//
// Kept on ScriptFlowHandler (with rationale):
//   - log: universal logger; legacy adapter methods in
//     handler_legacy_*.go call h.log.Warn directly.
//   - jobsSvc: thin delegator enqueueEnvelope fallback path uses
//     h.jobsSvc + h.log + h.registry to call enqueueEnvelopeFn
//     directly when h.jobs is nil (godlike/07 minimum-blast-radius
//     test-fixture contract per PR-SCRIPT-JOBS-EXTRACT).
//   - registry: same fallback path uses h.registry; canonical
//     plumbing for MaxRetries via registry.DefaultMaxRetries(jType).
//   - adminToken: EnableAuth + AdminToken (script-flow
//     AdminTokenProvider satisfaction per middleware_auth.go).
//   - clipsSearcher: SearchClipsByName uses h.clipsSearcher.SearchByName
//     directly (handler_clip_search.go).
//   - gen/jobs/facade: delegation pointers to sub-handlers owned by
//     their respective files (handler_generate_handler.go +
//     handler_jobs.go + handler_facade.go).
//   - driveFolderClient/documentCreator: listed by user as
//     essential primitive primitives; thin delegators on
//     ScriptFlowHandler hop through h.facade.X so the canonical
//     owner is facade (godlike/06 SSOT one-owner-per-fact), but
//     ScriptFlowHandler carries its own access path for direct
//     use by future typed-handler call sites.
func NewScriptFlowHandler(deps ScriptFlowDeps) *ScriptFlowHandler {
	log := deps.Log
	if log == nil {
		log = zap.NewNop()
	}

	// assetTreeSvc is needed ONLY inside this constructor body to
	// build groupsResolver. The resolver lives on FacadeHandler
	// post-extraction; the local variable below is the single
	// re-use seam per godlike/06 SSOT.
	var groupsResolver *voiceover.GroupsResolver
	if deps.AssetTree != nil {
		if gr, err := voiceover.NewGroupsResolver(deps.AssetTree, log); err != nil {
			log.Warn("ScriptFlowHandler groups_resolver not initialized", zap.Error(err))
		} else {
			groupsResolver = gr
		}
	}

	return &ScriptFlowHandler{
		log:           log,
		jobsSvc:       deps.Jobs,
		registry:      deps.Registry,
		adminToken:    deps.AdminToken,
		clipsSearcher: deps.ClipsSearcher,

		// Issue 4 (June 2026, P1): plumb the typed *appjobs.Registry
		// through the enqueue helpers.
		// AZIONE 1 (July 2026): construct the 3-field HandlerGenerate
		// alongside the slim ScriptFlowHandler. POST /generate
		// delegates to h.gen.Generate(c).
		gen: NewHandlerGenerate(deps.Jobs, log, deps.Registry),

		// PR-SCRIPT-JOBS-EXTRACT (July 2026): construct the 3-field
		// JobsHandler. POST /api/script/jobs/:id mounts via
		// JobsHandler.RegisterJobRoutes; legacy adapters' h.enqueueEnvelope
		// thin-delegates to JobsHandler.EnqueueEnvelope.
		jobs: NewJobsHandler(deps.Jobs, log, deps.Registry),

		// PR-SCRIPT-FACADE-EXTRACT (July 2026): construct the 5-field
		// FacadeHandler. The 4 extracted methods access voService +
		// groupsResolver + driveFolderClient + documentCreator + log;
		// thin delegators on ScriptFlowHandler preserve byte-stable
		// surface for cross-package callers.
		facade: NewFacadeHandler(deps.Voiceover, groupsResolver, deps.DriveFolderClient, deps.DocumentCreator, log),
	}
}
