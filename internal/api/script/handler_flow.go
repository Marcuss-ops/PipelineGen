// Package script (api/script) — ScriptFlowHandler is the slim HTTP
// orchestrator (post-PR-script-deps-slim, 2026-07-04).
// Surface contracts (RegisterRoutes + EnableAuth/AdminToken + facade
// accessors + enqueue thunk + SearchClipsByName) preserved byte-stable per
// PR-SCRIPT-AUTH-EXTRACT / PR-SCRIPT-JOBS-EXTRACT /
// PR-SCRIPT-FACADE-EXTRACT (godlike/06 SSOT 3-surface lockstep via
// architecture/current.yaml#SCRIPT-FLOW-SPLIT).
//
// PR-script-deps-slim (July 2026, P1) removals:
//   - sectionRegen + cacheEviction fields + RegenerateSection +
//     EvictCache methods + 2 routes (the fields were never
//     populated by NewScriptFlowHandler so the routes always
//     returned 503; godlike/07 no-fake-availability: routes that
//     always 503 are fake-availability).
//   - 4 facade delegator methods (GetVoiceoverService +
//     GetGroupsResolver + ResolveDriveFolderID + MaybeCreateGoogleDoc)
//     + facade field (ZERO external callers per audit-pin in
//     flow.go:88; the FacadeHandler type remains as the canonical
//     owner of the 4 real impls).
//
// Construction seam (`*ScriptFlowHandler` from `ScriptFlowDeps`) +
// ScriptFlowDeps itself live in handler_deps.go
// (PR-SCRIPT-DEPENDENCIES-EXTRACT). Lower-priority fields (engine +
// imgService + realtimeSvc + associationSvc + voService + assetTreeSvc
// + groupsResolver + clipSourceBuilder + mediaCurator +
// scriptsRepo + harvestSvc + driveFolderID + clipServices +
// sectionRegen + cacheEviction + DriveScriptsGenFolder) are dropped
// as dead wire per godlike/07 no-fake-availability (zero
// production-code readers post-trim).

package script

import (
	"context"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	jobservice "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	domainScript "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// DriveFolderClient abstracts folder creation for Drive resolution.
//
// DEPRECATED (FASE A5, July 2026): the sole consumer (FacadeHandler)
// now uses delivery.Publisher.ResolveFolder instead. This interface
// and its concrete adapter (driveFolderAdapterImpl in
// wire_script_adapters.go) are dead code. Retained to avoid
// godlike/07 minimum-blast-radius cross-package churn until a future
// dead-code purge wave retires them.
//
// godlike/06 SSOT: orchestrator owns the contract type — STAYS here
// even though the canonical field reference lives on FacadeHandler
// (the canonical owner of the 4 facade method impls).
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
//  1. **Domain primitives** (clipsSearcher) — used by
//     SearchClipsByName thin path.
//  2. **Auth primitive** (adminToken) — consumed by EnableAuth +
//     AdminToken (the AdminTokenProvider interface satisfaction
//     locked by middleware_auth.go's compile-time assertion).
//  3. **Delegation pointers** (gen + jobs) — sub-handlers that own
//     canonical impls of POST /generate, /jobs/:id.
//  4. **Operating envelope** (jobsSvc + registry + log + caps) — used by:
//     - jobsSvc + registry: enqueueEnvelope fallback path on the
//     godlike/07 minimum-blast-radius test fixtures
//     (PR-SCRIPT-JOBS-EXTRACT).
//     - log: universal; every per-method logger.
//     - caps: SCRIPTCONTRACT-2026-07-08 PR-2 PreflightCaps, the
//     flat composition-time postprocessor-availability surface.
//     Mirrored to h.gen + h.jobs at construction time; the field
//     here is the canonical owner of the top-level preflight
//     surface for the legacy-adapter thin-delegator path
//     (h.enqueueEnvelope nil-jobs fallback).
type ScriptFlowHandler struct {
	// Domain primitives
	clipsSearcher ClipSearcher

	// Auth primitive — locked by middleware_auth.go's
	// var _ AdminTokenProvider = (*ScriptFlowHandler)(nil).
	adminToken string

	// Delegation pointers — canonical owners live in their files:
	//   - gen    → handler_generate_handler.go
	//   - jobs   → handler_jobs.go
	gen  *HandlerGenerate
	jobs *JobsHandler

	// Operating envelope
	jobsSvc  jobservice.Service
	log      *zap.Logger
	registry *appjobs.Registry
	caps     PreflightCaps
}

// jobsRegisterRoutes mounts /api/script/jobs/:id via JobsHandler.
// `h` (ScriptFlowHandler) is the auth-bearing AdminTokenProvider
// (carries adminToken); JobsHandler does not.
func (h *ScriptFlowHandler) jobsRegisterRoutes(r *gin.RouterGroup) {
	h.jobs.RegisterJobRoutes(r, h)
}

// RegisterRoutes mounts every active script-flow route under r.
// AZIONE 1 (July 2026): POST /generate → h.gen.
// PR-SCRIPT-JOBS-EXTRACT: /jobs/:id → h.jobsRegisterRoutes.
//
// Legacy generate adapters are no longer mounted.
func (h *ScriptFlowHandler) RegisterRoutes(r *gin.RouterGroup) {
	h.gen.GenerateRoute(r)

	r.GET("/clips/search", h.SearchClipsByName)

	h.jobsRegisterRoutes(r)
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

// enqueueEnvelope thin delegator. Canonical impl on
// JobsHandler.EnqueueEnvelope (handler_jobs.go) per
// PR-SCRIPT-JOBS-EXTRACT. The nil-jobs fallback preserves the
// godlike/07 minimum-blast-radius test-fixture contract (struct-
// literal fixtures rely on direct enqueueEnvelopeFn dispatch).
//
// SCRIPTCONTRACT-2026-07-08 PR-2: the `h.caps` field is the
// canonical preflight surface; both the nil-jobs fallback path
// AND the JobsHandler.EnqueueEnvelope dispatch thread h.caps to
// enqueueEnvelopeFn. godlike/06 SSOT: there is exactly ONE
// PreflightCaps instance per ScriptFlowHandler (set in
// NewScriptFlowHandler); both the canonical live /generate path
// (HandlerGenerate.Generate) and the legacy-adapter thin-delegator
// path (this method) share it.
func (h *ScriptFlowHandler) enqueueEnvelope(c *gin.Context, env domainScript.GenerationEnvelopeV2) {
	if h.jobs == nil {
		enqueueEnvelopeFn(c, env, h.jobsSvc, h.log, h.registry, h.caps, nil)
		return
	}
	h.jobs.EnqueueEnvelope(c, env, h.caps)
}
