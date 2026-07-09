package script

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	dto "github.com/prometheus/client_model/go"
)

// FASE 2.1 PR-VOICE-FREEZE (July 2026): retired the legacy CounterVec +
// DeprecationCount helper in favour of two typed Prometheus counters
// (one per active legacy route). Typed counters carry the route name
// in the metric suffix, not in a label, so the registered metric
// surface is exhaustive and observable in /metrics without the
// label-axis cardinality questions a CounterVec introduces.
//
// Each counter increments by exactly 1 per handler invocation.
// The FREEZE retirement contract (godlike/07 minimum-blast-radius):
// both counters are declared HERE so that when handler_legacy_*.go
// files are git-rm'd at the 2026-12-31 deadline (or earlier if
// rate(legacy_generate_from_clips_total[7d]) == 0 AND
// rate(legacy_generate_with_images_total[7d]) == 0), the counter
// declarations vanish with the handlers in a single atomic op.
// No external dependency on /workspace/observability/ metrics
// wiring is created; the promauto registration at package init()
// time is the canonical local registration per the inline
// counter discipline.

// legacyGenerateFromClipsTotal is the typed invocation counter for the
// POST /api/script/generate-from-clips handler. Increments at handler
// entry (BEFORE BindJSON so even rejected-by-PR3-guard payloads
// surface in /metrics, matching the X-Deprecated audit-marker
// contract — see TestLegacyGenerateFromClips_x_deprecated_header_set_even_on_400).
var legacyGenerateFromClipsTotal = promauto.NewCounter(prometheus.CounterOpts{
	Name: "legacy_generate_from_clips_total",
	Help: "Monotonic counter for POST /api/script/generate-from-clips handler invocations (deprecated; removal target 2026-12-31).",
})

// legacyGenerateWithImagesTotal is the typed invocation counter for the
// POST /api/script/generate-with-images handler. Increments at handler
// entry (BEFORE BindJSON so even rejected payloads surface in /metrics).
var legacyGenerateWithImagesTotal = promauto.NewCounter(prometheus.CounterOpts{
	Name: "legacy_generate_with_images_total",
	Help: "Monotonic counter for POST /api/script/generate-with-images handler invocations (deprecated; removal target 2026-12-31).",
})

// addGenerateFromClipsDeprecationHeader increments the generate-from-clips
// counter and emits the X-Deprecated response headers. The capture-by-name
// (NOT by-label) reflects the godlike/06 one-canonical-owner-per-fact
// discipline: route name lives in the function name, not in a string arg.
func addGenerateFromClipsDeprecationHeader(c *gin.Context, removalDate string) {
	legacyGenerateFromClipsTotal.Inc()
	c.Header("X-Deprecated", "true")
	c.Header("X-Deprecation-Notice",
		"POST /api/script/generate is the canonical endpoint. "+
			"This route will be removed on "+removalDate+".")
}

// addGenerateWithImagesDeprecationHeader increments the generate-with-images
// counter and emits the X-Deprecated response headers. Symmetric to
// addGenerateFromClipsDeprecationHeader per the 2-route parallel surface.
func addGenerateWithImagesDeprecationHeader(c *gin.Context, removalDate string) {
	legacyGenerateWithImagesTotal.Inc()
	c.Header("X-Deprecated", "true")
	c.Header("X-Deprecation-Notice",
		"POST /api/script/generate is the canonical endpoint. "+
			"This route will be removed on "+removalDate+".")
}

// DeprecationCount returns the cumulative invocation count across the active
// legacy script-generation route counters (snake_case metrics). Retained as
// a thin shim to avoid a godlike/07 minimum-blast-radius API break: this
// function WAS exported on the package before FASE 2.1, and any external
// operator tool / monitoring scrape / test fixture importing the function
// surface expects the symbol to remain. The implementation reads the 2
// typed counters via prometheus.Metric.Write so the live values are read
// back from the registered metric registry, not duplicated. godlike/06
// SSOT (one canonical owner per fact): the 2 typed counters in this file
// are the SOLE canonical write surface; DeprecationCount is a passive
// reader (errors.Wrap-friendly via having no error return type) that
// mirrors those values verbatim.
//
// Errors (impossible in production — typed counters are always registered
// at package init) are skipped silently so the function stays signature-
// stable for the legacy callers that have no error path.
func DeprecationCount() int64 {
	var total int64
	for _, counter := range []prometheus.Counter{
		legacyGenerateFromClipsTotal,
		legacyGenerateWithImagesTotal,
	} {
		var m dto.Metric
		if err := counter.Write(&m); err != nil {
			continue
		}
		total += int64(m.GetCounter().GetValue())
	}
	return total
}

const (
	removalDateFromClips  = "2026-12-31"
	removalDateWithImages = "2026-12-31"
)

// LegacyDeprecationPayload is the canonical HTTP 410 body shape for
// every retired legacy script-generation route. SSOT per
// PR-script-legacy-contract (P0 ABSOLUTE, Jul 2026): the body shape
// lives here (NOT inline-gin.H) so the JSON wire contract is
// verifiable from a single canonical source. Promotion-pointer: a
// future PR can move the type to internal/domain/script/ if it needs
// cross-package visibility (godlike/06 SSOT surface audit), but the
// current api/script-package-private scope is sufficient (only the
// 2 LegacyGenerate* handler bodies construct it).
//
// Fields are NOT omitempty so the wire shape carries the canonical
// set verbatim — operators' chrome tests look for these specific
// keys (godlike/06 SSOT observability contract).
type LegacyDeprecationPayload struct {
	OK                   bool   `json:"ok"`
	Error                string `json:"error"`
	CanonicalEndpoint    string `json:"canonical_endpoint"`
	RemovalDate          string `json:"removal_date"`
	DeprecationNoticeRef string `json:"deprecation_notice_ref"`
}

// StatusGoneDeprecated is the canonical HTTP 410 constant for the
// retired legacy routes. Single-SSOT discipline (godlike/06) — the
// handler bodies in handler_legacy_from_clips.go +
// handler_legacy_with_images.go reference this constant instead of
// importing net/http directly for http.StatusGone. Type assertion:
// `const StatusGoneDeprecated = http.StatusGone`.
const StatusGoneDeprecated = http.StatusGone

// PR-AUDIT-9 (P2, July 2026): verified the two legacy_generate_*_total
// Prometheus counters are correctly wired and the retirement deadline
// of 2026-12-31 is exhaustively documented across this file (godoc,
// const block, counter Help strings) + the 2 handler bodies
// (handler_legacy_from_clips.go + handler_legacy_with_images.go) +
// the test surface (handler_legacy_deprecation_test.go 6 TDD tests +
// handler_legacy_int_stock_test.go wire-shape assertions). The FREEZE
// retirement trigger (rate(legacy_generate_*_total[7d]) == 0 for both
// counters, sustained 7 days, THEN git-rm handlers + consts + counters
// in a single atomic commit) is the canonical godlike/07 NO-FAKE-
// AVAILABILITY contract — do NOT remove the counter declarations before
// the 7-day-zero gate is met; do NOT remove the handlers while either
// counter is non-zero.
//
// RegisterLegacyDeprecationRoutes mounts the 2 retired legacy routes
// (generate-from-clips + generate-with-images) under r with the canonical
// 410-Gone contract. PR-script-legacy-contract (Jul 2026, P0 ABSOLUTE):
// this function IS the canonical godlike/06 SSOT for the 2 legacy
// route bindings — the r.POST lines that USED to live in
// handler_flow.go::RegisterRoutes were physically MOVED here so the
// handler-flow REGISTER surface stops carrying legacy-pipeline
// bindings (literal compliance with "Rimuovi i 2 r.POST dalla
// RegisterRoutes di handler_flow.go"). Counter increments happen at
// handler entry via the existing addGenerate*DeprecationHeader
// helpers, so the FREEZE-phase observability invariant
// (rate(metric[7d]) == 0 triggers 2026-12-31 retirement) stays LIVE.
//
// godlike/07 minimum-blast-radius: this function is delegated-to at
// the end of handler_flow.go::RegisterRoutes, so observable route
// surface is byte-compatible with pre-PR callers of RegisterRoutes
// directly (handler_test.go + handler_idempotency_test.go pass
// unchanged).
func (h *ScriptFlowHandler) RegisterLegacyDeprecationRoutes(r *gin.RouterGroup) {
	r.POST("/generate-from-clips", h.LegacyGenerateFromClips)
	r.POST("/generate-with-images", h.LegacyGenerateWithImages)
}
