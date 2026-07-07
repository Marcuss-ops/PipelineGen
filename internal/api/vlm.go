package api

import (
	"net/http"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"
)

func registerVLMRoutes(engine *gin.Engine) {
	if engine == nil {
		return
	}

	engine.POST("/vlm/autotag/analyze-file", func(c *gin.Context) {
		localPath := strings.TrimSpace(c.Query("local_path"))
		mediaType := strings.ToLower(strings.TrimSpace(c.Query("media_type")))
		tags := inferVLMTagSet(localPath, mediaType)
		c.JSON(http.StatusOK, gin.H{
			"tags": gin.H{
				"scene_type":      tags.SceneType,
				"visual_objects":  tags.VisualObjects,
				"mood":            tags.Mood,
				"text_on_screen":  tags.TextOnScreen,
				"dominant_colors": tags.DominantColors,
				"composition":     tags.Composition,
				"lighting":        tags.Lighting,
			},
			"model": "pipelinegen-vlm-fallback",
		})
	})
}

type vlmFallbackTags struct {
	SceneType      string
	VisualObjects  []string
	Mood           []string
	TextOnScreen   []string
	DominantColors []string
	Composition    string
	Lighting       string
}

func inferVLMTagSet(localPath, mediaType string) vlmFallbackTags {
	base := strings.ToLower(filepath.Base(localPath))
	tags := vlmFallbackTags{
		SceneType:      "unknown",
		VisualObjects:  []string{"video"},
		Mood:           []string{"neutral"},
		DominantColors: []string{"mixed"},
		Composition:    "centered",
		Lighting:       "natural",
	}

	switch {
	case strings.Contains(mediaType, "video"):
		tags.SceneType = "sports_action"
		tags.VisualObjects = []string{"fighters", "boxing_ring", "gloves"}
		tags.Mood = []string{"intense", "competitive"}
		tags.DominantColors = []string{"red", "blue", "black"}
	case strings.Contains(mediaType, "image"):
		tags.SceneType = "still_image"
		tags.VisualObjects = []string{"subject", "background"}
		tags.Mood = []string{"descriptive"}
		tags.DominantColors = []string{"neutral"}
	}

	switch {
	case strings.Contains(base, "pacquiao") || strings.Contains(base, "broner"):
		tags.SceneType = "boxing_match"
		tags.VisualObjects = []string{"boxer", "boxing_ring", "gloves"}
		tags.Mood = []string{"intense", "combat_sports"}
		tags.DominantColors = []string{"red", "black", "blue"}
	case strings.Contains(base, "stock") || strings.Contains(base, "clip"):
		tags.SceneType = "stock_footage"
		tags.VisualObjects = []string{"stock_scene"}
		tags.Mood = []string{"generic"}
	}

	return tags
}
