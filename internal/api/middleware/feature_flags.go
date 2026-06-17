package middleware

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"velox/go-master/internal/config"
)

// FeatureFlagChecker returns a gin.HandlerFunc that checks if a feature is enabled
func FeatureFlagChecker(cfg *config.Config, featureName string, isEnabled bool) gin.HandlerFunc {
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

// ArtlistEnabled checks if the Artlist feature is enabled
func ArtlistEnabled(cfg *config.Config) gin.HandlerFunc {
	return FeatureFlagChecker(cfg, "Artlist", cfg.Features.ArtlistEnabled)
}

// ScriptDocsEnabled checks if the ScriptDocs feature is enabled
func ScriptDocsEnabled(cfg *config.Config) gin.HandlerFunc {
	return FeatureFlagChecker(cfg, "ScriptDocs", cfg.Features.ScriptDocsEnabled)
}

// ScriptClipsEnabled checks if the ScriptClips feature is enabled
func ScriptClipsEnabled(cfg *config.Config) gin.HandlerFunc {
	return FeatureFlagChecker(cfg, "ScriptClips", cfg.Features.ScriptClipsEnabled)
}
