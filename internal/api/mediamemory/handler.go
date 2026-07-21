// Package mediamemory (api) — handler.go is the thin HTTP transport
// for the canonical MediaMemory API surface (godlike/06 SSOT: thin
// transport only, business logic lives in
// internal/application/mediamemory).
//
// Routes mounted under /api/media-memory/* (mirrors the architecture
// doc, "API consigliate"):
//
//	POST /api/media-memory/resolve
//	POST /api/media-memory/bindings
//	GET  /api/media-memory/bindings
//	POST /api/media-memory/feedback
//	POST /api/media-memory/batches
//	GET  /api/media-memory/batches/:id
//	POST /api/media-memory/batches/:id/reconcile
//
// godlike/06 SSOT: ALL business logic for mediamemory lives in the
// application-layer siblings (resolver.go, binding_service.go,
// feedback_service.go, batch_service.go). This file owns ONLY:
//   - JSON binding (apiutil.BindJSON)
//   - request → canonical ResolveRequest / BindingSpec / FeedbackInput
//     / BatchSpec translation
//   - response DTO rendering (forward-pointer to dto.go in Phase 1.x)
//   - typed-sentinel → HTTP status mapping (forward-pointer to errors.go)
//
// godlike/07 NO-FAKE-AVAILABILITY: the wire-stub routes return 501
// Not Implemented with a JSON envelope that names the missing
// dependency (no silent 200, no empty body). Production wiring in
// subsequent phases REPLACES the stub closures with real ones via
// the constructor's HandlerFunc ports — no late-binding setters
// (godlike/06 SSOT).
//
// AGENTS.md Pattern 8 ("API package: thin transport only") applies:
// this file MUST NOT import database/sql, an HTTP client, os/exec,
// or any other infra import.
package mediamemory

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/mediamemory"
)

// ── Handler ports (composition-root-narrow) ────────────────────────

// ResolverPort is the narrow request side the handler depends on.
// Production wiring injects *mediamemory.VisualResolver (cast to the
// interface). Skeletons inject a stub closure (see
// Handler.WireParams).
type ResolverPort interface {
	Resolve(c *gin.Context, req mediamemory.ResolveRequest) (mediamemory.ResolveResult, error)
}

// BindingServicePort is the narrow binding-service surface.
type BindingServicePort interface {
	Create(c *gin.Context, b mediamemory.MediaBinding) (mediamemory.MediaBinding, error)
	ListByConcept(c *gin.Context, conceptID string) ([]mediamemory.MediaBinding, error)
	Approve(c *gin.Context, id string) error
	Reject(c *gin.Context, id string) error
	Delete(c *gin.Context, id string) error
}

// FeedbackServicePort is the narrow feedback-service surface.
type FeedbackServicePort interface {
	Record(c *gin.Context, in mediamemory.FeedbackInput) (mediamemory.UsageEvent, error)
}

// BatchServicePort is the narrow batch-service surface.
type BatchServicePort interface {
	CreateBatch(c *gin.Context, spec mediamemory.BatchSpec) (mediamemory.Batch, []mediamemory.BatchChild, error)
	Get(c *gin.Context, batchID string) (mediamemory.Batch, error)
	Reconcile(c *gin.Context, batchID string) (mediamemory.Batch, error)
}

// ── WireParams + Handler ───────────────────────────────────────────

// WireParams bundles every dependency the Handler needs. The
// production wiring injects the canonical mediamemory services;
// skeleton wiring (Phase 1.1) injects closures that return
// 501 Not Implemented.
//
// godlike/06 SSOT (canonical logger seam): the API handler layer
// uses ONLY a *zap.Logger (mirrors internal/api/mediasearch).
// mediamemory.Logger belongs to the application layer; the handler
// does NOT re-export it (no double logger seam).
type WireParams struct {
	Resolver ResolverPort
	Bindings BindingServicePort
	Feedback FeedbackServicePort
	Batches  BatchServicePort
	Log      *zap.Logger
	Clock    mediamemory.Clock
}

// Handler is the thin HTTP transport for the canonical MediaMemory API.
type Handler struct {
	resolver ResolverPort
	bindings BindingServicePort
	feedback FeedbackServicePort
	batches  BatchServicePort
	log      *zap.Logger
	clock    mediamemory.Clock
}

// NewHandler creates the Handler. Composition root wires concrete
// services in Phase 1.2+; Phase 1.1 passes nil for the service ports
// and the handler falls back to the 501 stub closures.
func NewHandler(p WireParams) *Handler {
	clock := p.Clock
	if clock == nil {
		clock = mediamemory.RealClock()
	}
	return &Handler{
		resolver: p.Resolver,
		bindings: p.Bindings,
		feedback: p.Feedback,
		batches:  p.Batches,
		log:      p.Log,
		clock:    clock,
	}
}

// safeError logs without panicking if Logger is nil.
func (h *Handler) safeError(msg string, fields ...zap.Field) {
	if h.log == nil {
		return
	}
	h.log.Error(msg, fields...)
}

// ── RegisterRoutes ────────────────────────────────────────────────

// RegisterRoutes mounts the canonical mediamemory surface under
// /api/media-memory. Authorization + workspace-scope middleware
// MUST be applied upstream (composition root owns that decision,
// parallel to internal/api/mediasearch::RegisterRoutes).
func (h *Handler) RegisterRoutes(r *gin.RouterGroup) {
	g := r.Group("/media-memory")

	g.POST("/resolve", h.Resolve)
	g.POST("/bindings", h.BindingsCreate)
	g.GET("/bindings", h.BindingsList)
	g.POST("/feedback", h.Feedback)
	g.POST("/batches", h.BatchesCreate)
	g.GET("/batches/:id", h.BatchGet)
	g.POST("/batches/:id/reconcile", h.BatchReconcile)
}

// ── Per-route handlers (skeleton; Phase 1.1 returns 501) ──────────

// notImplementedResponse is the canonical wire shape for the 501
// Not Implemented envelope returned by Phase 1.x stub routes.
//
// godlike/06 SSOT (typed DTO): the wire shape is a typed struct
// (not a gin.H map) so the schema is grep-able, exportable, and
// drift-pinnable. A future module tests the JSON marshalling
// contract verbatim.
//
// godlike/07 NO-FAKE-AVAILABILITY: the envelope names the missing
// dependency + phase marker so callers can distinguish "route
// registered but unresolved" from "route doesn't exist".
type notImplementedResponse struct {
	OK        bool   `json:"ok"`
	Route     string `json:"route"`
	Reason    string `json:"reason"`
	Timestamp string `json:"timestamp"`
}

// notImplemented is the canonical handler for routes whose
// application-side dependency hasn't been wired yet.
//
// godlike/06 SSOT (constructor invariant): NewHandler always
// inits h.clock from mediamemory.RealClock(); the nil-guard is
// unnecessary.
func (h *Handler) notImplemented(c *gin.Context, route string) {
	c.JSON(http.StatusNotImplemented, notImplementedResponse{
		OK:        false,
		Route:     route,
		Reason:    "mediamemory route registered but Phase 1.x dependency not yet wired",
		Timestamp: h.clock.Now().UTC().Format(time.RFC3339Nano),
	})
}

// Resolve is the HTTP handler for POST /api/media-memory/resolve.
func (h *Handler) Resolve(c *gin.Context) {
	if h.resolver == nil {
		h.notImplemented(c, "POST /api/media-memory/resolve")
		return
	}
	// Composition root will inject a real resolver. Phase 1.1
	// returns 501 via the hold above; subsequent phases replace
	// the closure with: bind req → h.resolver.Resolve(...) →
	// resultToResponse(...).
}

// BindingsCreate is the HTTP handler for POST /api/media-memory/bindings.
func (h *Handler) BindingsCreate(c *gin.Context) {
	if h.bindings == nil {
		h.notImplemented(c, "POST /api/media-memory/bindings")
		return
	}
}

// BindingsList is the HTTP handler for GET /api/media-memory/bindings.
func (h *Handler) BindingsList(c *gin.Context) {
	if h.bindings == nil {
		h.notImplemented(c, "GET /api/media-memory/bindings")
		return
	}
}

// Feedback is the HTTP handler for POST /api/media-memory/feedback.
func (h *Handler) Feedback(c *gin.Context) {
	if h.feedback == nil {
		h.notImplemented(c, "POST /api/media-memory/feedback")
		return
	}
}

// BatchesCreate is the HTTP handler for POST /api/media-memory/batches.
func (h *Handler) BatchesCreate(c *gin.Context) {
	if h.batches == nil {
		h.notImplemented(c, "POST /api/media-memory/batches")
		return
	}
}

// BatchGet is the HTTP handler for GET /api/media-memory/batches/:id.
func (h *Handler) BatchGet(c *gin.Context) {
	if h.batches == nil {
		h.notImplemented(c, "GET /api/media-memory/batches/:id")
		return
	}
}

// BatchReconcile is the HTTP handler for POST /api/media-memory/batches/:id/reconcile.
func (h *Handler) BatchReconcile(c *gin.Context) {
	if h.batches == nil {
		h.notImplemented(c, "POST /api/media-memory/batches/:id/reconcile")
		return
	}
}
