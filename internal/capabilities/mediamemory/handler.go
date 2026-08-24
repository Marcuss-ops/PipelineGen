// Package mediamemory (api) — handler.go is the thin HTTP transport
// for the canonical MediaMemory API surface (godlike/06 SSOT: thin
// transport only, business logic lives in
// internal/capabilities/mediamemory).
//
// Routes mounted under /api/media-memory/* (mirrors the architecture
// doc, "API consigliate"):
//
//	POST /api/media-memory/resolve
//	POST /api/media-memory/bindings
//	GET  /api/media-memory/bindings
//
// godlike/06 SSOT: this file owns only JSON binding, canonical request
// translation, response DTO rendering, and typed-sentinel HTTP mapping.
// Business logic remains in the application-layer resolver and binding
// services. Missing live service dependencies fail closed with 501; no
// future capability route is registered as a placeholder.

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

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
	apiutil "github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
)

// ── Handler ports (composition-root-narrow) ────────────────────────

// ResolverPort is the narrow request side the handler depends on.
// Production wiring injects *VisualResolver (cast to the
// interface). Skeletons inject a stub closure (see
// Handler.WireParams).
//
// godlike/06 SSOT (transport-leak discipline): the port takes
// standard context.Context NOT *gin.Context. The *gin.Context is
// the handler's own input (per gin's framework contract); the
// port is consumed by the application layer which is
// transport-agnostic. Passing *gin.Context through the port would
// leak the HTTP framework into the resolver — anti-pattern.
// The binding port accepts context.Context for the same reason.
type ResolverPort interface {
	Resolve(ctx context.Context, req ResolveRequest) (ResolveResult, error)
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
	Create(ctx context.Context, b MediaBinding) (MediaBinding, error)
	Update(ctx context.Context, b MediaBinding) (MediaBinding, error)
	Delete(ctx context.Context, id string) error
	Approve(ctx context.Context, id string) error
	Reject(ctx context.Context, id string) error
	ListByConcept(ctx context.Context, conceptID string) ([]MediaBinding, error)
	ListBySlot(ctx context.Context, conceptID string, slot media.SlotKind, limit int) ([]MediaBinding, error)
}

// ── WireParams + Handler ───────────────────────────────────────────

// WireParams bundles every dependency the Handler needs. Production
// wiring injects the canonical MediaMemory services. A missing live
// service is reported as 501 by its route handler.
//
// godlike/06 SSOT (canonical logger seam): the API handler layer
// uses ONLY a *zap.Logger (mirrors internal/api/mediasearch).
// Logger belongs to the application layer; the handler
// does NOT re-export it (no double logger seam).
type WireParams struct {
	Resolver       ResolverPort
	PolicyResolver ResolutionPolicyResolver
	Bindings       BindingServicePort
	Log            *zap.Logger
	Clock          Clock
}

// Handler is the thin HTTP transport for the canonical MediaMemory API.
type Handler struct {
	resolver       ResolverPort
	policyResolver ResolutionPolicyResolver
	bindings       BindingServicePort
	log            *zap.Logger
	clock          Clock
}

// NewHandler creates the Handler. The composition root wires the
// concrete services; a nil live service is handled as unavailable by
// the corresponding route.
func NewHandler(p WireParams) *Handler {
	clock := p.Clock
	if clock == nil {
		clock = RealClock()
	}
	policyResolver := p.PolicyResolver
	if policyResolver == nil {
		policyResolver = NewResolutionPolicyResolver()
	}
	return &Handler{
		resolver:       p.Resolver,
		policyResolver: policyResolver,
		bindings:       p.Bindings,
		log:            p.Log,
		clock:          clock,
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
// Live routes:
//
//	POST /api/media-memory/bindings                       (Fase 1.4)
//	GET  /api/media-memory/bindings                       (Fase 1.4)
//	POST /api/media-memory/bindings/:id/approve           (Fase 1.4)
//	POST /api/media-memory/bindings/:id/reject            (Fase 1.4)
//	DELETE /api/media-memory/bindings/:id                 (Fase 1.4)
func (h *Handler) RegisterRoutes(r *gin.RouterGroup) {
	g := r.Group("/media-memory")

	g.POST("/resolve", h.Resolve)
	g.POST("/bindings", h.BindingsCreate)
	g.GET("/bindings", h.BindingsList)
	g.POST("/bindings/:id/approve", h.BindingsApprove)
	g.POST("/bindings/:id/reject", h.BindingsReject)
	g.DELETE("/bindings/:id", h.BindingsDelete)
}

// ── Per-route handlers ─────────────────────────────────────────────

// notImplementedResponse is the canonical wire shape for the 501
// Not Implemented envelope returned when a live route dependency is
// unavailable.
//
// godlike/06 SSOT (typed DTO): the wire shape is a typed struct,
// rather than a map, so the schema is grep-able and drift-pinnable.
//
// godlike/07 NO-FAKE-AVAILABILITY: the envelope names the missing
// dependency so callers can distinguish an unavailable live service
// from an unknown route.
type notImplementedResponse struct {
	OK        bool   `json:"ok"`
	Route     string `json:"route"`
	Reason    string `json:"reason"`
	Timestamp string `json:"timestamp"`
}

// notImplemented is the canonical handler for a live route whose
// application-side dependency is unavailable.
//
// godlike/06 SSOT (constructor invariant): NewHandler always
// inits h.clock from RealClock(); the nil-guard is
// unnecessary.
func (h *Handler) notImplemented(c *gin.Context, route string) {
	c.JSON(http.StatusNotImplemented, notImplementedResponse{
		OK:        false,
		Route:     route,
		Reason:    "mediamemory live route dependency unavailable",
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
	policy := h.policyResolver.Resolve(req.Language, req.toOptionalPolicy())
	resolved, err := h.resolver.Resolve(c.Request.Context(), req.toResolveRequest(policy))
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
				ErrInvalidSlotKind)) // 400 surface
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

// bindingsToDTOs projects a slice of canonical MediaBindings into
// the wire shape (godlike/06 SSOT: no LocalPath / DriveLink
// leaks; canonical bool strings preserved exactly).
func bindingsToDTOs(in []MediaBinding) []bindingDTO {
	out := make([]bindingDTO, 0, len(in))
	for _, b := range in {
		out = append(out, toBindingDTO(b))
	}
	return out
}

// Compile-time assertion: keep the standard error helpers linked into
// this transport package while the route handlers remain split by file.
var _ = errors.Is
var _ = fmt.Errorf
