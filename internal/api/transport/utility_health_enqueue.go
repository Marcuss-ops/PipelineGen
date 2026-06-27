// Package transport — utility_health_enqueue.go (cherry-pick
// restoration, June 2026).
//
// Wave 17/18 refactor deleted these cross-cutting handler widgets
// from `transport/` but left call sites in
// internal/api/routes.go, internal/api/module_core_modules.go,
// internal/app/composition.go, and the 4 voiceover/bulk_upload/
// youtube/sync_drive_folder handler variants referencing them.
//
// Minimal-change restoration per AGENTS.md: this file adds ONLY the
// symbols the call sites actually consume (UtilityHandler + Slugify,
// HealthHandler, NewHealthHandler, EnqueueAsync, EnqueueInput). The
// earlier transport.go pipeline is untouched.
//
// Future PR: surgical migration of these widgets into per-handler
// files (e.g. internal/api/admin/health_handler.go) — out of scope
// here.

package transport

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/system/health"
	job "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	"github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
	"github.com/Marcuss-ops/PipelineGen/pkg/textutil"
)

// ── Async enqueue helper (used by every long-running handler) ──────

// EnqueueInput is the typed payload envelope handed to EnqueueAsync.
// Field set is the closed schema used by the bulk_upload +
// voiceover_indexer + youtube_clip_extract + sync_drive_folder +
// local_to_drive jobs; addition of fields is a typed-coupled drift
// (each job handler enforces its own payload type, but the envelope
// is shared).
type EnqueueInput struct {
	Type          string         `json:"type"`
	Payload       map[string]any `json:"payload"`
	MaxRetries    int            `json:"max_retries,omitempty"`
	CorrelationID string         `json:"correlation_id,omitempty"`
	Project       string         `json:"project,omitempty"`
	ActiveKey     string         `json:"active_key,omitempty"`
}

// EnqueueAsync enqueues a long-running job via the canonical jobs
// service. Returns true on enqueue success. Returns false on jobs
// service missing (HTTP 503) or on enqueue error (HTTP 500 with
// mapped message); on false the gin context already carries the
// response — the caller should `return` immediately.
//
// Mirrors the contract pinned by
// internal/api/assets/voiceover/handler.go (Wave 18 follow-up),
// internal/api/assets/clips/bulk_upload.go (Wave 18),
// internal/api/assets/storage/sync_drive_folder.go + local_to_drive.go
// (Wave 17/18), and internal/api/assets/youtube/youtube_handlers.go.
func EnqueueAsync(c *gin.Context, jobsSvc job.Service, in *EnqueueInput, msg string) bool {
	if jobsSvc == nil {
		apiutil.Error(c, http.StatusServiceUnavailable, "jobs service not configured")
		return false
	}
	req := &job.EnqueueRequest{
		Type:          in.Type,
		Payload:       in.Payload,
		MaxRetries:    in.MaxRetries,
		CorrelationID: in.CorrelationID,
		Project:       in.Project,
		ActiveKey:     in.ActiveKey,
	}
	if _, err := jobsSvc.Enqueue(c.Request.Context(), req); err != nil {
		apiutil.Error(c, http.StatusInternalServerError, msg+": "+err.Error())
		return false
	}
	apiutil.OK(c, gin.H{"ok": true, "message": msg})
	return true
}

// ── UtilityHandler (low-volume / slug / ping endpoints) ──────

// UtilityHandler groups the lower-volume utility endpoints
// (currently a thin struct — routes attach via RegisterRoutes
// pattern). The HealthHandler lives below as a separate type for
// fail-closed wiring clarity (per pg-007 health-route split).
type UtilityHandler struct {
	log *zap.Logger
}

// NewUtilityHandler creates the UtilityHandler. log is required
// (nil-tolerant via zap.NewNop if nil).
func NewUtilityHandler(log *zap.Logger) *UtilityHandler {
	if log == nil {
		log = zap.NewNop()
	}
	return &UtilityHandler{log: log}
}

// Slugify serves GET /internal/slug — used by the UtilityModule
// (internal/api/module_core_modules.go). Reads `?q=` from the
// query string, applies pkg/textutil.Slugify (the canonical
// slugifier, also used by asset folder naming), and returns the
// slug in the response body. Future PR: replace with the
// pkg/textutil.SlugifyWithMax variant if a length cap is needed.
func (u *UtilityHandler) Slugify(c *gin.Context) {
	q := strings.TrimSpace(c.Query("q"))
	if q == "" {
		apiutil.BadRequest(c, "q query parameter is required")
		return
	}
	apiutil.OK(c, gin.H{"slug": textutil.Slugify(q)})
}

// ── HealthHandler (/health + /ready probes) ──────────────────────

// HealthHandler is the canonical /health + /ready probe handler.
// Fail-closed: nil svc/readyChecker → 503; readyChecker error → 503
// with message; success → 200 OK.
//
// Construction wires *health.Service (used by Health) and
// *health.ReadyChecker (used by Ready). Either can be nil — the
// respective endpoint returns 503 in that case (the composition
// root is required to wire both for production deploys).
type HealthHandler struct {
	svc          *health.Service
	readyChecker *health.ReadyChecker
}

// NewHealthHandler builds a HealthHandler. Both args are optional;
// nil-tolerant for test fixtures and partial deploys.
func NewHealthHandler(svc *health.Service, readyChecker *health.ReadyChecker) *HealthHandler {
	return &HealthHandler{svc: svc, readyChecker: readyChecker}
}

// Health returns 200 OK when the health service is wired and the
// underlying service.Check (canonical) passes. Returns 503 otherwise.
func (h *HealthHandler) Health(c *gin.Context) {
	if h.svc == nil {
		apiutil.Error(c, http.StatusServiceUnavailable, "health service not wired")
		return
	}
	if resp := h.svc.Check(c.Request.Context(), nil); !resp.OK {
		apiutil.Error(c, http.StatusServiceUnavailable, "health check failed: "+resp.Status)
		return
	}
	apiutil.OK(c, gin.H{"status": "ok"})
}

// Ready returns 200 OK when the ready checker is wired and
// readyChecker.CheckReady passes. Returns 503 otherwise.
func (h *HealthHandler) Ready(c *gin.Context) {
	if h.readyChecker == nil {
		apiutil.Error(c, http.StatusServiceUnavailable, "ready checker not wired")
		return
	}
	if resp := h.readyChecker.CheckReady(c.Request.Context()); !resp.OK {
		apiutil.Error(c, http.StatusServiceUnavailable, "not ready: "+resp.Status)
		return
	}
	apiutil.OK(c, gin.H{"status": "ready"})
}
