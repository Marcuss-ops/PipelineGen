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
	"net/http"

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

// legacyVoiceoverRouteInvocationsTotal is the per-operation Prometheus
// counter that tracks voiceover operations under the BACKFILL/CUTOVER
// window (Block 6, June 2026). The label set has been refreshed to
// align with the post-Block-4 EXPAND canonical surface:
//
//   - "generate" — canonical async POST /api/voiceover/generate
//     (handler.go Generate). The destination.kind=group|explicit
//     distinction lives in the PAYLOAD field of GenerateVoiceoversCommand,
//     NOT in this counter. /groups resolution moved out of the HTTP
//     handler at Block 4 EXPAND and is now owned by the use case layer
//     (AGENTS.md Pattern 8 thin-transport rule).
//   - "sync" — emitted by
//     internal/application/assets/reconciliation/voiceover/service.go,
//     invoked EXCLUSIVELY via cmd/admin/cleanup.go:507 after the
//     Block 4 EXPAND slim removed the handler /sync route. There is
//     no longer any HTTP surface for /sync — admin CLI only.
//   - "generate-with-group" — DEPRECATED (legacy HTTP route removed
//     from RegisterRoutes at Block 4 EXPAND). Label value retained
//     briefly for backwards-compat dashboard series during the
//     2026-06-28 → 2026-09-26 Sunset window; CONTRACTED at PR-VO-E1.
//
// At this Block 6 confirmation commit, NO code path emits the counter:
// /generate is the canonical surface (not a deprecated legacy route)
// and /sync is admin-only (not network-reachable). Future BACKFILL
// observability commits may add a log-tap watchdog; the help text
// and LegacyVoiceoverDeprecationCount iteration list are
// pre-documented so dashboards can be wired earlier than emission.
//
// Operators expose this counter via /metrics or admin dashboards.
// Mirrors handler_legacy_adapters.go::legacyRouteInvocationsTotal
// pattern (Wave 21 + PR-VO-C1, June 2026).
var legacyVoiceoverRouteInvocationsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
	Name: "legacy_voiceover_route_invocations_total",
	Help: "Monotonic counter for voiceover canonical operations tracked under the BACKFILL/CUTOVER window, labelled by operation name. Known label values: generate, sync. (deprecated: generate-with-group — retained for legacy dashboard series during the 2026-09-26 Sunset window, contracted at PR-VO-E1).",
}, []string{"route"})

// LegacyVoiceoverDeprecationCount returns the cumulative invocation
// count across the voiceover operations tracked under the
// BACKFILL/CUTOVER window (Block 6, June 2026) by reading the
// legacyVoiceoverRouteInvocationsTotal prometheus counter (dto.Metric
// writeback pattern). Exposed for admin/diagnostic surfaces so the
// PR-VO-C1 Sunset deadline can be tracked against live usage.
//
// Block 6 — refreshed label set (one-owner-per-fact, godlike/06):
//
//   - "generate"             — POST /api/voiceover/generate
//                              (canonical Block 4 EXPAND surface).
//   - "sync"                 — cmd admin cleanup /sync path
//                              (cmd/admin/cleanup.go:507 only;
//                              no HTTP route reachable from
//                              external clients).
//   - "generate-with-group"  — DEPRECATED legacy HTTP route
//                              (removed at Block 4 EXPAND slim);
//                              label value retained for backwards-
//                              compat dashboard series during the
//                              2026-09-26 Sunset window; CONTRACTED
//                              at PR-VO-E1.
func LegacyVoiceoverDeprecationCount() int64 {
	var total int64
	for _, route := range []string{"generate", "sync", "generate-with-group"} {
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

	// PR-VO-CONTRACT Phase 2 (godlike/13 §"Runtime cut", June 2026):
	// re-bind the legacy routes as 410 Gone stubs so existing clients
	// discover the migration timer via RFC 8594 Sunset + RFC 8288
	// successor-version Link headers. The 4 legacy paths were removed
	// from RegisterRoutes at Blocco 4 EXPAND slim; here we restore them
	// minimally (no business logic) so the BACKFILL/CUTOVER signal is
	// observable from any HTTP client. The headers are duplicated
	// locally from addVoiceoverDeprecationHeader so the Phase 7 deletion
	// of the Sunset machinery does not strand a 410 stub with no header
	// payload. r.Any catches GET/POST/PUT/DELETE — legacy callers
	// may have used any method on these paths.
	r.Any("/generate-with-group", h.voiceoverGone)
	r.Any("/batch", h.voiceoverGone)
	r.Any("/promo", h.voiceoverGone)
	r.Any("/sync", h.voiceoverGone)
}

// voiceoverGone (PR-VO-CONTRACT Phase 2, June 2026) emits an
// RFC 8594 + RFC 8288 410 Gone response for the legacy voiceover
// routes. Headers are intentionally inlined from the
// voiceoverSunsetDate constant + a static successor-version Link
// pointer — this isolates the runtime-cut 410 stub from the
// Phase 7 deletion of addVoiceoverDeprecationHeader (godlike/07
// CONTRACT half), so the 410 stub still emits valid Sunset +
// successor-version headers after the Sunset machinery is gone.
func (h *Handler) voiceoverGone(c *gin.Context) {
	c.Header("Sunset", voiceoverSunsetDate)
	c.Header("Link", `</api/voiceover/generate>; rel="successor-version"`)
	c.AbortWithStatusJSON(http.StatusGone, gin.H{
		"error":     "This legacy voiceover endpoint was sunset on 2026-09-26. Use POST /api/voiceover/generate.",
		"sunset":    voiceoverSunsetDate,
		"successor": "/api/voiceover/generate",
	})
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
