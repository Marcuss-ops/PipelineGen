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
//   - the former facade delegators and facade field (they had no
//     external callers and were removed with the compatibility layer).
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
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// ScriptFlowHandler is the slim struct-literal-friendly HTTP
// orchestrator. Fields are partitioned into 3 groups:
//
//  1. Domain primitives (clipsSearcher) — used by SearchClipsByName.
//  2. Auth primitive (adminToken) — consumed by EnableAuth + AdminToken.
//  3. Delegation pointers (gen + shorts + jobs) — sub-handlers that own
//     canonical impls of POST /generate, /shorts/*, /jobs/:id.
//
// FASE 2 (July 2026): jobsSvc + registry fields are REMOVED.
// The FASE 2 enqueue path is owned by h.gen.operations + h.jobs.operations.
type ScriptFlowHandler struct {
	clipsSearcher ClipSearcher
	adminToken    string
	gen           *HandlerGenerate
	shorts        *HandlerShorts
	jobs          *JobsHandler
	log           *zap.Logger
}

// jobsRegisterRoutes mounts /api/script/jobs/:id via JobsHandler.
// `h` (ScriptFlowHandler) is the auth-bearing AdminTokenProvider
// (carries adminToken); JobsHandler does not.
func (h *ScriptFlowHandler) jobsRegisterRoutes(r *gin.RouterGroup) {
	h.jobs.RegisterJobRoutes(r, h)
}

// RegisterRoutes mounts every active script-flow route under r.
// AZIONE 1 (July 2026): POST /generate → h.gen.
// PR-SHORTS-EXTRACT (July 2026): /shorts/* → h.shorts.
// PR-SCRIPT-JOBS-EXTRACT: /jobs/:id → h.jobsRegisterRoutes.
//
// Legacy generate adapters are no longer mounted.
func (h *ScriptFlowHandler) RegisterRoutes(r *gin.RouterGroup) {
	h.gen.GenerateRoute(r)
	h.shorts.ShortsRoute(r)

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

// SearchClipsByName is the canonical handler for
// GET /api/script/clips/search?q= discovery endpoint.
// Canonical implementation lives in handler_clip_search.go
// (godlike/06 SSOT: one owner per fact). This file retains
// the RegisterRoutes mount point only.

// FASE 2 (July 2026): the pre-FASE-2 `enqueueEnvelope` thin
// delegator is REMOVED. Legacy adapter routes are no longer
// mounted (see RegisterRoutes above; the only active routes
// are /generate via h.gen and /jobs/:id via h.jobsRegisterRoutes).
// The dead-code purge removes the un-reachable delegator per
// godlike/07 minimum-blast-radius.
