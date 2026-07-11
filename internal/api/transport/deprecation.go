// Package transport — deprecation.go provides the canonical HTTP 410
// Gone payload for retired legacy API routes.
//
// PR-AUDIT-3 (2026-07-09): every retired legacy route MUST return the
// same wire shape so operators and clients have a single contract to
// rely on. The payload carries the canonical replacement endpoint and
// a removal date so callers know when the route will be physically
// removed.
package transport

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// LegacyDeprecationPayload is the canonical HTTP 410 body shape for
// retired legacy routes. Fields are NOT omitempty so the wire shape is
// stable and testable.
type LegacyDeprecationPayload struct {
	OK                   bool   `json:"ok"`
	Error                string `json:"error"`
	CanonicalEndpoint    string `json:"canonical_endpoint"`
	RemovalDate          string `json:"removal_date"`
	DeprecationNoticeRef string `json:"deprecation_notice_ref"`
}

// DeprecationNotice holds the metadata needed to build a 410 response.
type DeprecationNotice struct {
	// CanonicalEndpoint is the replacement endpoint, e.g.
	// "POST /api/images/generated/generate".
	CanonicalEndpoint string
	// RemovalDate is the ISO-8601 date when the route will be removed.
	RemovalDate string
	// DeprecationNoticeRef is a stable reference (PR + date) for the
	// retirement decision.
	DeprecationNoticeRef string
}

// Respond410Gone writes the canonical 410 Gone response for a retired
// legacy route. It sets X-Deprecated and X-Deprecation-Notice headers
// and returns the JSON payload.
func Respond410Gone(c *gin.Context, notice DeprecationNotice) {
	c.Header("X-Deprecated", "true")
	c.Header("X-Deprecation-Notice",
		notice.CanonicalEndpoint+" is the canonical endpoint. "+
			"This route will be removed on "+notice.RemovalDate+".")

	c.AbortWithStatusJSON(http.StatusGone, LegacyDeprecationPayload{
		OK:                   false,
		Error:                "This endpoint has been retired. Use " + notice.CanonicalEndpoint + " instead.",
		CanonicalEndpoint:    notice.CanonicalEndpoint,
		RemovalDate:          notice.RemovalDate,
		DeprecationNoticeRef: notice.DeprecationNoticeRef,
	})
}
