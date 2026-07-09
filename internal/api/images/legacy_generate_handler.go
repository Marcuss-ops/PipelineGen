// Package images (api/images) — legacy_generate_handler.go holds
// the legacy synchronous AI image generation handler
// (POST /api/images/generate). Per PR-AUDIT-3 (2026-07-09): this
// route is now RETIRED with HTTP 410 Gone, matching the legacy
// script-route precedent (handler_legacy_deprecation.go). The
// canonical generated-territory generation endpoint is
// POST /api/images/generated/generate (generated_generate_handler.go).
//
// BEFORE PR-AUDIT-3: this handler called GenerateSmartImage with
// legacy defaults (empty subject/topic, skipDrive=false) and was
// a live, active pipeline. PR-IMG-LEGACY-5 (IMAGES-LEGACY-CLEANUP-2026-07-06)
// unified the request type to ImageGenerationRequest but kept the
// handler operational. The 410 retirement removes the duplicate
// pipeline surface; callers must migrate to the canonical
// /api/images/generated/generate endpoint.
package images

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// legacyImagesGenerateTotal is the typed invocation counter for the
// POST /api/images/generate handler. Increments at handler entry
// (BEFORE any payload binding) so operators can monitor the
// rate(legacy_images_generate_total[7d]) metric for the 7-day
// sustained-zero gate. When that gate is met, the route can be
// physically git-rm'd per the FASE-2.1-VOICE-FREEZE pattern.
var legacyImagesGenerateTotal = promauto.NewCounter(prometheus.CounterOpts{
	Name: "legacy_images_generate_total",
	Help: "Monotonic counter for POST /api/images/generate handler invocations (deprecated; removal target 2026-12-31).",
})

const (
	// deprecationRemovalDateImages is the canonical removal date for the
	// legacy POST /api/images/generate route. Mirrors the
	// removalDateFromClips / removalDateWithImages constants in the
	// script legacy deprecation surface (handler_legacy_deprecation.go).
	deprecationRemovalDateImages = "2026-12-31"

	// canonicalImagesGenerateEndpoint is the canonical replacement
	// endpoint advertised in the 410 response body.
	canonicalImagesGenerateEndpoint = "POST /api/images/generated/generate"
)

// legacyDeprecationPayload is the canonical HTTP 410 body shape for
// retired legacy image-generation routes. SSOT per PR-AUDIT-3
// (godlike/06): the body shape lives here so the JSON wire contract
// is verifiable from a single canonical source. Mirrors
// LegacyDeprecationPayload in handler_legacy_deprecation.go.
//
// Fields are NOT omitempty so the wire shape carries the canonical
// set verbatim — operators' chrome tests look for these specific keys.
type legacyDeprecationPayload struct {
	OK                   bool   `json:"ok"`
	Error                string `json:"error"`
	CanonicalEndpoint    string `json:"canonical_endpoint"`
	RemovalDate          string `json:"removal_date"`
	DeprecationNoticeRef string `json:"deprecation_notice_ref"`
}

// Generate handles POST /api/images/generate — RETIRED legacy
// synchronous AI image generation. Returns HTTP 410 Gone per
// PR-AUDIT-3 (godlike/07 no-fake-availability: the handler MUST NOT
// call GenerateSmartImage, even as a fallback). The canonical
// replacement is POST /api/images/generated/generate.
//
// godlike/07 typed-error contract: every invocation returns exactly
// the same wire shape — the 410 body always carries the canonical
// LegacyDeprecationPayload pointing to the replacement endpoint.
// Counter incremented BEFORE response so even aborted requests
// surface in /metrics.
func (h *ImagesHandler) Generate(c *gin.Context) {
	legacyImagesGenerateTotal.Inc()

	c.Header("X-Deprecated", "true")
	c.Header("X-Deprecation-Notice",
		canonicalImagesGenerateEndpoint+" is the canonical endpoint. "+
			"This route will be removed on "+deprecationRemovalDateImages+".")

	c.AbortWithStatusJSON(http.StatusGone, legacyDeprecationPayload{
		OK:                   false,
		Error:                "This endpoint has been retired. Use " + canonicalImagesGenerateEndpoint + " instead.",
		CanonicalEndpoint:    canonicalImagesGenerateEndpoint,
		RemovalDate:          deprecationRemovalDateImages,
		DeprecationNoticeRef: "PR-AUDIT-3 (2026-07-09): legacy /api/images/generate retired to 410 Gone",
	})
}
