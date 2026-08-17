package middleware

import (
	"net/http"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/application/middleware"
	"github.com/gin-gonic/gin"
)

// FeatureFlagChecker returns a gin.HandlerFunc that checks if a feature
// is enabled. PG-006 (June 2026): dropped the `cfg *config.Config`
// argument entirely — the body only ever used the `isEnabled` bool, so
// the cfg parameter was dead weight. Callers that compose this with a
// per-feature FeatureFlagsPort now read the bool themselves.
func FeatureFlagChecker(featureName string, isEnabled bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !isEnabled {
			c.JSON(http.StatusServiceUnavailable, gin.H{
				"error":  "module disabled",
				"module": strings.ToLower(featureName),
			})
			c.Abort()
			return
		}
		c.Next()
	}
}

// ArtlistEnabled checks if the Artlist feature is enabled.
func ArtlistEnabled(flags middleware.FeatureFlagsPort) gin.HandlerFunc {
	return FeatureFlagChecker("Artlist", flags != nil && flags.ArtlistEnabled())
}

// ScriptClipsEnabled checks if the ScriptClips feature is enabled.
func ScriptClipsEnabled(flags middleware.FeatureFlagsPort) gin.HandlerFunc {
	return FeatureFlagChecker("ScriptClips", flags != nil && flags.ScriptClipsEnabled())
}
