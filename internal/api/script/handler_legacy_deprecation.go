package script

import (
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
