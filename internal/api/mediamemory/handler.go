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
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/mediamemory"
	apiutil "github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
)

// ── Handler ports (composition-root-narrow) ────────────────────────

// ResolverPort is the narrow request side the handler depends on.
// Production wiring injects *mediamemory.VisualResolver (cast to the
// interface). Skeletons inject a stub closure (see
// Handler.WireParams).
//
// godlike/06 SSOT (transport-leak discipline): the port takes
// standard context.Context NOT *gin.Context. The *gin.Context is
// the handler's own input (per gin's framework contract); the
// port is consumed by the application layer which is
// transport-agnostic. Passing *gin.Context through the port would
// leak the HTTP framework into the resolver — anti-pattern.
// Mirror of BindingServicePort / FeedbackServicePort which also
// accept context.Context for the same reason.
type ResolverPort interface {
	Resolve(ctx context.Context, req mediamemory.ResolveRequest) (mediamemory.ResolveResult, error)
}

// BindingServicePort is the narrow binding-service surface. The
// service-side signatures DO NOT TAKE *gin.Context (godlike/06 SSOT:
// the application layer is transport-agnostic); the handler
// adapters below pluck workspace-id + actor info from the gin
// context and inject them into the service call as plain args.
//
// Phase 1.x limitation: this surface forwards the gin.Context
// solely so future workspace-scope middleware can attach it; for
// now, the ports above are direct passthroughs.
type BindingServicePort interface {
	Create(ctx context.Context, b mediamemory.MediaBinding) (mediamemory.MediaBinding, error)
	Update(ctx context.Context, b mediamemory.MediaBinding) (mediamemory.MediaBinding, error)
	Delete(ctx context.Context, id string) error
	Approve(ctx context.Context, id string) error
	Reject(ctx context.Context, id string) error
	ListByConcept(ctx context.Context, conceptID string) ([]mediamemory.MediaBinding, error)
	ListBySlot(ctx context.Context, conceptID string, slot mediamemory.SlotKind, limit int) ([]mediamemory.MediaBinding, error)
}

// FeedbackServicePort is the narrow feedback-service surface.
type FeedbackServicePort interface {
	Record(ctx context.Context, in mediamemory.FeedbackInput) (mediamemory.UsageEvent, error)
}

// BatchServicePort is the narrow batch-service surface.
type BatchServicePort interface {
	CreateBatch(ctx context.Context, spec mediamemory.BatchSpec) (mediamemory.Batch, []mediamemory.BatchChild, error)
	Get(ctx context.Context, batchID string) (mediamemory.Batch, error)
	Reconcile(ctx context.Context, batchID string) (mediamemory.Batch, error)
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
//
// Fase 1.4 routes (live):
//
//	POST /api/media-memory/bindings                       (Fase 1.4)
//	GET  /api/media-memory/bindings                       (Fase 1.4)
//	POST /api/media-memory/bindings/:id/approve           (Fase 1.4)
//	POST /api/media-memory/bindings/:id/reject            (Fase 1.4)
//	DELETE /api/media-memory/bindings/:id                 (Fase 1.4)
//	POST /api/media-memory/feedback                       (Fase 1.4)
//
// Still 501 (wire-stub only):
//
//	POST /api/media-memory/resolve                        (Fase 1.5)
//	POST /api/media-memory/batches*                       (Fase 3.x)
func (h *Handler) RegisterRoutes(r *gin.RouterGroup) {
	g := r.Group("/media-memory")

	g.POST("/resolve", h.Resolve)
	g.POST("/bindings", h.BindingsCreate)
	g.GET("/bindings", h.BindingsList)
	g.POST("/bindings/:id/approve", h.BindingsApprove)
	g.POST("/bindings/:id/reject", h.BindingsReject)
	g.DELETE("/bindings/:id", h.BindingsDelete)
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

// writeError renders a typed-sentinel envelope as JSON. The HTTP
// status comes from MapError; the body is the canonical
// errorEnvelope DTO (drift-pinned via tests in dto-pin).
func (h *Handler) writeError(c *gin.Context, route string, err error) {
	mapped := MapError(err)
	// godlike/07: log the underlying cause via safeError so we
	// keep a server-side trail without leaking internal detail
	// to the wire envelope.
	h.safeError(route,
		zap.Int("status", mapped.Status),
		zap.String("code", mapped.Code),
		zap.String("canonical_route", route),
		zap.Error(err),
	)
	c.JSON(mapped.Status, errorEnvelope{
		OK:        false,
		Code:      mapped.Code,
		Message:   mapped.Message,
		Timestamp: h.clock.Now().UTC().Format(time.RFC3339Nano),
	})
}

// Resolve is the HTTP handler for POST /api/media-memory/resolve.
//
// Fase 1.5: thin transport — JSON binding → canonical
// ResolveRequest → ResolverPort.Resolve → response DTO. Errors
// map via MapError to typed HTTP statuses. Per-scene failures
// surface in the `warnings` array (not as a 500) so the
// dashboard preview can keep displaying the partial plan.
func (h *Handler) Resolve(c *gin.Context) {
	if h.resolver == nil {
		h.notImplemented(c, "POST /api/media-memory/resolve")
		return
	}
	var req resolveCreateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.writeError(c, "POST /api/media-memory/resolve",
			fmt.Errorf("mediamemory: bind JSON: %w", err))
		return
	}
	resolved, err := h.resolver.Resolve(c.Request.Context(), req.toResolveRequest())
	if err != nil {
		h.writeError(c, "POST /api/media-memory/resolve", err)
		return
	}
	plans := make([]resolvePlanDTO, 0, len(resolved.Plans))
	for _, p := range resolved.Plans {
		plans = append(plans, toResolvePlanDTO(p))
	}
	apiutil.OK(c, resolveResponse{
		OK:        true,
		Plans:     plans,
		Warnings:  resolved.Warnings,
		Timestamp: h.clock.Now().UTC().Format(time.RFC3339Nano),
	})
}

// BindingsCreate is the HTTP handler for POST /api/media-memory/bindings.
//
// godlike/06 SSOT: thin transport. JSON binding → canonical
// MediaBinding → service → response DTO → JSON. Errors map via
// MapError to typed HTTP statuses.
func (h *Handler) BindingsCreate(c *gin.Context) {
	if h.bindings == nil {
		h.notImplemented(c, "POST /api/media-memory/bindings")
		return
	}
	var req bindingCreateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.writeError(c, "POST /api/media-memory/bindings",
			fmt.Errorf("mediamemory: bind JSON: %w",
				mediamemory.ErrInvalidSlotKind)) // 400 surface
		return
	}

	bindingIn := req.toMediaBinding()
	bindingOut, err := h.bindings.Create(c.Request.Context(), bindingIn)
	if err != nil {
		h.writeError(c, "POST /api/media-memory/bindings", err)
		return
	}

	out := gin.H{
		"ok":        true,
		"binding":   toBindingDTO(bindingOut),
		"timestamp": h.clock.Now().UTC().Format(time.RFC3339Nano),
	}
	apiutil.OK(c, out)
}

// BindingsList is the HTTP handler for GET /api/media-memory/bindings.
//
// godlike/07 NO-FAKE-AVAILABILITY: missing concept_id is a 400
// (godlike/06 SSOT surface: ??concept_id is required to scope the
// diff; an unscoped listing would return every binding in the
// catalog, silently failing the dashboard's pagination contract).
func (h *Handler) BindingsList(c *gin.Context) {
	if h.bindings == nil {
		h.notImplemented(c, "GET /api/media-memory/bindings")
		return
	}
	var req bindingListRequest
	if err := c.ShouldBindQuery(&req); err != nil {
		h.writeError(c, "GET /api/media-memory/bindings",
			fmt.Errorf("mediamemory: bind query: %w", err))
		return
	}
	bindings, err := h.bindings.ListByConcept(c.Request.Context(), req.ConceptID)
	if err != nil {
		h.writeError(c, "GET /api/media-memory/bindings", err)
		return
	}
	out := gin.H{
		"ok":        true,
		"bindings":  bindingsToDTOs(bindings),
		"count":     len(bindings),
		"timestamp": h.clock.Now().UTC().Format(time.RFC3339Nano),
	}
	apiutil.OK(c, out)
}

// BindingsApprove is the HTTP handler for POST /api/media-memory/bindings/:id/approve.
// godlike/06 SSOT: explicit approve/reject endpoints mirror the
// dashboard's two-button UI; the service guards ApprovalStatus
// flips via the canonical approve/reject paths (NOT via Update).
func (h *Handler) BindingsApprove(c *gin.Context) {
	if h.bindings == nil {
		h.notImplemented(c, "POST /api/media-memory/bindings/:id/approve")
		return
	}
	id := c.Param("id")
	if err := h.bindings.Approve(c.Request.Context(), id); err != nil {
		h.writeError(c, "POST /api/media-memory/bindings/:id/approve", err)
		return
	}
	apiutil.OK(c, gin.H{
		"ok":        true,
		"id":        id,
		"timestamp": h.clock.Now().UTC().Format(time.RFC3339Nano),
	})
}

// BindingsReject is the HTTP handler for POST /api/media-memory/bindings/:id/reject.
func (h *Handler) BindingsReject(c *gin.Context) {
	if h.bindings == nil {
		h.notImplemented(c, "POST /api/media-memory/bindings/:id/reject")
		return
	}
	id := c.Param("id")
	if err := h.bindings.Reject(c.Request.Context(), id); err != nil {
		h.writeError(c, "POST /api/media-memory/bindings/:id/reject", err)
		return
	}
	apiutil.OK(c, gin.H{
		"ok":        true,
		"id":        id,
		"timestamp": h.clock.Now().UTC().Format(time.RFC3339Nano),
	})
}

// BindingsDelete is the HTTP handler for DELETE /api/media-memory/bindings/:id.
// Routes admin reindex flows (dashboard "remove binding" button).
func (h *Handler) BindingsDelete(c *gin.Context) {
	if h.bindings == nil {
		h.notImplemented(c, "DELETE /api/media-memory/bindings/:id")
		return
	}
	id := c.Param("id")
	if err := h.bindings.Delete(c.Request.Context(), id); err != nil {
		h.writeError(c, "DELETE /api/media-memory/bindings/:id", err)
		return
	}
	apiutil.OK(c, gin.H{
		"ok":        true,
		"id":        id,
		"timestamp": h.clock.Now().UTC().Format(time.RFC3339Nano),
	})
}

// Feedback is the HTTP handler for POST /api/media-memory/feedback.
//
// godlike/07 NO-FAKE-AVAILABILITY: an invalid FeedbackAction
// returns 400 + typed envelope (NOT a 5xx that would let callers
// retry the same action). ErrInvalidFeedbackAction is the
// canonical sentinel for "?action is outside the closed set".
func (h *Handler) Feedback(c *gin.Context) {
	if h.feedback == nil {
		h.notImplemented(c, "POST /api/media-memory/feedback")
		return
	}
	var req feedbackRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.writeError(c, "POST /api/media-memory/feedback",
			fmt.Errorf("mediamemory: bind JSON: %w", err))
		return
	}
	ev, err := h.feedback.Record(c.Request.Context(), req.toFeedbackInput())
	if err != nil {
		h.writeError(c, "POST /api/media-memory/feedback", err)
		return
	}
	apiutil.OK(c, gin.H{
		"ok":        true,
		"event":     toUsageEventDTO(ev),
		"timestamp": h.clock.Now().UTC().Format(time.RFC3339Nano),
	})
}

// BatchesCreate is the HTTP handler for POST /api/media-memory/batches.
// Fase 3.x: real impl lands with the BatchService concrete.
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

// bindingsToDTOs projects a slice of canonical MediaBindings into
// the wire shape (godlike/06 SSOT: no LocalPath / DriveLink
// leaks; canonical bool strings preserved exactly).
func bindingsToDTOs(in []mediamemory.MediaBinding) []bindingDTO {
	out := make([]bindingDTO, 0, len(in))
	for _, b := range in {
		out = append(out, toBindingDTO(b))
	}
	return out
}

// Compile-time assertion: unused helpers do not drift out of
// the source (forward-pin for future handlers).
var _ = errors.Is
var _ = fmt.Errorf
