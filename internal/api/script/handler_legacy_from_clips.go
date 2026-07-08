package script

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// LegacyGenerateFromClips handles POST /api/script/generate-from-clips.
// PR-script-legacy-contract (P0 ABSOLUTE, deadline 2026-08-01, Jul
// 2026, canonical SHA 461b71a4): endpoint permanently retired to
// canonical PipelineGen 410-Gone contract. The deprecation counter
// increment (legacy_generate_from_clips_total) and the X-Deprecation
// header emission stay at handler entry-point so the 7-day-zero
// retirement trigger on removal_date 2026-12-31 has the operational
// signal it needs (godlike/07 minimum-blast-radius — FREEZE-phase
// observability is the only thing keeping the route alive).
//
// PR-LEGACY-RETIRE-F2 (Aug 2026): the previously byte-stable surface
// (LegacyClipInput + LegacyGenerateFromClipsRequest + their toEnvelope /
// deriveClipIDs / resolveAliases helpers + warnIgnoredLegacyFields) was
// physically deleted because the 410-only handler does not invoke them
// and there are zero live callers (Fase 0 Discovery confirmed). The 410
// wire shape is still pinned by handler_legacy_deprecation_test.go
// (4 invariants per route: status code, body payload, counter delta,
// X-Deprecation headers) so the FREEZE-phase observability contract is
// unbroken.
func (h *ScriptFlowHandler) LegacyGenerateFromClips(c *gin.Context) {
	addGenerateFromClipsDeprecationHeader(c, removalDateFromClips)

	c.JSON(http.StatusGone, LegacyDeprecationPayload{
		OK:                   false,
		Error:                "endpoint retired; use POST /api/script/generate",
		CanonicalEndpoint:    "POST /api/script/generate",
		RemovalDate:          removalDateFromClips,
		DeprecationNoticeRef: "See X-Deprecation-Notice header for details",
	})
}
