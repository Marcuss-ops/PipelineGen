package sources

import (
	"net/http"
	"regexp"
	"strings"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/media/vectorstore"
	defaults "github.com/Marcuss-ops/PipelineGen/internal/platform"
	textutil "github.com/Marcuss-ops/PipelineGen/internal/platform"
)

// RecommendRequest is the input for the clip recommendation endpoint.
// Takes a full script text, splits it into scenes automatically,
// and searches Qdrant for the best matching clips per scene.
type RecommendRequest struct {
	ScriptText string  `json:"script_text" binding:"required"`
	Language   string  `json:"language,omitempty"`
	Source     string  `json:"source,omitempty"`
	MediaType  string  `json:"media_type,omitempty"`
	TopK       int     `json:"top_k_per_scene,omitempty"`
	MinScore   float64 `json:"min_score,omitempty"`
}

// RecommendSceneResult is the recommendation result for a single scene.
type RecommendSceneResult struct {
	Scene           string              `json:"scene"`
	SceneIndex      int                 `json:"scene_index"`
	Query           string              `json:"query"`
	Recommendations []RecommendClipItem `json:"recommendations"`
}

// RecommendClipItem is a single recommended clip for a scene.
type RecommendClipItem struct {
	AssetID        string   `json:"asset_id"`
	Title          string   `json:"title"`
	Score          float64  `json:"score"`
	Source         string   `json:"source,omitempty"`
	MediaType      string   `json:"media_type,omitempty"`
	Language       string   `json:"language,omitempty"`
	LocalPath      string   `json:"local_path,omitempty"`
	DriveLink      string   `json:"drive_link,omitempty"`
	YouTubeVideoID string   `json:"youtube_video_id,omitempty"`
	YouTubeURL     string   `json:"youtube_url,omitempty"`
	StartTime      string   `json:"start_time,omitempty"`
	EndTime        string   `json:"end_time,omitempty"`
	Tags           []string `json:"tags,omitempty"`
	SearchText     string   `json:"search_text,omitempty"`
	Reason         string   `json:"reason,omitempty"`
}

// RecommendResponse is the full response from the recommend endpoint.
type RecommendResponse struct {
	OK            bool                   `json:"ok"`
	ScriptPreview string                 `json:"script_preview"`
	SceneCount    int                    `json:"scene_count"`
	Scenes        []RecommendSceneResult `json:"scenes"`
	TotalClips    int                    `json:"total_clips"`
	Language      string                 `json:"language,omitempty"`
}

// RecommendClips splits the script into scenes, searches Qdrant per scene,
// and returns the best matching clips with similarity scores and reasoning.
func (h *Handler) RecommendClips(c *gin.Context) {
	if h.realtimeSvc == nil {
		apiutil.BadRequest(c, "Vector search / Realtime matching service is disabled or not configured.")
		return
	}

	var req RecommendRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiutil.BadRequest(c, "invalid request: "+err.Error())
		return
	}

	req.ScriptText = strings.TrimSpace(req.ScriptText)
	if req.ScriptText == "" {
		apiutil.BadRequest(c, "script_text is required")
		return
	}

	req.TopK = defaults.Int(req.TopK, 5)
	if req.MinScore <= 0 {
		req.MinScore = h.cfg.VectorSearch.MinInstantScore
	}
	if req.MinScore <= 0 {
		req.MinScore = 0.5
	}

	// Step 1: Split script into scenes (by double newline, then filter short lines)
	rawScenes := splitScriptIntoScenes(req.ScriptText)

	h.log.Info("recommend: splitting script into scenes",
		zap.Int("raw_scenes", len(rawScenes)),
		zap.String("language", req.Language),
		zap.String("source", req.Source),
	)

	// Step 2: For each scene, search Qdrant
	resp := &RecommendResponse{
		OK:            true,
		ScriptPreview: textutil.Truncate(req.ScriptText, 100),
		SceneCount:    len(rawScenes),
		Scenes:        make([]RecommendSceneResult, 0, len(rawScenes)),
		Language:      req.Language,
	}

	totalClips := 0
	usedAssetIDs := make(map[string]bool) // Avoid recommending the same clip twice

	for i, sceneText := range rawScenes {
		if strings.TrimSpace(sceneText) == "" {
			continue
		}

		// Generate query text: use the scene text itself as the search query
		queryText := cleanQueryText(sceneText)
		if len(queryText) > 300 {
			queryText = queryText[:300]
		}

		// Get embedding for this scene
		queryVector, err := h.realtimeSvc.EmbedTextForVector(c.Request.Context(), queryText, "text")
		if err != nil {
			h.log.Warn("recommend: embedding failed for scene",
				zap.Int("scene_index", i),
				zap.Error(err))
			continue
		}

		// Build search request with optional filters
		// Use HybridSearch to leverage BM25 + transcript + RRF fusion
		// This is critical for YouTube clips where spoken content matters
		searchReq := vectorstore.HybridSearchRequest{
			QueryText:            cleanQueryText(queryText),
			DenseVector:          queryVector,
			DenseVectorName:      h.cfg.VectorSearch.TextVectorName,
			TranscriptVector:     queryVector,
			TranscriptVectorName: h.cfg.VectorSearch.TranscriptVectorName,
			Limit:                req.TopK * 2,
			MinScore:             req.MinScore,
			Source:               req.Source,
			MediaType:            req.MediaType,
			Language:             req.Language,
		}

		// Search Qdrant via hybrid (dense + BM25 + transcript + RRF)
		results, err := h.realtimeSvc.VectorStore().HybridSearch(c.Request.Context(), searchReq)
		if err != nil {
			h.log.Warn("recommend: Qdrant search failed for scene",
				zap.Int("scene_index", i),
				zap.Error(err))
			continue
		}

		// Build recommendations for this scene (deduplicate across scenes)
		sceneResult := RecommendSceneResult{
			Scene:      textutil.Truncate(sceneText, 120),
			SceneIndex: i,
			Query:      queryText,
		}

		for _, r := range results {
			// Skip if already recommended in a previous scene
			if usedAssetIDs[r.AssetID] {
				continue
			}

			item := RecommendClipItem{
				AssetID:        r.AssetID,
				Title:          r.Name,
				Score:          r.Score,
				Source:         r.Source,
				MediaType:      r.MediaType,
				Language:       r.Language,
				LocalPath:      r.LocalPath,
				DriveLink:      r.DriveLink,
				YouTubeVideoID: r.YouTubeVideoID,
				YouTubeURL:     r.YouTubeURL,
				StartTime:      r.StartTime,
				EndTime:        r.EndTime,
				Tags:           r.Tags,
				SearchText:     textutil.Truncate(r.SearchText, 150),
				Reason:         buildRecommendReason(r, queryText),
			}
			sceneResult.Recommendations = append(sceneResult.Recommendations, item)
			usedAssetIDs[r.AssetID] = true

			if len(sceneResult.Recommendations) >= req.TopK {
				break
			}
		}

		resp.Scenes = append(resp.Scenes, sceneResult)
		totalClips += len(sceneResult.Recommendations)
	}

	resp.TotalClips = totalClips

	h.log.Info("recommend: completed",
		zap.Int("scenes", len(resp.Scenes)),
		zap.Int("total_clips", totalClips),
	)

	c.JSON(http.StatusOK, resp)
}

// splitScriptIntoScenes splits a script into logical scenes.
// Scenes are separated by double newlines. Short fragments (<20 chars) are merged
// with adjacent scenes.
func splitScriptIntoScenes(script string) []string {
	// Split by double newline (paragraph boundary)
	raw := regexp.MustCompile(`\n\s*\n`).Split(script, -1)

	var scenes []string
	for _, part := range raw {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		// If very short, merge with previous scene
		if len(part) < 20 && len(scenes) > 0 {
			scenes[len(scenes)-1] = scenes[len(scenes)-1] + " " + part
		} else {
			scenes = append(scenes, part)
		}
	}

	// Also try splitting by scene markers (e.g., "**Scene", "BLOCCO", numbered items)
	if len(scenes) <= 1 {
		// Try numbered splits (e.g., "1.", "2.", "Blocco 1:", "Scene 2:")
		numbered := regexp.MustCompile(`(?m)^(?:(?:\d+[.)]\s*)|(?:Blocco\s+\d+)|(?:Scene\s+\d+)|(?:##\s*))`).Split(script, -1)
		if len(numbered) > 1 {
			scenes = make([]string, 0, len(numbered))
			for _, part := range numbered {
				part = strings.TrimSpace(part)
				if part != "" && len(part) > 15 {
					scenes = append(scenes, part)
				}
			}
		}
	}

	// If we still only have 1 scene, split by sentence boundaries
	if len(scenes) <= 1 {
		// Split long text into chunks of ~200 chars at sentence boundaries
		sentences := regexp.MustCompile(`[.!?]\s+`).Split(script, -1)
		var currentScene strings.Builder
		for _, s := range sentences {
			s = strings.TrimSpace(s)
			if s == "" {
				continue
			}
			if currentScene.Len() > 0 && currentScene.Len()+len(s) > 200 {
				scenes = append(scenes, currentScene.String())
				currentScene.Reset()
			}
			if currentScene.Len() > 0 {
				currentScene.WriteString(" ")
			}
			currentScene.WriteString(s)
		}
		if currentScene.Len() > 0 {
			scenes = append(scenes, currentScene.String())
		}
	}

	// If still no scenes, use the whole text
	if len(scenes) == 0 {
		scenes = []string{script}
	}

	return scenes
}

// cleanQueryText prepares scene text for embedding search.
// Removes markdown, emoji, and very short words.
func cleanQueryText(text string) string {
	// Remove markdown headers
	text = regexp.MustCompile(`#{1,6}\s+`).ReplaceAllString(text, "")
	// Remove emoji and special chars but keep letters, numbers, punctuation
	text = regexp.MustCompile(`[^\p{L}\p{N}\s.,!?;:'"-]`).ReplaceAllString(text, " ")
	// Collapse whitespace
	text = regexp.MustCompile(`\s+`).ReplaceAllString(text, " ")
	return strings.TrimSpace(text)
}

// buildRecommendReason generates a human-readable reason for why a clip was recommended.
func buildRecommendReason(result vectorstore.SearchResult, queryText string) string {
	parts := []string{}
	if result.Score >= 0.8 {
		parts = append(parts, "alta similarità semantica")
	} else if result.Score >= 0.6 {
		parts = append(parts, "buona corrispondenza tematica")
	} else {
		parts = append(parts, "corrispondenza parziale")
	}

	if result.Language != "" {
		parts = append(parts, "lingua: "+result.Language)
	}
	if result.Source != "" {
		parts = append(parts, "fonte: "+result.Source)
	}
	if len(result.Tags) > 0 {
		tags := result.Tags
		if len(tags) > 3 {
			tags = tags[:3]
		}
		parts = append(parts, "tag: "+strings.Join(tags, ", "))
	}

	return strings.Join(parts, " | ")
}
