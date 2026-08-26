// Package api — vlm.go is the canonical SSOT for the
// /vlm/autotag/analyze-file HTTP boundary.
//
// godlike/07 NO-FAKE-AVAILABILITY (AGENTS.md): "Never represent an
// unavailable backend as a successful no-op. Fail closed with typed
// errors or do not register the capability."
//
// The route stays registered so that health probes + operator tooling
// can detect an unconfigured VLM sidecar (better than 404 / silent
// drop), but the handler NEVER returns a fake-success 200 with canned
// tags. Pre-July-2026 this file carried a deterministic fallback that
// derived scene_type / visual_objects / mood from filename heuristics
// (e.g. "pacquiao" -> boxing_match, anything with "stock"/"clip" ->
// stock_footage, video -> sports_action) and stamped the response as
// model pipelinegen-vlm-fallback. Operators were getting green-light
// responses from "the VLM" while zero neural networks were running,
// silently producing fake VisualSummary rows and polluting Qdrant.
// Removed here; the route now surfaces ErrVLMUnavailable.
//
// Companion infrastructure-side sentinel: ErrVLMDisabled in
// internal/platform/ai/vlm (different boundary -- VLM
// client pre-network failure mode). Both sentinels exist by design:
// api.ErrVLMUnavailable is HTTP-bound (this file), VLM-side
// ErrVLMDisabled surfaces before the network call. errors.Is can
// match either depending on call site; do not consolidate unless
// the call sites lose the layered visibility they need.
package httpserver

import (
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// ErrVLMUnavailable is the typed sentinel surfaced by the
// /vlm/autotag/analyze-file route when the VLM sidecar is not
// configured or unreachable. Callers branch on errors.Is(err,
// ErrVLMUnavailable); do not string-match the JSON "error" field.
var ErrVLMUnavailable = errors.New(
	"vlm_unavailable: VLM sidecar is not configured or unreachable; " +
		"see config.external.vlm_endpoint (canonical default http://127.0.0.1:8000)")

func registerVLMRoutes(engine *gin.Engine) {
	if engine == nil {
		return
	}

	// /vlm/autotag/analyze-file is intentionally fail-closed (godlike/07).
	// We keep the route registered so that /api/health probes, operator
	// diagnostics, and downstream call paths detect the
	// "vlm_unavailable" failure mode rather than 404-ing silently, but
	// every request is rejected with HTTP 503 + the typed
	// ErrVLMUnavailable. A future PR that wires the real Python sidecar
	// bridge (scripts/bridges/semantic_tagger/vlm.py per
	// internal/application/indexing/visual_summary.go) will replace this
	// handler with a thin proxy that preserves the typed surface.
	engine.POST("/vlm/autotag/analyze-file", func(c *gin.Context) {
		// Warn-log the failure with normalized request context so the
		// autotag sweeper iteration logs, /api/health probes, and
		// operator diagnostics can correlate a 503 with the asset
		// being processed (no silent 503 in production traces).
		// media_type is lowercased to match the inferVLMTagSet-era
		// convention and keep grep patterns stable.
		zap.L().Named("api.vlm").Warn(
			"vlm route fail-closed",
			zap.String("local_path", strings.TrimSpace(c.Query("local_path"))),
			zap.String("media_type", strings.ToLower(strings.TrimSpace(c.Query("media_type")))),
		)
		c.Header("X-VLM-Status", "unavailable")
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"error":  "vlm_unavailable",
			"reason": ErrVLMUnavailable.Error(),
		})
	})
}
