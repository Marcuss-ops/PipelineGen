package clipresolver

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/ai/ollama/client"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/ai/ollama/types"
	textutil "github.com/Marcuss-ops/PipelineGen/internal/platform"

	"go.uber.org/zap"
)

// LLMDecisionConfig controls the LLM clip evaluation behaviour.
type LLMDecisionConfig struct {
	Enabled bool   // Toggle the LLM decision layer (default: true)
	Model   string // Ollama model (default: "gemma4:e4b")
	TopK    int    // Number of top candidates to evaluate (default: 5)
	Timeout time.Duration
}

// DefaultLLMDecisionConfig returns sensible defaults.
func DefaultLLMDecisionConfig() LLMDecisionConfig {
	return LLMDecisionConfig{
		Enabled: true,
		Model:   "gemma4:e4b",
		TopK:    5,
		Timeout: 30 * time.Second,
	}
}

// LLMDecisionResult holds the LLM's evaluation of the top candidates.
type LLMDecisionResult struct {
	ChosenClipID   string  `json:"chosen_clip_id"`   // The clip the LLM selects
	Rating         string  `json:"rating"`           // HIGH / MEDIUM / LOW
	Reason         string  `json:"reason"`           // Natural language explanation
	OriginalRank   int     `json:"original_rank"`    // The rank before LLM re-evaluation
	OriginalScore  float64 `json:"original_score"`   // The score before LLM re-evaluation
	ChosenNewScore float64 `json:"chosen_new_score"` // The LLM-assigned score (0-100)
}

// LLMDecisionService wraps an LLM (Ollama) to evaluate top-N clip candidates
// and select the best one with a human-readable justification.
//
// It sits AFTER Qdrant ANN search + reranker + weighted scoring,
// providing a final LLM-powered quality gate before clip selection.
type LLMDecisionService struct {
	client *client.Client
	cfg    LLMDecisionConfig
	log    *zap.Logger
}

// NewLLMDecisionService creates a new LLM decision service.
// If ollamaClient is nil, the service will be disabled.
func NewLLMDecisionService(ollamaClient *client.Client, cfg LLMDecisionConfig, log *zap.Logger) *LLMDecisionService {
	if log == nil {
		log = zap.NewNop()
	}
	return &LLMDecisionService{
		client: ollamaClient,
		cfg:    cfg,
		log:    log,
	}
}

// IsEnabled returns whether the LLM decision layer is active.
func (s *LLMDecisionService) IsEnabled() bool {
	return s.cfg.Enabled && s.client != nil
}

// EvaluateCandidates takes the top-N recommended clips and the original request,
// calls the LLM to evaluate each candidate's relevance, and returns the LLM's
// choice with a detailed reasoning.
//
// Returns nil if the LLM is unavailable or returns unparseable output — the
// caller should fall back to the original ranking.
func (s *LLMDecisionService) EvaluateCandidates(ctx context.Context, req *RecommendRequest, topK int, candidates []RecommendedClip) (*LLMDecisionResult, error) {
	if !s.IsEnabled() {
		return nil, nil
	}

	if len(candidates) == 0 {
		return nil, nil
	}

	// Limit to top-K
	if len(candidates) > topK {
		candidates = candidates[:topK]
	}

	// 1. Build the prompt
	prompt := s.buildPrompt(req, candidates)

	// 2. Call the LLM
	messages := []types.Message{
		{Role: "system", Content: s.systemPrompt()},
		{Role: "user", Content: prompt},
	}

	llmCtx, cancel := context.WithTimeout(ctx, s.cfg.Timeout)
	defer cancel()

	raw, err := s.client.Chat(llmCtx, messages, map[string]any{
		"temperature": 0.2,
		"num_predict": 1024,
	})
	if err != nil {
		s.log.Warn("LLM decision failed, falling back to ranking",
			zap.Error(err))
		return nil, nil
	}

	// 3. Parse the response
	result := s.parseResponse(raw, candidates)
	if result == nil {
		s.log.Warn("LLM decision unparseable, falling back to ranking",
			zap.String("raw_preview", textutil.Truncate(raw, 200)))
		return nil, nil
	}

	// Set original rank/score
	for i, c := range candidates {
		if c.ClipID == result.ChosenClipID {
			result.OriginalRank = i + 1
			result.OriginalScore = c.Score
			break
		}
	}

	s.log.Info("LLM decision made",
		zap.String("clip_id", result.ChosenClipID),
		zap.String("rating", result.Rating),
		zap.Int("original_rank", result.OriginalRank),
		zap.String("reason", textutil.Truncate(result.Reason, 120)))

	return result, nil
}

// systemPrompt returns the system message for the LLM evaluator.
func (s *LLMDecisionService) systemPrompt() string {
	return `You are a professional video editor selecting the best stock footage clip for a documentary segment.

You evaluate each candidate clip based on:
1. VISUAL RELEVANCE: How well does the clip visually represent the segment topic?
2. NARRATIVE FIT: Does the clip's mood and style match the narrative tone?
3. TEXTUAL MATCH: How well does the clip's description match the search queries and visual prompts?
4. DIVERSITY: Is this clip visually distinct from previously used clips?

You must respond with valid JSON only — no markdown, no extra text.`
}

// buildPrompt constructs the evaluation prompt with segment context and candidate details.
func (s *LLMDecisionService) buildPrompt(req *RecommendRequest, candidates []RecommendedClip) string {
	var sb strings.Builder

	sb.WriteString("SELECT THE BEST CLIP FOR THIS SEGMENT\n\n")
	sb.WriteString("=== SEGMENT CONTEXT ===\n")
	sb.WriteString(fmt.Sprintf("Topic: %s\n", req.Topic))
	if req.SegmentText != "" {
		sb.WriteString(fmt.Sprintf("Narrative: %s\n", textutil.Truncate(req.SegmentText, 500)))
	}
	if len(req.Queries) > 0 {
		sb.WriteString(fmt.Sprintf("Search Queries: %s\n", strings.Join(req.Queries, ", ")))
	}
	if len(req.VisualPrompts) > 0 {
		sb.WriteString(fmt.Sprintf("Visual Prompts: %s\n", strings.Join(req.VisualPrompts, ", ")))
	}
	if len(req.EntityQueries) > 0 {
		sb.WriteString(fmt.Sprintf("Entity Queries: %s\n", strings.Join(req.EntityQueries, ", ")))
	}
	if req.Category != "" {
		sb.WriteString(fmt.Sprintf("Category: %s\n", req.Category))
	}
	if req.SceneType != "" {
		sb.WriteString(fmt.Sprintf("Scene Type: %s\n", req.SceneType))
	}
	sb.WriteString("\n")

	sb.WriteString("=== CANDIDATE CLIPS ===\n")
	for i, c := range candidates {
		sb.WriteString(fmt.Sprintf("\n--- Candidate %d (Score: %.2f) ---\n", i+1, c.Score))
		sb.WriteString(fmt.Sprintf("  Title: %s\n", c.Title))
		if c.Category != "" {
			sb.WriteString(fmt.Sprintf("  Category: %s\n", c.Category))
		}
		if len(c.MatchedTerms) > 0 {
			sb.WriteString(fmt.Sprintf("  Matched Terms: %s\n", strings.Join(c.MatchedTerms, ", ")))
		}
		if c.MatchedQuery != "" {
			sb.WriteString(fmt.Sprintf("  Matched Query: %s\n", c.MatchedQuery))
		}
		// Include score breakdown if available
		if c.ScoreBreakdown != nil {
			sb.WriteString(fmt.Sprintf("  Text Score: %.2f\n", c.ScoreBreakdown.TextScore))
			sb.WriteString(fmt.Sprintf("  Topic Boost: %.2f\n", c.ScoreBreakdown.TopicBoost))
			sb.WriteString(fmt.Sprintf("  Source Boost: %.2f\n", c.ScoreBreakdown.SourceBoost))
			if c.ScoreBreakdown.NegativePenalty > 0 {
				sb.WriteString(fmt.Sprintf("  Negative Penalty: %.2f (contains avoid terms)\n", c.ScoreBreakdown.NegativePenalty))
			}
			if c.ScoreBreakdown.ReusePenalty > 0 {
				sb.WriteString(fmt.Sprintf("  Reuse Penalty: %.2f (already used)\n", c.ScoreBreakdown.ReusePenalty))
			}
		}
		if c.Reason != "" {
			sb.WriteString(fmt.Sprintf("  System Reason: %s\n", c.Reason))
		}
		// Include the clip's source
		parts := strings.SplitN(c.ClipID, ":", 2)
		if len(parts) == 2 {
			sb.WriteString(fmt.Sprintf("  Source: %s\n", parts[0]))
		}
	}
	sb.WriteString("\n")

	sb.WriteString("=== YOUR TASK ===\n")
	sb.WriteString("Evaluate each candidate. Consider which clip best matches the segment's topic, narrative, and visual needs.\n\n")

	sb.WriteString("Respond with this exact JSON structure (no markdown, no extra text):\n")
	sb.WriteString(`{
  "chosen_clip_id": "the exact clip_id from the best candidate",
  "rating": "HIGH|MEDIUM|LOW",
  "reason": "A short, specific reason why this clip was chosen over the others",
  "chosen_new_score": a float from 0-100 representing your confidence (higher = better fit)
}`)

	return sb.String()
}

// parseResponse extracts the LLMDecisionResult from the LLM's JSON response.
func (s *LLMDecisionService) parseResponse(raw string, candidates []RecommendedClip) *LLMDecisionResult {
	// Extract JSON from the response (handle possible surrounding text/code blocks)
	cleaned := raw
	if idx := strings.Index(cleaned, "{"); idx != -1 {
		cleaned = cleaned[idx:]
	}
	if idx := strings.LastIndex(cleaned, "}"); idx != -1 {
		cleaned = cleaned[:idx+1]
	}

	// Remove markdown code fences if present
	cleaned = strings.TrimSpace(cleaned)
	cleaned = strings.TrimPrefix(cleaned, "```json")
	cleaned = strings.TrimPrefix(cleaned, "```")
	cleaned = strings.TrimSuffix(cleaned, "```")
	cleaned = strings.TrimSpace(cleaned)

	var result struct {
		ChosenClipID   string  `json:"chosen_clip_id"`
		Rating         string  `json:"rating"`
		Reason         string  `json:"reason"`
		ChosenNewScore float64 `json:"chosen_new_score"`
	}

	if err := json.Unmarshal([]byte(cleaned), &result); err != nil {
		return nil
	}

	// Validate: chosen_clip_id must exist in candidates
	valid := false
	for _, c := range candidates {
		if c.ClipID == result.ChosenClipID {
			valid = true
			break
		}
	}
	if !valid {
		return nil
	}

	// Normalize rating
	rating := strings.ToUpper(strings.TrimSpace(result.Rating))
	switch rating {
	case "HIGH", "MEDIUM", "LOW":
		// valid
	default:
		rating = "MEDIUM"
	}

	// Cap new score
	if result.ChosenNewScore < 0 {
		result.ChosenNewScore = 0
	}
	if result.ChosenNewScore > 100 {
		result.ChosenNewScore = 100
	}

	return &LLMDecisionResult{
		ChosenClipID:   result.ChosenClipID,
		Rating:         rating,
		Reason:         strings.TrimSpace(result.Reason),
		ChosenNewScore: result.ChosenNewScore,
	}
}
