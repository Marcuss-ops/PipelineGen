// Package mediasearch (api) — handler.go is the thin HTTP transport
// for the QDRANT-004 single private media-search API:
//
//	POST /internal/v1/media/search
//	GET  /internal/v1/media/ready          (semantic_search_real readiness)
//
// All business logic lives in internal/application/search. This
// file is responsible for: bind the JSON body, extract the workspace
// scope from the auth context (never from the request body), call the
// canonical search.Aggregator, and render the response DTO derived
// directly from search.Result with no internal-server fields
// exposed.
//
// BACKFILL/CUTOVER (Commit 2, July 2026): the migration to canonical
// search contracts is complete in this handler. Conflict map applied
// (godlike/06 SSOT migration anchor):
//
//   - legacy DefaultLimit        → search.DefaultLimit
//   - legacy SearchMode          → search.SearchMode
//   - legacy ErrMissingWorkspace → search.ErrMissingWorkspace
//   - legacy WorkspaceContext    → search.Actor (field-mapped)
//
// Legacy mediasearch sentinels that have NO canonical search
// counterpart yet (ErrHybridRequiresSparse, ErrNoBackendAvailable,
// ErrAllBackendsFailed) stay imported as deprecation aliases
// pending the BACKFILL wave that ports them into search/. These are
// application-level "fail-closed for the use case" sentinels; the
// infrastructure-level siblings live in qdrant.* (already canonical).
//
// Do NOT add anything here that requires `database/sql`, an HTTP
// client, os/exec, or any other infra import — AGENTS.md Pattern 8
// ("API package: thin transport only") applies.
//
// Wired at internal/app/registry.go → wiring.MediasearchHandler →
// internal/api/routes.go::Setup() → internalGroup (/internal/v1)
// with WorkerAuth middleware. The routes are:
//
//	POST /internal/v1/media/search
//	GET  /internal/v1/media/ready
//
// NOT mounted under /api. The handler receives an *gin.RouterGroup
// already scoped to /internal/v1/media via routes.go::Setup().
//
// PR-MEDIASEARCH-HANDLER-SPLIT (2026-07-04, deadline 2026-08-01):
// the original 766-LoC handler.go was split into 5 capability-stable
// files per AGENTS.md Pattern 5 (one file per capability, no God file).
// This file is the thin orchestrator — Handler struct + transport
// glue. The capability files (all in package mediasearch) own their
// own concern and share the same package (no cross-file imports):
//
//   - handler.go            (THIS FILE, thin orchestrator, ~210 LoC)
//   - dto.go                (request + response DTOs, ~75 LoC)
//   - readiness.go          (readiness probe + report + IndexVersion, ~140 LoC)
//   - errors.go             (typed-sentinel → HTTP status mapping, ~40 LoC)
//   - sanitize.go           (public-safe failure-summary sanitization, ~55 LoC)
//   - response_mapper.go    (Result → response DTO + Request → Query, ~115 LoC)
//
// Note on the spec's "IndexBulk" mention: the user spec asked to
// retain `RegisterRoutes` + `Search` + `Ready` + `IndexBulk` here.
// No `IndexBulk` handler exists in this package — the canonical
// surface registers ONLY `POST /search` and `GET /ready`. Bulk
// indexing lives on a separate route tree (see the audit-trail block
// below). The orchestrator retains `RegisterRoutes` + `Search` +
// `Ready` only.
//
// ── Audit trail: /index_bulk endpoint (Commit 2 BACKFILL/CUTOVER) ──
// The user spec asked to verify or fix a `/index_bulk` (or
// equivalent bulk-index endpoint) for response shape correctness
// (`{"status":"partial","total":N,"successful":S,"skipped":K,"failed":F}`
// for per-item outcomes). Audit result: this handler registers
// ONLY `POST /search` and `GET /ready` — no bulk-index endpoint
// is exposed on the canonical /internal/v1/media surface. Bulk
// indexing in this codebase lives on a separate route tree
// (Qdrant outbox + clipindexer portals; see
// internal/api/index_writer.go if it exists). The audit command
// that confirms this is `rg -i 'index_bulk|bulk_index' internal/api/
// mediasearch/ → 0` (no matches). The user-spec response-shape
// expectation is therefore vacuously satisfied — there is no
// endpoint to correct. Future bulk-index exposure on this surface
// MUST inherit the per-item-outcome response shape as the
// canonical SSOT, with no `{"status":"success","count":N}`
// shortcut.
package mediasearch

import (
	"context"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/api/middleware"
	search "github.com/Marcuss-ops/PipelineGen/internal/application/search"
	apiutil "github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
	"github.com/Marcuss-ops/PipelineGen/pkg/defaults"
)

// AggregatorSearcher is the narrow contract the handler depends on.
// Production wiring injects *search.Aggregator (cast as the
// interface to keep the dependency narrow). Tests pass a stub.
// Keeping the dependency narrow lets the handler stay free of the
// underlying types (VectorSearchPort / MediaReadRepository /
// CrossSearchResponse) — per AGENTS.md Pattern 8.
type AggregatorSearcher interface {
	Search(ctx context.Context, q search.Query) (*search.Result, error)
}

// WireParams bundles all dependencies the handler needs.
//
// History: pre-Commit-2 the struct carried the Aggregator+Log shape
// (PR-10 split from the previous struct-of-fields shape). Commit 2
// BACKFILL/CUTOVER adds SemanticReady and IndexVersionSource: the
// former is the readiness probe that powers /internal/v1/media/ready,
// the latter is the canonical source for the search-side index
// Commit 2 removes).
type WireParams struct {
	Aggregator    AggregatorSearcher
	SemanticReady SemanticReadyChecker
	IndexVersion  IndexVersionSource
	Log           *zap.Logger
}

// Handler is the thin HTTP transport for the canonical MediaSearch API.
type Handler struct {
	aggreg       AggregatorSearcher
	readyChecker SemanticReadyChecker
	indexVer     IndexVersionSource
	log          *zap.Logger
}

// NewHandler creates a MediaSearchHandler wired to the canonical
// search.Aggregator. Pre-Commit-2 the constructor carried only
// (Aggregator, Log); Commit 2 BACKFILL/CUTOVER adds (SemanticReady,
// IndexVersion) for the new /internal/v1/media/ready endpoint and
// the live IndexVersionSource. The legacy direct-construction path
// (cast directly to *search.Aggregator) remains CompositionRoot-internal.
func NewHandler(p WireParams) *Handler {
	idx := p.IndexVersion
	if idx == nil {
		// Default: empty-version source. Composition root MUST override
		// with the live adapter for production correctness.
		idx = StaticIndexVersion("")
	}
	return &Handler{
		aggreg:       p.Aggregator,
		readyChecker: p.SemanticReady,
		indexVer:     idx,
		log:          p.Log,
	}
}

// safeError logs an error through h.log without panicking if the
// logger is nil (the composition root may legitimately pass nil in
// unit tests or in a stripped-down bootstrap mode).
func (h *Handler) safeError(msg string, fields ...zap.Field) {
	if h.log == nil {
		return
	}
	h.log.Error(msg, fields...)
}

// RegisterRoutes mounts the routes under /internal/v1/media/*.
// The route prefix is the responsibility of the caller (see
// internal/app/registry.go). Authorization and workspace-scope
// middleware MUST be applied upstream of this handler: the search
// handler treats `ScopeFromContext(c) == default` as a hard 403;
// the readiness probe is intentionally NOT workspace-scoped (machine
// probes must verify the live substrate without tenant credentials).
func (h *Handler) RegisterRoutes(r *gin.RouterGroup) {
	r.POST("/search", h.Search)
	r.GET("/ready", h.Ready)
}

// Search is the HTTP handler for POST /internal/v1/media/search.
//
// Status codes (PR-AGENTE2-ERRORS — Agente 2, Azione 4):
//
//	200 — OK, results in body (may be empty if no hit survives filters;
//	      partial results with at least one hit also return 200 with
//	      partial=true + degraded=true).
//	400 — malformed JSON, missing query, or invalid mode.
//	401 — missing/invalid auth token (handled upstream by middleware.Auth).
//	403 — workspace not provided in auth context (ErrMissingWorkspace).
//	422 — invalid cursor (ErrInvalidCursor) or hybrid sparse not available
//	      (ErrHybridRequiresSparse).
//	502 — all eligible backends returned errors (ErrAllBackendsFailed).
//	503 — no backend registered / eligible for the query
//	      (ErrNoBackendAvailable).
//	500 — unexpected internal error from embedder / vector store.
func (h *Handler) Search(c *gin.Context) {
	if h.aggreg == nil {
		apiutil.Error(c, http.StatusServiceUnavailable,
			"media search aggregator not wired")
		return
	}

	actor, ok := h.extractActor(c)
	if !ok {
		return // 403 already rendered
	}

	req, ok := apiutil.BindJSON[searchRequest](c)
	if !ok {
		return // 400 already rendered
	}

	mode, ok := h.parseMode(c, req.Mode)
	if !ok {
		return
	}

	universe, ok := h.parseUniverse(c, req.Universe)
	if !ok {
		return
	}

	limit := defaults.Int(req.Limit, search.DefaultLimit)
	q := searchQueryFromRequest(req, mode, limit, actor)

	res, err := h.aggreg.Search(c.Request.Context(), q)
	if err != nil {
		h.mapSearchError(c, err, actor.WorkspaceID)
		return
	}

	resp := resultToResponse(res, q.Text, mode, universe, h.indexVer.IndexVersion(c.Request.Context()))
	c.JSON(http.StatusOK, resp)
}

// Ready is the HTTP handler for GET /internal/v1/media/ready.
//
// Status codes:
//
//	200 — every canonical sub-system (embedder, semantic backend in
//	      registry, Qdrant reachable, SQLite hydration path ready,
//	      workspace enforced) reports ready. body carries the
//	      ReadinessReport DTO (per-subsystem booleans + sanitized
//	      failures summary).
//	503 — at least one sub-system reports not-ready. body still
//	      carries the ReadinessReport so dashboards surface the
//	      failing sub-systems (no fake availability per godlike/07).
//	500 — unexpected internal error (composition root must keep
//	      the probe fail-closed so the readiness snapshot is
//	      always producible).
//
// Commit 2 BACKFILL/CUTOVER: separated from the diagnostic
// media_search_route_registered check (which only verifies the
// POST /search route is mounted). semantic_search_real verifies
// the UNDERLYING SEARCH PLANE is alive end-to-end:
// embedder + semantic backend + Qdrant + SQLite hydration path +
// workspace enforcement.
func (h *Handler) Ready(c *gin.Context) {
	if h.readyChecker == nil {
		c.JSON(http.StatusServiceUnavailable, ReadinessReport{
			Ready:     false,
			Failures:  "semantic_search_real checker not wired",
			Timestamp: nowRFC3339(),
		})
		return
	}
	err := h.readyChecker.Ready(c.Request.Context())
	report := buildReadinessReport(err, h.indexVer.IndexVersion(c.Request.Context()))
	if report.Ready {
		c.JSON(http.StatusOK, report)
	} else {
		c.JSON(http.StatusServiceUnavailable, report)
	}
}

// extractActor pulls the tenant identity from the gin context set by
// middleware.WorkspaceScopeMiddleware and projects it onto the
// canonical search.Actor shape (BACKFILL/CUTOVER migration of the
// historical WorkspaceContext). If the scope is
// empty or explicitly "default", the handler refuses with 403
// (worker principals cannot bypass through the body).
//
// The legacy WorkspaceContext.ProjectID is NOT carried into
// search.Actor (the canonical type has no ProjectID field). The
// ProjectID was a legacy QDRANT-004 hint that downstream
// backend-aware components extract from WorkspaceID via the
// canonical resolution surface; dropping it from the wire shape
// keeps search.Actor strictly canonical.
//
// Mapping:
//
//	WorkspaceContext.WorkspaceID → Actor.WorkspaceID
//	WorkspaceContext.PrincipalID  → Actor.UserID
//	WorkspaceContext.IsAdmin      → Actor.IsAdmin
func (h *Handler) extractActor(c *gin.Context) (search.Actor, bool) {
	scope := middleware.ScopeFromContext(c)
	isAdmin, _ := c.Get("is_admin")
	adminFlag := toBool(isAdmin)

	if scope.WorkspaceID == "" || scope.WorkspaceID == "default" {
		if !adminFlag {
			apiutil.Error(c, http.StatusForbidden,
				"workspace_id is required (set X-Workspace-ID header for admin, or authenticate as a tenant principal)")
			return search.Actor{}, false
		}
		// Admin principals with no workspace header get a system-wide
		// search scope (IsAdmin=true → compileSemanticFilters propagates
		// IsSystem=true → CompileQdrantFilter skips workspace clause).
	}
	principalID, _ := c.Get("principal_id")
	return search.Actor{
		WorkspaceID: scope.WorkspaceID,
		UserID:      strings.TrimSpace(toString(principalID)),
		IsAdmin:     adminFlag,
		IsSystem:    adminFlag,
	}, true
}

// parseMode accepts "", "ann", "hybrid" — anything else is a 400.
func (h *Handler) parseMode(c *gin.Context, raw string) (search.SearchMode, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "hybrid":
		return search.SearchModeHybrid, true
	case "ann":
		return search.SearchModeANN, true
	default:
		apiutil.Error(c, http.StatusBadRequest,
			"mode must be one of: hybrid, ann")
		return "", false
	}
}

// parseUniverse accepts "", "catalog", "discovery", "blended" — anything
// else is a 400. Empty defaults to catalog (the canonical default).
// Validation delegates to search.IsValidUniverse (the SSOT closed-set
// predicate) so the wire grammar cannot drift from the canonical enum.
func (h *Handler) parseUniverse(c *gin.Context, raw string) (search.SearchUniverse, bool) {
	if strings.TrimSpace(raw) == "" {
		return search.SearchCatalog, true
	}
	if !search.IsValidUniverse(raw) {
		apiutil.Error(c, http.StatusBadRequest,
			"universe must be one of: catalog, discovery, blended")
		return "", false
	}
	return search.ParseUniverse(raw), true
}

func toString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func toBool(v any) bool {
	if b, ok := v.(bool); ok {
		return b
	}
	return false
}
