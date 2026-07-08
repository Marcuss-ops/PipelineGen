package script

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// LegacyGenerateWithImages handles POST /api/script/generate-with-images.
// PR-script-legacy-contract (P0 ABSOLUTE, deadline 2026-08-01, Jul
// 2026, canonical SHA 461b71a4): endpoint permanently retired to
// canonical PipelineGen 410-Gone contract. The deprecation counter
// increment (legacy_generate_with_images_total) and the X-Deprecation
// header emission stay at handler entry-point so the 7-day-zero
// retirement trigger on removal_date 2026-12-31 has the operational
// signal it needs (godlike/07 minimum-blast-radius — FREEZE-phase
// observability).
//
// PR-LEGACY-RETIRE-F2 (Aug 2026): the previously byte-stable surface
// (LegacyGenerateWithImagesRequest + its toEnvelope mapper) was
// physically deleted because the 410-only handler does not invoke it
// and there are zero live callers (Fase 0 Discovery confirmed). The
// 410 wire shape is still pinned by handler_legacy_deprecation_test.go
// (symmetric to LegacyGenerateFromClips contract).
func (h *ScriptFlowHandler) LegacyGenerateWithImages(c *gin.Context) {
	addGenerateWithImagesDeprecationHeader(c, removalDateWithImages)

	c.JSON(http.StatusGone, LegacyDeprecationPayload{
		OK:                   false,
		Error:                "endpoint retired; use POST /api/script/generate",
		CanonicalEndpoint:    "POST /api/script/generate",
		RemovalDate:          removalDateWithImages,
		DeprecationNoticeRef: "See X-Deprecation-Notice header for details",
	})
}
