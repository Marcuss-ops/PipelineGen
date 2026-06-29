// Package voiceover provides thin HTTP handlers for voiceover operations.
//
// Slim canonical surface (Blocco 4 EXPAND, June 2026): only
// POST /api/voiceover/generate. The canonical wire-format is the
// typed GenerateVoiceoversCommand (internal/application/voiceover/command.go):
//   - async-only via the voiceover.generate job type (introduced in
//     Blocco 4 EXPAND). The /generate handler enqueues a job and
//     returns 202 Accepted with the canonical body shape
//     { "ok": true, "job_id": "...", "status": "queued", "total_outputs": N }.
//   - the legacy /generate-with-group /batch /promo /sync /groups
//     routes are removed from RegisterRoutes at this commit (their
//     removal is part of the CUTOVER step — godlike/07 EXPAND/BACKFILL/
//     CUTOVER/CONTRACT migration sequence).
//
// BACKFILL window (PR-VO-C1, June 2026): the Sunset infrastructure
// — voiceoverSunsetDate, legacyVoiceoverRouteInvocationsTotal,
// addVoiceoverDeprecationHeader, LegacyVoiceoverDeprecationCount —
// stays defined in this file even though NO route calls the helper
// at this commit. handler_pr_vo_c1_test.go pins all four artefacts
// against future drift; removal of any of them flips the test red.
// Future BACKFILL commits can re-register /generate-with-group via
// addVoiceoverDeprecationHeader with these constants already wired.
package voiceover

import (
	"github.com/gin-gonic/gin"
	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/voiceover"
	jobservice "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	apiutil "github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
)

// voiceoverSunsetDate (PR-VO-C1, June 2026) is the IETF IMF-fixdate
// RFC 8594 Sunset header value for the /generate-with-group endpoint.
// Hard-coded constant (matches the same convention as
// handler_legacy_adapters.go::removalDateFromClips / etc.) so deploy
// operators can grep the codebase for "2026-09-26" and find every
// surface that mentions it. The 90-day grace window is intentional —
// it matches the migration window documented at
// docs/voiceover/p0-bundle-A1-A6.md §"Deprecation contract (90-day
// Sunset, RFC 8594)".
const voiceoverSunsetDate = "Sat, 26 Sep 2026 00:00:00 GMT"

// legacyVoiceoverRouteInvocationsTotal is the per-route Prometheus
// counter that tracks how many times the deprecated
// /generate-with-group endpoint has been invoked since process start.
// Operators expose this via /metrics or admin dashboards to identify
// clients that haven't migrated to the canonical /generate endpoint
// with destination.kind="group". Mirrors the
// handler_legacy_adapters.go::legacyRouteInvocationsTotal pattern
// (Wave 21 Wave 25 + PR-VO-C1, June 2026).
var legacyVoiceoverRouteInvocationsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
	Name: "legacy_voiceover_route_invocations_total",
	Help: "Monotonic counter for deprecated voiceover routes, labelled by route name.",
}, []string{"route"})

// LegacyVoiceoverDeprecationCount returns the cumulative invocation
// count across the deprecated voiceover routes by reading the
// legacyVoiceoverRouteInvocationsTotal prometheus counter (dto.Metric
// writeback pattern). Exposed for admin/diagnostic surfaces so the
// PR-VO-C1 sunset deadline can be tracked against live usage.
func LegacyVoiceoverDeprecationCount() int64 {
	var total int64
	for _, route := range []string{"generate-with-group"} {
		counter, err := legacyVoiceoverRouteInvocationsTotal.GetMetricWithLabelValues(route)
		if err != nil {
			continue
		}
		var m dto.Metric
		if err := counter.Write(&m); err != nil {
			continue
		}
		total += int64(m.GetCounter().GetValue())
	}
	return total
}

// addVoiceoverDeprecationHeader injects the standard deprecation
// headers on the deprecated /generate-with-group endpoint at every
// invocation (RFC 9745 draft "Deprecation" header + RFC 8594 "Sunset"
// header + RFC 8288 Web Linking "successor-version" Link header).
//
// This is PR-VO-C1's canonical pattern: the proprietary
// X-Deprecation / X-Deprecation-Notice headers used at
// handler_legacy_adapters.go are a legacy Wave 21 convention; new
// deprecations MUST use the IETF standard form so dashboards can be
// authored against one consistent cross-language contract.
//
// BACKFILL behaviour: at this commit NO route calls this helper, but
// it remains defined for future BACKFILL commits that re-register
// the deprecated /generate-with-group endpoint per godlike/07
// §"Migration sequence" (BACKFILL after Sunset deadline).
func addVoiceoverDeprecationHeader(c *gin.Context, route string) {
	legacyVoiceoverRouteInvocationsTotal.WithLabelValues(route).Inc()
	c.Header("Deprecation", "true")
	c.Header("Sunset", voiceoverSunsetDate)
	c.Header("Link",
		`</api/voiceover/generate>; rel="successor-version"`,
	)
}

// Handler is the slim canonical handler for the voiceover surface.
//
// Blocco 4 EXPAND (June 2026): the Handler struct holds ONLY the
// dependencies required by the canonical /generate flow — the
// jobs.Dispatcher (for enqueuing the voiceover.generate job) and a
// logger. The legacy service/syncService/groupsResolver/defaultVoiceoverRoot
// fields were removed because:
//   - Execution is async via the job queue; no in-process Service
//     shortcut is reachable from this handler.
//   - Sync (Drive-backed voiceover reconciliation) is OUT of scope
//     for the canonical surface (revived separately if needed).
//   - Routing is Handled by DestinationRequest (handler pre-resolves
//     OR voiceover.execute dest-resolves inside the use case).
type Handler struct {
	jobsSvc jobservice.Service
	log     *zap.Logger
}

// NewHandler constructs the slim canonical handler. ALL callers must
// pass a non-nil jobsSvc — fail-fast on nil via panic so a
// misconfigured composition root surfaces at startup rather than
// at the first request.
func NewHandler(jobsSvc jobservice.Service, log *zap.Logger) *Handler {
	if jobsSvc == nil {
		panic("voiceover.NewHandler: jobsSvc is required (jobs.Service for voiceover.generate enqueue)")
	}
	if log == nil {
		log = zap.NewNop()
	}
	return &Handler{
		jobsSvc: jobsSvc,
		log:     log,
	}
}

// RegisterRoutes registers the slim canonical surface. Only
// POST /generate is exposed; legacy routes (PR-VO-C1 sunset
// documented at voiceoverSunsetDate) are NOT re-registered this
// commit — their Sunset machinery stays in handler.go for future
// BACKFILL commits per godlike/07 §"BACKFILL window".
func (h *Handler) RegisterRoutes(r *gin.RouterGroup) {
	r.POST("/generate", h.Generate)
}

// Generate enqueues a voiceover.generate job (Blocco 4 EXPAND job
// type) and returns 202 Accepted with the canonical body shape.
//
// Dispatch contract (per Pattern 0 / AGENTS.md): the handler is
// THIN transport. The wire shape is the typed
// voiceover.GenerateVoiceoversCommand; the bound struct is unmarshalled
// by jobs/generate_handler.go on the worker side via the same
// json tags added to command.go (snake_case + omitempty). The handler
// runs the synchronous envelope ONLY — validation + enqueue + canonical
// 202 — and the heavy per-language fan-out lives in the worker
// (bounded parallel executor per Blocco 3).
//
// Errors:
//   - JSON binding failure / cmd.Validate failure → 400 Bad Request.
//   - jobsSvc.Enqueue failure → 500 Internal Server Error (the
//     dispatcher / SQLite store is the canonical owner of the
//     queue-idempotency contract).
func (h *Handler) Generate(c *gin.Context) {
	cmd, ok := apiutil.BindJSON[voiceover.GenerateVoiceoversCommand](c)
	if !ok {
		return
	}

	if err := cmd.Validate(); err != nil {
		apiutil.BadRequest(c, err.Error())
		return
	}

	jobReq := &jobservice.EnqueueRequest{
		Type:    jobservice.TypeVoiceoverGenerate,
		Payload: cmd,
	}

	enqueuedJob, enqueueErr := h.jobsSvc.Enqueue(c.Request.Context(), jobReq)
	if enqueueErr != nil {
		h.log.Error("voiceover.generate enqueue failed",
			zap.Error(enqueueErr))
		apiutil.InternalError(c, enqueueErr)
		return
	}

	h.log.Info("voiceover.generate enqueued",
		zap.String("job_id", enqueuedJob.ID),
		zap.String("request_id", enqueuedJob.CorrelationID),
		zap.Int("total_outputs", len(cmd.Languages)))

	apiutil.Accepted(c, gin.H{
		"ok":            true,
		"job_id":        enqueuedJob.ID,
		"status":        "queued",
		"total_outputs": len(cmd.Languages),
	})
}
