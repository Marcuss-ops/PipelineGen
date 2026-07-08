// Package scriptdocs — handler.go: the canonical ReAct typed port
// (godlike/06 SSOT one-canonical-owner-per-fact) + the slim
// HTTP handler that mounts POST /api/script-docs/generate.
//
// PR-SCRIPT-DOCS-DRIFT-2026-07-08 closure: this package closes the
// drift notice in AGENTS.md by shipping the canonical typed port +
// route surface. The route is gated on cfg.Features.ScriptDocsEnabled
// (per the canonical capability standard — the route module's
// EnabledFunc is the canonical gate; nil-port at composition time
// returns 503, not 500, to distinguish "not wired" from "wired but
// broken").
//
// godlike/07 NO-FAKE-AVAILABILITY contract:
//   - nil port → 503 (composition root did not wire ReActPort)
//   - port call returns error → 500 (typed error, no silent success)
//   - port call returns ReActResponse → 200 (canonical response shape)
//
// The Python ReAct agent surface (scripts/bridges/reAct_agent.py
// or similar) is the future CUTOVER consumer. Today, the canonical
// ReActPort has zero production-side concrete implementations
// (composition root passes nil) — every /api/script-docs/generate
// request returns 503 + a clear "not yet wired" diagnostic. This
// matches the canonical pre-fail-closed posture for optional modules
// (see the voiceover pattern at internal/application/voiceover/).
package scriptdocs

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// ReActPort is the canonical Pattern 0 typed port for the ReAct
// agent surface (godlike/06 SSOT one-canonical-owner-per-fact).
//
// The Python ReAct agent path is the canonical future consumer;
// composition-root wiring is the SOLE owner of concrete implementations.
// Today's production composition passes nil — the handler's
// nil-port guard returns 503, which is the canonical pre-fail-closed
// posture for optional modules.
//
// The port surface is intentionally narrow (Generate only). The
// ReAct agent's reasoning/acting loop is opaque to the api/ layer —
// it lives in the application/ port adapter, NOT in handler.go.
// godlike/07 minimum-blast-radius: zero new dependencies on the
// Python bridge (composition root wires that; this package stays
// free of scripts/bridges imports).
type ReActPort interface {
	// Generate is the canonical ReAct agent entrypoint. ctx carries
	// the request-scoped lifetime (the handler passes c.Request.Context()
	// through); the implementation MUST honor ctx.Done().
	//
	// req is the canonical typed request shape (topic + initial state).
	// Returns ReActResponse on success or a typed error on failure.
	// The handler maps typed errors to 500 (NOT 503 — nil-port
	// is "not wired"; port-error is "wired but broken").
	Generate(ctx context.Context, req ReActRequest) (ReActResponse, error)
}

// ReActRequest is the canonical typed request shape for the ReAct
// agent surface. The fields are additive (json:omitempty) per
// godlike/07 minimum-blast-radius — a future CUTOVER that adds
// new ReAct parameters doesn't break existing clients.
type ReActRequest struct {
	// Topic is the canonical ReAct topic (the user-facing prompt).
	// Required; empty → handler returns 400.
	Topic string `json:"topic"`
	// Context is the optional initial reasoning context (free-form).
	Context string `json:"context,omitempty"`
	// MaxSteps bounds the ReAct loop iterations (0 → server default).
	MaxSteps int `json:"max_steps,omitempty"`
}

// ReActResponse is the canonical typed response shape. The fields
// are additive (json:omitempty) per godlike/07 minimum-blast-radius.
// Status is the canonical high-level state: "ok" / "partial" / "error"
// (caller's responsibility to set the appropriate value).
type ReActResponse struct {
	// Result is the canonical ReAct agent's final answer (the
	// synthesize step's output).
	Result string `json:"result"`
	// Status is the canonical high-level state. "ok" on success,
	// "partial" on partial success, "error" on graceful failure
	// (the typed error still bubbles up to the handler for 500).
	Status string `json:"status"`
	// StepsTaken is the canonical count of ReAct loop iterations
	// actually performed (observability surface).
	StepsTaken int `json:"steps_taken,omitempty"`
}

// ErrReActNotWired is the canonical godlike/07 typed sentinel for
// the nil-port diagnostic. Composition root that hasn't wired
// ReActPort yet is the canonical producer; the handler reads it
// via errors.Is and returns 503 + the canonical diagnostic message.
var ErrReActNotWired = &reActNotWiredError{}

// reActNotWiredError is the canonical error type for ErrReActNotWired.
// Implements error + Is(target error) bool for errors.Is probing
// per godlike/07 typed-error contract.
type reActNotWiredError struct{}

// Error implements the error interface.
func (e *reActNotWiredError) Error() string {
	return "script-docs: ReAct port is not wired at the composition root (set features.script_docs_enabled=false to silence the route, or wire an ReActPort implementation to enable)"
}

// Is implements the errors.Is target matching (canonical Pattern).
func (e *reActNotWiredError) Is(target error) bool {
	_, ok := target.(*reActNotWiredError)
	return ok
}

// Handler is the slim HTTP handler for /api/script-docs/* routes.
// godlike/06 SSOT: the Handler is the SOLE owner of the route surface
// + the typed-error mapping. The ReActPort is the SOLE owner of the
// agent-execution surface (Pattern 0).
type Handler struct {
	port ReActPort
	log  *zap.Logger
}

// NewHandler constructs the canonical Handler. port may be nil —
// the Generate route's nil-port guard returns 503 with the
// canonical ErrReActNotWired diagnostic (godlike/07 fail-closed
// at the seam, NOT a panic). log nil → zap.NewNop().
func NewHandler(port ReActPort, log *zap.Logger) *Handler {
	if log == nil {
		log = zap.NewNop()
	}
	return &Handler{port: port, log: log}
}

// RegisterRoutes mounts POST /api/script-docs/generate. The
// pre-existing AGENTS.md drift notice (PR-SCRIPT-DOCS-DRIFT-2026-07-08)
// is closed by this registration — operators hitting the canonical
// endpoint today get 503 (not 404) with a clear diagnostic.
//
// Future endpoints (e.g. POST /api/script-docs/reset,
// GET /api/script-docs/state) land here in lockstep with the
// ReActPort surface extensions.
func (h *Handler) RegisterRoutes(rg *gin.RouterGroup) {
	rg.POST("/generate", h.Generate)
}

// Generate is the canonical POST /api/script-docs/generate handler.
//
// godlike/07 fail-closed mapping:
//   - nil port → 503 (composition root did not wire ReActPort)
//   - topic empty → 400 (request validation)
//   - port call returns error → 500 (typed error passthrough)
//   - happy path → 200 (canonical ReActResponse shape)
//
// godlike/07 NO-FAKE-AVAILABILITY: the handler NEVER fabricates a
// success response. If the port is nil or returns an error, the
// caller sees the typed failure, NOT a 200 with empty Result.
func (h *Handler) Generate(c *gin.Context) {
	// Fail-closed at the seam: composition root that hasn't
	// wired ReActPort yet returns 503 with the canonical diagnostic.
	if h.port == nil {
		h.log.Debug("script-docs: ReAct port is nil — returning 503 ErrReActNotWired")
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"error":   "service_unavailable",
			"message": ErrReActNotWired.Error(),
		})
		return
	}

	// Parse + validate the request body.
	var req ReActRequest
	if bindErr := c.ShouldBindJSON(&req); bindErr != nil {
		h.log.Debug("script-docs: bind JSON failed", zap.Error(bindErr))
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "bad_request",
			"message": "request body must be valid JSON with a 'topic' field",
		})
		return
	}
	if req.Topic == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "bad_request",
			"message": "topic is required",
		})
		return
	}

	// Dispatch to the typed port. Context is request-scoped — the
	// port implementation MUST honor ctx.Done() (godlike/07).
	resp, portErr := h.port.Generate(c.Request.Context(), req)
	if portErr != nil {
		h.log.Warn("script-docs: ReAct port returned error",
			zap.String("topic", req.Topic),
			zap.Error(portErr))
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "internal_error",
			"message": portErr.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, resp)
}

// Compile-time assertion: the Handler implements the implicit
// gin.HandlerFunc-RegisterRoutes contract via the api.NewRouteModule
// caller in module.go's Build function. No separate sentinel needed
// because the contract is structural (RegisterRoutes(rg *gin.RouterGroup)).
