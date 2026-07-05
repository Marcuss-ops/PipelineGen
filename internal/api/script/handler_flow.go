// Package script (api/script) — ScriptFlowHandler is the slim HTTP
// orchestrator (post-PR-SCRIPT-DEPENDENCIES-EXTRACT, 2026-07-04).
// Surface contracts (RegisterRoutes + EnableAuth/AdminToken + facade
// accessors + enqueue thunk + SearchClipsByName +
// RegenerateSection + EvictCache + LegacyGenerateFromClips +
// LegacyGenerateWithImages) preserved byte-stable per
// PR-SCRIPT-AUTH-EXTRACT / PR-SCRIPT-JOBS-EXTRACT /
// PR-SCRIPT-FACADE-EXTRACT (godlike/06 SSOT 3-surface lockstep via
// architecture/current.yaml#SCRIPT-FLOW-SPLIT).
//
// Construction seam (`*ScriptFlowHandler` from `ScriptFlowDeps`) +
// ScriptFlowDeps itself live in handler_deps.go
// (PR-SCRIPT-DEPENDENCIES-EXTRACT). Lower-priority fields (engine +
// imgService + realtimeSvc + associationSvc + voService + assetTreeSvc
// + groupsResolver + clipSourceBuilder + mediaCurator +
// insightBuilder + clipServices + scriptsRepo + harvestSvc +
// driveFolderID) are dropped as dead wire per godlike/07
// no-fake-availability (zero production-code readers post-trim).

package script

import (
	"context"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/usecase"
	"github.com/Marcuss-ops/PipelineGen/internal/application/voiceover"
	jobservice "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	domainScript "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// DriveFolderClient abstracts folder creation for Drive resolution.
// godlike/06 SSOT: orchestrator owns the contract type — STAYS here
// even though the canonical field reference lives on FacadeHandler.
type DriveFolderClient interface {
	GetOrCreateFolder(ctx context.Context, name, parentID string) (string, error)
}

// DocumentCreator abstracts Google Doc creation. STAYS here per
// godlike/06 SSOT (orchestrator owns the contract type); canonical
// field reference lives on FacadeHandler.
type DocumentCreator interface {
	CreateDoc(ctx context.Context, title, content, folderID string) (docURL, docID string)
}

// ScriptFlowHandler is the slim struct-literal-friendly HTTP
// orchestrator. Fields are partitioned into 3 groups:
//
//  1. **Domain primitives** (driveFolderClient + documentCreator +
//     clipsSearcher) — used by MaybeCreateGoogleDoc / SearchClipsByName
//     thin paths.
//  2. **Auth primitive** (adminToken) — consumed by EnableAuth +
//     AdminToken (the AdminTokenProvider interface satisfaction
//     locked by middleware_auth.go's compile-time assertion).
//  3. **Delegation pointers** (gen + jobs + facade) — sub-handlers
//     that own canonical impls of POST /generate, /jobs/:id + facade
//     accessors; thin delegators on this struct preserve byte-stable
//     cross-package surface per godlike/07 minimum-blast-radius.
//  4. **Operating envelope** (jobsSvc + registry + log + sectionRegen +
//     cacheEviction) — used by:
//     - jobsSvc + registry: enqueueEnvelope fallback path on the
//     godlike/07 minimum-blast-radius test fixtures (PR-SCRIPT-JOBS-
//     EXTRACT).
//     - log: universal; every per-method logger.
//     - sectionRegen + cacheEviction: RegenerateSection + EvictCache
//     (handler_flow_ops.go).
type ScriptFlowHandler struct {
	// Domain primitives
	driveFolderClient DriveFolderClient
	documentCreator   DocumentCreator
	clipsSearcher     ClipSearcher

	// Auth primitive — locked by middleware_auth.go's
	// var _ AdminTokenProvider = (*ScriptFlowHandler)(nil).
	adminToken string

	// Delegation pointers — canonical owners live in their files:
	//   - gen    → handler_generate_handler.go
	//   - jobs   → handler_jobs.go
	//   - facade → handler_facade.go
	gen    *HandlerGenerate
	jobs   *JobsHandler
	facade *FacadeHandler

	// Operating envelope
	jobsSvc       jobservice.Service
	log           *zap.Logger
	registry      *appjobs.Registry
	sectionRegen  *usecase.SectionRegenerator
	cacheEviction *usecase.CacheEvictionUseCase
}

// jobsRegisterRoutes mounts /api/script/jobs/:id via JobsHandler.
// `h` (ScriptFlowHandler) is the auth-bearing AdminTokenProvider
// (carries adminToken); JobsHandler does not.
func (h *ScriptFlowHandler) jobsRegisterRoutes(r *gin.RouterGroup) {
	h.jobs.RegisterJobRoutes(r, h)
}

// RegisterRoutes mounts every script-flow route under r.
// AZIONE 1 (July 2026): POST /generate → h.gen.
// PR-SCRIPT-JOBS-EXTRACT: /jobs/:id → h.jobsRegisterRoutes.
// PR-script-legacy-contract (Jul 2026, P0 ABSOLUTE): the 2 legacy r.POST
// lines (generate-from-clips + generate-with-images) physically move
// out of this function into RegisterLegacyDeprecationRoutes
// (handler_legacy_deprecation.go — the canonical godlike/06 SSOT for
// the deprecation contract). They're delegated-to at the end of this
// function so observable route surface is byte-compatible with
// pre-PR callers of RegisterRoutes directly (handler_test.go +
// handler_idempotency_test.go pass unchanged). godlike/07
// minimum-blast-radius preserved (no api.NewRouteModule signature
// change).
func (h *ScriptFlowHandler) RegisterRoutes(r *gin.RouterGroup) {
	h.gen.GenerateRoute(r)

	r.GET("/clips/search", h.SearchClipsByName)

	h.jobsRegisterRoutes(r)
	r.POST("/:id/sections/:section_id/regenerate", h.RegenerateSection)
	r.POST("/cache/evict", h.EvictCache)

	h.RegisterLegacyDeprecationRoutes(r)
}

// EnableAuth + AdminToken satisfy AdminTokenProvider.
// middleware_auth.go compile-time assertion locks the contract.
func (h *ScriptFlowHandler) EnableAuth() bool { return h.adminToken != "" }

func (h *ScriptFlowHandler) AdminToken() string {
	if h == nil {
		return ""
	}
	return h.adminToken
}

// 4 facade thin delegators — canonical impl on FacadeHandler
// (handler_facade.go) per PR-SCRIPT-FACADE-EXTRACT.
func (h *ScriptFlowHandler) GetVoiceoverService() *voiceover.Service {
	return h.facade.GetVoiceoverService()
}

func (h *ScriptFlowHandler) GetGroupsResolver() *voiceover.GroupsResolver {
	return h.facade.GetGroupsResolver()
}

func (h *ScriptFlowHandler) ResolveDriveFolderID(ctx context.Context, input, defaultRootID string) (string, error) {
	return h.facade.ResolveDriveFolderID(ctx, input, defaultRootID)
}

func (h *ScriptFlowHandler) MaybeCreateGoogleDoc(ctx context.Context, title, content, folderID string, createDoc bool) (string, string) {
	return h.facade.MaybeCreateGoogleDoc(ctx, title, content, folderID, createDoc)
}

// enqueueEnvelope thin delegator. Canonical impl on
// JobsHandler.EnqueueEnvelope (handler_jobs.go) per
// PR-SCRIPT-JOBS-EXTRACT. The nil-jobs fallback preserves the
// godlike/07 minimum-blast-radius test-fixture contract (struct-
// literal fixtures rely on direct enqueueEnvelopeFn dispatch).
func (h *ScriptFlowHandler) enqueueEnvelope(c *gin.Context, env domainScript.GenerationEnvelopeV2) {
	if h.jobs == nil {
		enqueueEnvelopeFn(c, env, h.jobsSvc, h.log, h.registry)
		return
	}
	h.jobs.EnqueueEnvelope(c, env)
}
