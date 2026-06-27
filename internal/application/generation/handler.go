// Package generation — handler.go: thin HTTP transport for the
// Generation capability.
//
// Capability Standard rule: "Handlers may bind input, validate
// transport syntax, translate to a command/query, invoke one use
// case, map typed errors, and serialize output." This handler does
// only that — no SQL, no jobs.Service access, no Books/Lessons
// sub-service access at runtime. The worker side runs via
// DescriptorJobs in jobs.go and is wired by the composition root.
package generation

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/generation"
	"github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// Handler exposes the unified generation API.
type Handler struct {
	svc handlerService
	log *zap.Logger
}

// NewHandler constructs a new generation handler. Accepts the
// handlerService interface (Create/Status/Cancel) instead of the
// concrete *Service so test stubs (e.g. handler_test.go's
// *fakeService) satisfy the same shape at compile time. The canonical
// *Service satisfies handlerService too — see the var _ assertion at
// the bottom of this file.
func NewHandler(svc handlerService, log *zap.Logger) *Handler {
	if svc == nil {
		panic("generation.NewHandler: Service is required")
	}
	if log == nil {
		log = zap.NewNop()
	}
	return &Handler{svc: svc, log: log}
}

// RegisterRoutes mounts the unified generation routes.
func (h *Handler) RegisterRoutes(r *gin.RouterGroup) {
	r.POST("", h.Create)
	r.GET("/:id", h.Get)
	r.POST("/:id/cancel", h.Cancel)
}

// createRequest is the JSON body for POST /api/generations. The
// handler binds transport syntax then translates to
// generation.CreateCommand — never constructs domain objects.
type createRequest struct {
	SchemaVersion int             `json:"schema_version"`
	Type          generation.Type `json:"type"`
	Input         json.RawMessage `json:"input"`
	Options       map[string]any  `json:"options,omitempty"`
}

// Create enqueues a generation job and returns the canonical
// envelope (id + status + status_url). Errors map to typed status
// codes via writeErr.
func (h *Handler) Create(c *gin.Context) {
	if h.svc == nil {
		apiutil.Error(c, http.StatusServiceUnavailable, "generation service not initialized")
		return
	}
	var req createRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiutil.BadRequest(c, err.Error())
		return
	}
	out, err := h.svc.Create(c.Request.Context(), generation.Request{
		SchemaVersion: req.SchemaVersion,
		Type:          req.Type,
		Input:         req.Input,
		Options:       req.Options,
	})
	if err != nil {
		h.writeErr(c, err)
		return
	}
	apiutil.Accepted(c, toCreateResult(out))
}

// Get returns the current job status.
func (h *Handler) Get(c *gin.Context) {
	if h.svc == nil {
		apiutil.Error(c, http.StatusServiceUnavailable, "generation service not initialized")
		return
	}
	out, err := h.svc.Status(c.Request.Context(), strings.TrimSpace(c.Param("id")))
	if err != nil {
		h.writeErr(c, err)
		return
	}
	apiutil.OK(c, toStatusResult(out))
}

// Cancel cancels a generation job.
func (h *Handler) Cancel(c *gin.Context) {
	if h.svc == nil {
		apiutil.Error(c, http.StatusServiceUnavailable, "generation service not initialized")
		return
	}
	if err := h.svc.Cancel(c.Request.Context(), strings.TrimSpace(c.Param("id"))); err != nil {
		h.writeErr(c, err)
		return
	}
	apiutil.OK(c, CancelResult{OK: true})
}

// writeErr maps capability-defined errors to HTTP status codes.
// Anything not in the typed set falls through to 500 InternalError.
func (h *Handler) writeErr(c *gin.Context, err error) {
	switch {
	case errors.Is(err, ErrUnsupportedType):
		apiutil.BadRequest(c, err.Error())
	case errors.Is(err, ErrTypeDisabled):
		apiutil.Error(c, http.StatusServiceUnavailable, err.Error())
	case errors.Is(err, ErrJobNotFound):
		apiutil.NotFound(c, err.Error())
	default:
		if h.log != nil {
			h.log.Error("generation request failed", zap.Error(err))
		}
		apiutil.InternalError(c, err)
	}
}

// ── Result translators ────────────────────────────────────────

// toCreateResult converts the existing Service response shape
// (kept for backwards compatibility with internal callers) into
// the canonical contract CreateResult. The shape is identical at
// the wire level so this is a struct-equality projection.
func toCreateResult(in *CreateResponse) CreateResult {
	return CreateResult{
		OK: in.OK,
		Job: JobRef{
			ID:        in.Job.ID,
			Type:      in.Job.Type,
			Status:    in.Job.Status,
			StatusURL: in.Job.StatusURL,
		},
	}
}

// toStatusResult converts the existing Service StatusResponse into
// the canonical contract StatusResult. Field-for-field projection;
// the source struct already carries the JSON tags we want.
func toStatusResult(in *StatusResponse) StatusResult {
	if in == nil {
		return StatusResult{}
	}
	return StatusResult{
		OK:  in.OK,
		Job: fromJobStatus(in.Job),
	}
}

func fromJobStatus(j JobStatus) JobState {
	return JobState{
		ID:          j.ID,
		Type:        j.Type,
		Status:      j.Status,
		Progress:    j.Progress,
		Phase:       j.Phase,
		Message:     j.Message,
		CreatedAt:   j.CreatedAt,
		StartedAt:   j.StartedAt,
		CompletedAt: j.CompletedAt,
		Result:      j.Result,
		Error:       j.Error,
	}
}

// ── Service interface (declared here for the handler) ─────────
// Handlers consume only the three use cases they expose; the
// concrete Service in service.go is the only implementation.

type handlerService interface {
	Create(ctx context.Context, req generation.Request) (*CreateResponse, error)
	Status(ctx context.Context, id string) (*StatusResponse, error)
	Cancel(ctx context.Context, id string) error
}

// Compile-time checks ensure that the canonical Service and the
// declared handlerService interface stay aligned. Drift is a build
// error, not a runtime nil-deref.
var _ handlerService = (*Service)(nil)
