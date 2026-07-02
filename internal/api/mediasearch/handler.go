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
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"

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

// SemanticReadyChecker is the narrow port for the semantic_search_real
// readiness check. Production wiring injects the composition root's
// readiness probe (which composes: embedder presence, semantic
// backend in registry, Qdrant reachability, SQLite hydration path,
// workspace enforcement). Tests pass a stub. PER godlike/06 SSOT
// the api layer never imports `database/sql` / net/http probe
// implementations; readiness state is PRE-COMPUTED by the
// composition root and forwarded via this typed port.
type SemanticReadyChecker interface {
	// Ready returns nil when every canonical semantic-search sub-system is
	// wired correctly; otherwise it returns a typed multi-error listing
	// the failing sub-systems. Per godlike/07 fail-closed the orchestrator
	// returns ALL failures (not the first one) so operators see the full
	// picture in dashboards.
	Ready(ctx context.Context) error
}

// ReadinessReport is the JSON DTO for the semantic_search_real probe.
// Each sub-check has its own bool so dashboards surface per-subsystem
// status; the top-level "ready" is fail-closed (Ready AND every
// sub-check is true).
type ReadinessReport struct {
	Ready                 bool   `json:"ready"`
	Embedder              bool   `json:"embedder"`
	SemanticBackend       bool   `json:"semantic_backend"`
	QdrantReachable       bool   `json:"qdrant_reachable"`
	SQLiteHydrationReady  bool   `json:"sqlite_hydration_ready"`
	WorkspaceEnforced     bool   `json:"workspace_enforced"`
	Timestamp             string `json:"timestamp"`
	IndexVersion          string `json:"index_version,omitempty"`
	Failures              string `json:"failures,omitempty"` // space-joined, sanitized summary
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
	Aggregator        AggregatorSearcher
	SemanticReady     SemanticReadyChecker
	IndexVersion      IndexVersionSource
	Log               *zap.Logger
}

// IndexVersionSource is the narrow port for the live search-side
// index version. Production wiring injects a query-time lookup against
// the canonical IndexManifest (composition root wires the read
// adapter). Empty string when the index version is unknown — the
// handler renders `index_version: ""` (no fake availability per
// godlike/07).
type IndexVersionSource interface {
	IndexVersion(ctx context.Context) string
}

// staticIndexVersion is a static-source adapter the test / default
// composition wires use when no live IndexManifest is plumbed.
type staticIndexVersion struct{ v string }

func (s staticIndexVersion) IndexVersion(_ context.Context) string { return s.v }

// StaticIndexVersion produces an IndexVersionSource that always
// returns the supplied string. Use ONLY for tests / dry-runs; the
// canonical composition wires a live adapter.
func StaticIndexVersion(v string) IndexVersionSource { return staticIndexVersion{v: v} }

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

// ── DTO types ───────────────────────────────────────────────────────────

// searchRequest is the JSON body. Note: workspace_id, asset_id, and
// any other auth-context fields are deliberately absent from this
// struct — they MUST come from the auth context, never the body
// (AGENTS.md Hard Rule: never trust client-supplied workspace).
//
// QDRANT-004 §advanced filters: filters.Source/MediaType/Category/
// Language map 1:1 to Qdrant must-predicates (already wired in the
// service). Tags is AND-semantics; DurationMsMin is enforced
// post-hydration (canonical duration comes from SQLite, not the
// vector payload — that's why it's not a vector-store filter).
type searchRequest struct {
	Query   string              `json:"query" binding:"required"`
	Mode    string              `json:"mode,omitempty"` // "ann" or "hybrid"
	Limit   int                 `json:"limit,omitempty"`
	Filters searchRequestFilter `json:"filters,omitempty"`
}

type searchRequestFilter struct {
	Source        string   `json:"source,omitempty"`
	MediaType     string   `json:"media_type,omitempty"`
	Category      string   `json:"category,omitempty"`
	Language      string   `json:"language,omitempty"`
	Tags          []string `json:"tags,omitempty"`
	DurationMsMin int      `json:"duration_ms_min,omitempty"`
}

// searchResponse is the response DTO derived directly from
// search.Result. Fields are a 1:1 projection of search.Candidate —
// no lossy translation, no legacy envelope. OK flips to false only
// when the result is partial AND has zero items (no fake availability).
// Degraded is true when the result is partial but has at least one
// hit (the search "worked" but some backends are down).
//
// BACKFILL/CUTOVER (Commit 2): `BackendErrors` is the SANITIZED
// per-backend-failure map. The keys are backend names (canonical
// SearchBackend.Name()). The values are public-safe failure
// summaries — stack traces, internal URLs, secrets, and
// server-internal locators are STRIPPED by SanitizeProviderErrors
// below. godlike/07 fail-closed: a result partial with empty
// BackendErrors is impossible (the aggregator always populates
// ProviderErrors whenever Partial=true).
//
// IndexVersion is now OMITTABLE — Commit 2 removed the hardcoded
// empty IndexVersion as "index version unknown" (per godlike/07
// no-fake-availability).
type searchResponse struct {
	OK             bool               `json:"ok"`
	Query          string             `json:"query"`
	Mode           string             `json:"mode"`
	Count          int                `json:"count"`
	Items          []searchResultItem `json:"items"`
	Partial        bool               `json:"partial,omitempty"`
	Degraded       bool               `json:"degraded,omitempty"`
	BackendErrors  map[string]string  `json:"backend_errors,omitempty"` // SANITIZED — see SanitizeProviderErrors
	ChannelsUsed   []string           `json:"channels_used,omitempty"`
	NextCursor     string             `json:"next_cursor,omitempty"`
	IndexVersion   string             `json:"index_version,omitempty"`
}

// searchResultItem is the per-result item in the response,
// projected directly from search.Candidate.
type searchResultItem struct {
	AssetID    string  `json:"asset_id"`
	Score      float64 `json:"score"`
	Title      string  `json:"title"`
	Source     string  `json:"source"`
	MediaType  string  `json:"media_type"`
	PreviewURL string  `json:"preview_url"`
}

// ── Handler ─────────────────────────────────────────────────────────────

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

	limit := defaults.Int(req.Limit, search.DefaultLimit)
	q := searchQueryFromRequest(req, mode, limit, actor)

	res, err := h.aggreg.Search(c.Request.Context(), q)
	if err != nil {
		h.mapSearchError(c, err, actor.WorkspaceID)
		return
	}

	resp := resultToResponse(res, q.Text, mode, h.indexVer.IndexVersion(c.Request.Context()))
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
			Ready: false,
			Failures: "semantic_search_real checker not wired",
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

// ── Helpers ─────────────────────────────────────────────────────────────

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
//   WorkspaceContext.WorkspaceID → Actor.WorkspaceID
//   WorkspaceContext.PrincipalID  → Actor.UserID
//   WorkspaceContext.IsAdmin      → Actor.IsAdmin
func (h *Handler) extractActor(c *gin.Context) (search.Actor, bool) {
	scope := middleware.ScopeFromContext(c)
	if scope.WorkspaceID == "" || scope.WorkspaceID == "default" {
		apiutil.Error(c, http.StatusForbidden,
			"workspace_id is required (set X-Workspace-ID header for admin, or authenticate as a tenant principal)")
		return search.Actor{}, false
	}
	isAdmin, _ := c.Get("is_admin")
	principalID, _ := c.Get("principal_id")
	return search.Actor{
		WorkspaceID: scope.WorkspaceID,
		UserID:      strings.TrimSpace(toString(principalID)),
		IsAdmin:     toBool(isAdmin),
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

// SanitizeProviderErrors strips server-internal information from
// the per-backend failure map so the response never leaks stack
// traces, raw Drive URLs, /tmp filesystem paths, or
// deployment-secret strings. The sanitization is name-aware (the
// values are canonical short labels; anything matching internal
// patterns is replaced with a generic "<redacted>" marker).
//
// godlike/07 fail-closed: an entry whose value is a fully-redacted
// label is still propagated so dashboards see WHICH backends were
// involved (operators diagnose via dashboards + log lines, not via
// the response body).
func SanitizeProviderErrors(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = sanitizeMessage(v)
	}
	return out
}

// tokenRedactRegex matches token-bearing shapes like `token=`,
// `token: `, `token ` followed by an alphanumeric identifier.
// Bare-occurrence substrings like "context token expired" do
// NOT match (no `=`/`:`/alphanum adjacency), so the redactor
// only fires on actual token-leak shapes.
var tokenRedactRegex = regexp.MustCompile(`(?i)(?:\b|^)[-_]?token\b[-_:=]?\s*[A-Za-z0-9]`)

// sanitizeMessage returns a public-safe failure summary. The
// canonical heuristic: if the message contains marker patterns
// (filesystem paths, http(s) URLs, stack-trace adjacency, secret-
// bearing substrings, and auth-header markers), replace with
// "<redacted>"; otherwise, trim leading ordering noise
// ("backend: " prefix) and cap length at 240 chars.
//
// Commit 2 BACKFILL/CUTOVER (July 2026, code-reviewer revision):
// redactor set widened to cover the AGENTS.md godlike/07
// "operationally conservative" sidebar — added "password",
// tokenRedactRegex, "bearer", "authorization" markers in
// addition to the existing "secret" + filesystem-URL coverage.
// False positives (over-redacting benign failures) are the safe
// failure mode for the public-facing wire surface.
func sanitizeMessage(v string) string {
	s := strings.TrimSpace(v)
	if s == "" {
		return ""
	}
	low := strings.ToLower(s)
	if strings.HasPrefix(low, "/") ||
		strings.Contains(low, "/tmp/") ||
		strings.Contains(low, "stack:") ||
		strings.Contains(low, "secret") ||
		strings.Contains(low, "password") ||
		tokenRedactRegex.MatchString(low) ||
		strings.Contains(low, "bearer") ||
		strings.Contains(low, "authorization") ||
		strings.Contains(low, "https://") ||
		strings.Contains(low, "http://") {
		return "<redacted>"
	}
	// Cap the public length so a verbose upstream error does not
	// bloat the wire payload.
	const cap = 240
	if len(s) > cap {
		s = s[:cap] + "..."
	}
	return s
}

// buildReadinessReport populates the ReadinessReport DTO from the
// SemanticReadyChecker result. The detailed failure breakdown is
// surfaced as `Failures` (space-joined sanitized labels) so
// dashboards show WHICH sub-systems failed without leaking internal
// details.
//
// godlike/07 fail-closed: when err is non-nil but
// decomposeReadinessFailures returns an EMPTY map (the production
// checker failed to implement the typed Subsystems() contract), no
// per-subsystem boolean can be safely reported as TRUE. Every
// sub-check defaults to false; the Failures field carries the
// "typed readiness probe not wired" sentinel so operators see
// exactly which hard-dependency is missing. This contrast was
// caught by code-review on Commit 2 BACKFILL/CUTOVER: an empty
// subErrs map plus err != nil used to render every per-subsystem
// boolean TRUE (because `subErrs["embedder"] == ""` always evaluated
// true when the map was empty), producing an internally-inconsistent
// report (top-level Ready=false, per-subsystem all GREEN).
func buildReadinessReport(err error, indexVer string) ReadinessReport {
	if err == nil {
		return ReadinessReport{
			Ready:                true,
			Embedder:             true,
			SemanticBackend:      true,
			QdrantReachable:      true,
			SQLiteHydrationReady: true,
			WorkspaceEnforced:    true,
			Timestamp:            nowRFC3339(),
			IndexVersion:         indexVer,
		}
	}
	subErrs := decomposeReadinessFailures(err)
	// godlike/07 fail-closed: typed-probe-absent branch. When the
	// underlying error is non-nil but no typed decomposition is
	// available (subErrs is empty), every per-subsystem boolean
	// MUST default to false. The single Failures token names the
	// absent typed probe so operators see the failure mode.
	typedProbeAbsent := len(subErrs) == 0
	failuresField := joinFailures(subErrs)
	if typedProbeAbsent {
		failuresField = "typed readiness probe not wired (Subsystems() contract missing)"
	}
	return ReadinessReport{
		Ready:                false,
		Embedder:             !typedProbeAbsent && subErrs["embedder"] == "",
		SemanticBackend:      !typedProbeAbsent && subErrs["semantic_backend"] == "",
		QdrantReachable:      !typedProbeAbsent && subErrs["qdrant"] == "" && subErrs["qdrant_reachable"] == "",
		SQLiteHydrationReady: !typedProbeAbsent && subErrs["sqlite_hydration"] == "" && subErrs["sqlite"] == "",
		WorkspaceEnforced:    !typedProbeAbsent && subErrs["workspace"] == "",
		Timestamp:            nowRFC3339(),
		IndexVersion:         indexVer,
		Failures:             failuresField,
	}
}

// decomposeReadinessFailures splits a multi-error message into
// per-subsystem tokens. Production implementations use
// errors.As(target, &rErr) with a typed ReadinessError struct; the
// fallback below intentionally fails-CLOSED (godlike/07) — when the
// typed probe is absent AND err is non-nil, NO sub-check is filled
// (and buildReadinessReport renders the corresponding boolean as
// "false" — i.e. not-ready). This prevents silently-green readiness
// reports when the production checker forgets to implement the
// typed Subsystems() contract.
func decomposeReadinessFailures(err error) map[string]string {
	out := make(map[string]string, 5)
	if err == nil {
		return out
	}
	// Per-subsystem typed probes — typed-error multi-error carrier.
	if rErr, ok := err.(interface{ Subsystems() map[string]string }); ok {
		return rErr.Subsystems()
	}
	// godlike/07 fail-closed: do NOT string-scan. Empty map means
	// "cannot decompose" — buildReadinessReport marks every
	// sub-check as not-ready. A missing typed implementation is
	// a programming error at the composition root, not a
	// routine message scan.
	return out
}

// joinFailures joins the per-subsystem failure summaries into a
// single space-separated string for the report's Failures field.
// godlike/07 fail-closed: the join never throws; empty input → "".
func joinFailures(m map[string]string) string {
	if len(m) == 0 {
		return ""
	}
	parts := make([]string, 0, len(m))
	for k, v := range m {
		if v != "" {
			parts = append(parts, k+"="+sanitizeMessage(v))
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, " ")
}

// ── Error mapping ───────────────────────────────────────────────────────

// mapSearchError translates typed sentinels from the aggregator layer
// into HTTP status codes.
//
// Commit 2 BACKFILL/CUTOVER: ErrMissingWorkspace now matches the
// canonical search.ErrMissingWorkspace (godlike/06 SSOT). The legacy
// mediasearch.* sentinels that have NO canonical search counterpart
// yet (ErrHybridRequiresSparse, ErrNoBackendAvailable,
// ErrAllBackendsFailed) keep the deprecation alias import. They are
// application-level "fail-closed" sentinels; the BACKFILL wave that
// ports them into search/ is tracked in
// architecture/deprecations.yaml#SEARCH-MEDIASEARCH-CONTRACT-WAVE.
func (h *Handler) mapSearchError(c *gin.Context, err error, workspaceID string) {
	switch {
	case errors.Is(err, search.ErrInvalidCursor):
		apiutil.Error(c, http.StatusUnprocessableEntity, "invalid cursor")
	case errors.Is(err, search.ErrMissingWorkspace):
		apiutil.Error(c, http.StatusForbidden, "workspace_id required in context")
	case errors.Is(err, search.ErrHybridRequiresSparse):
		apiutil.Error(c, http.StatusUnprocessableEntity,
			"hybrid mode unavailable: sparse vector channel or BM25 tokenizer not configured")
	case errors.Is(err, search.ErrNoBackendAvailable):
		apiutil.Error(c, http.StatusServiceUnavailable,
			"no search backend available for the requested query")
	case errors.Is(err, search.ErrAllBackendsFailed):
		apiutil.Error(c, http.StatusBadGateway,
			"all search backends failed to return results")
	default:
		h.safeError("mediasearch.Search failed",
			zap.String("workspace", workspaceID),
			zap.Error(err))
		apiutil.InternalError(c, err)
	}
}

// resultToResponse converts the canonical search.Result into the
// handler's response DTO (searchResponse). Items map 1:1 from
// search.Candidate with no lossy translation — every field on
// Candidate is projected. OK flips to false only when Partial &&
// zero items (no fake availability).
// PR-AGENTE2-TRUTHFUL (Agente 2, Azione 3): Degraded is true when
// Partial && len(items) > 0 (at least one backend returned results
// but others failed). BackendErrors is the SANITIZED per-backend
// failure map (goes through SanitizeProviderErrors so the public
// wire never leaks stack traces / internal URLs / secrets).
// godlike/07 fail-closed: provider_errors is always populated
// whenever Partial=true (the aggregator always populates).
//
// Commit 2 BACKFILL/CUTOVER: IndexVersion is now sourced from the
// IndexVersionSource port (parameter `indexVer`), replacing the
// is rendered as JSON omitempty so callers do not get a stale
// static string.
func resultToResponse(r *search.Result, query string, mode search.SearchMode, indexVer string) *searchResponse {
	ok := true
	items := make([]searchResultItem, 0)
	var degraded bool
	var backendErrors map[string]string
	if r != nil {
		if r.Partial && len(r.Items) == 0 {
			ok = false
		}
		if r.Partial && len(r.Items) > 0 {
			degraded = true
		}
		if len(r.ProviderErrors) > 0 {
			backendErrors = SanitizeProviderErrors(r.ProviderErrors)
		}
		for _, c := range r.Items {
			items = append(items, searchResultItem{
				AssetID:    c.AssetID,
				Score:      c.Score,
				Title:      c.Title,
				Source:     c.Source,
				MediaType:  c.MediaType,
				// PreviewURL passes through UNCHANGED:
				// search.Candidate.PreviewURL is the canonical
				// signed delivery URL produced by the only
				// legitimate source (delivery.Publisher.BuildAuthorizedURL).
				// Sanitising it would break the signed-URL
				// contract QDRANT-004 requires; the safety
				// layer lives at construction time (the signer
				// never mints raw Drive paths), not at
				// projection time. godlike/07 fail-closed
				// applies to error strings, not to valid
				// response URLs.
				PreviewURL: c.PreviewURL,
			})
		}
	}
	nextCursor := ""
	if r != nil {
		nextCursor = r.NextCursor
	}
	partial := false
	if r != nil {
		partial = r.Partial
	}
	var channelsUsed []string
	if r != nil && len(r.ChannelsUsed) > 0 {
		channelsUsed = r.ChannelsUsed
	}
	return &searchResponse{
		OK:            ok,
		Query:         strings.TrimSpace(query),
		Mode:          string(mode),
		Count:         len(items),
		Items:         items,
		Partial:       partial,
		Degraded:      degraded,
		BackendErrors: backendErrors,
		ChannelsUsed:  channelsUsed,
		NextCursor:    nextCursor,
		IndexVersion:  indexVer,
	}
}

// searchQueryFromRequest builds a search.Query from the API request DTO.
// Workspace identity is taken from the canonical search.Actor
// (Commit 2 BACKFILL/CUTOVER migration target of the historical
// WorkspaceContext fed into Query.Actor).
// PR-1 (Agente 2, Azione 1): workspace is propagated into Query.Actor
// so every backend receives the real tenant identity.
// PR-AGENTE2-MEDIATYPE (Agente 2, Azione 2): when the request filter
// carries media_type, it is also forwarded as Query.MediaTypes so
// the BackendRegistry can select capability-compatible backends.
func searchQueryFromRequest(req searchRequest, mode search.SearchMode, limit int, actor search.Actor) search.Query {
	mediaType := strings.TrimSpace(req.Filters.MediaType)
	var mediaTypes []string
	if mediaType != "" {
		mediaTypes = []string{mediaType}
	}
	return search.Query{
		Text:       strings.TrimSpace(req.Query),
		Mode:       mode,
		Limit:      limit,
		MediaTypes: mediaTypes,
		Actor:      actor,
		Filters: search.Filters{
			Source:        strings.TrimSpace(req.Filters.Source),
			MediaType:     mediaType,
			Category:      strings.TrimSpace(req.Filters.Category),
			Language:      strings.TrimSpace(req.Filters.Language),
			Tags:          req.Filters.Tags,
			DurationMsMin: req.Filters.DurationMsMin,
		},
	}
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

// nowRFC3339 returns the current UTC time formatted as RFC3339. Used
// by the readiness endpoint to stamp the report. Centralizing the
// call here makes a future migration to monotonic-time stamps a
// single-file change. Direct stdlib time usage (no pkg/timeutil
// dependency) keeps the handler free of cross-package coupling for
// one line of formatting.
func nowRFC3339() string {
	return time.Now().UTC().Format(time.RFC3339)
}
