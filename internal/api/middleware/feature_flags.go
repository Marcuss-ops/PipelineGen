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

// YouTubeEnabled checks if the YouTube feature is enabled
func YouTubeEnabled(cfg *config.Config) gin.HandlerFunc {
	return FeatureFlagChecker(cfg, "YouTube", cfg.Features.YouTubeEnabled)
}

// ScriptDocsEnabled checks if the ScriptDocs feature is enabled
func ScriptDocsEnabled(cfg *config.Config) gin.HandlerFunc {
	return FeatureFlagChecker(cfg, "ScriptDocs", cfg.Features.ScriptDocsEnabled)
}

// ScriptClipsEnabled checks if the ScriptClips feature is enabled
func ScriptClipsEnabled(cfg *config.Config) gin.HandlerFunc {
	return FeatureFlagChecker(cfg, "ScriptClips", cfg.Features.ScriptClipsEnabled)
}

// WorkflowEnabled checks if the Workflow feature is enabled
func WorkflowEnabled(cfg *config.Config) gin.HandlerFunc {
	return FeatureFlagChecker(cfg, "Workflow", cfg.Features.WorkflowEnabled)
}

// VoiceoverEnabled checks if the Voiceover feature is enabled
func VoiceoverEnabled(cfg *config.Config) gin.HandlerFunc {
	return FeatureFlagChecker(cfg, "Voiceover", cfg.Features.VoiceoverEnabled)
}

// DriveEnabled checks if the Drive feature is enabled
func DriveEnabled(cfg *config.Config) gin.HandlerFunc {
	return FeatureFlagChecker(cfg, "Drive", cfg.Features.DriveEnabled)
}

// ImagesEnabled checks if the Images feature is enabled
func ImagesEnabled(cfg *config.Config) gin.HandlerFunc {
	return FeatureFlagChecker(cfg, "Images", cfg.Features.ImagesEnabled)
}
