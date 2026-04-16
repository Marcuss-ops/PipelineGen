// Package artlistpipeline handles the full Artlist pipeline: text → clips → video.
package artlistpipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"velox/go-master/internal/ml/ollama"
	"velox/go-master/pkg/logger"

	"go.uber.org/zap"
)

// QueryExpander expands text segments into multiple Artlist search queries.
type QueryExpander struct {
	client *ollama.Client
}

// NewQueryExpander creates a new query expander.
func NewQueryExpander(client *ollama.Client) *QueryExpander {
	return &QueryExpander{client: client}
}

// ExpandedQuery represents the result of query expansion for a single segment.
type ExpandedQuery struct {
	SegmentText string   `json:"segment_text"`
	SegmentIdx  int      `json:"segment_idx"`
	Queries     []string `json:"queries"`
	Topic       string   `json:"topic"`
	Actions     []string `json:"actions"`
	Objects     []string `json:"objects"`
	Mood        string   `json:"mood"`
	Style       string   `json:"style"`
}

// SegmentAnalysis is the LLM output structure.
type SegmentAnalysis struct {
	Topic   string   `json:"topic"`
	Actions []string `json:"actions"`
	Objects []string `json:"objects"`
	Mood    string   `json:"mood"`
	Style   string   `json:"style"`
	Queries []string `json:"queries"`
}

// ExpandQueries generates 5-7 expanded search queries for a text segment.
// Example: "boxe" → ["boxing ring", "punching bag training", "fighter shadowboxing", "gym sweat slow motion"]
func (qe *QueryExpander) ExpandQueries(ctx context.Context, segmentText string, segmentIdx int) (*ExpandedQuery, error) {
	analysis, err := qe.analyzeSegment(ctx, segmentText)
	if err != nil {
		return nil, fmt.Errorf("failed to analyze segment: %w", err)
	}

	return &ExpandedQuery{
		SegmentText: segmentText,
		SegmentIdx:  segmentIdx,
		Queries:     analysis.Queries,
		Topic:       analysis.Topic,
		Actions:     analysis.Actions,
		Objects:     analysis.Objects,
		Mood:        analysis.Mood,
		Style:       analysis.Style,
	}, nil
}

// analyzeSegment calls the LLM to extract semantic features and generate queries.
func (qe *QueryExpander) analyzeSegment(ctx context.Context, text string) (*SegmentAnalysis, error) {
	prompt := fmt.Sprintf(`Analyze this text segment and generate search queries for finding stock video clips.

TEXT: "%s"

Extract:
1. Main topic (1-2 words)
2. Key actions/verbs happening (2-4 words each)
3. Key objects/entities mentioned (1-3 words each)
4. Mood/vibe (e.g., dramatic, energetic, calm, intense)
5. Visual style (e.g., slow motion, close-up, wide shot, cinematic)

Then generate 5-7 English search queries optimized for Artlist stock footage.
Queries should be specific and varied, not just the topic.
Examples:
- "boxe" → "boxing ring", "punching bag training", "fighter shadowboxing", "gym sweat slow motion", "boxing gloves closeup"
- "cucina italiana" → "italian chef cooking", "pasta preparation hands", "kitchen ingredients overhead", "restaurant plating slow motion"

Respond with VALID JSON only (no markdown, no explanation):
{"topic":"...", "actions":["..."], "objects":["..."], "mood":"...", "style":"...", "queries":["...","...","...","...","..."]}`, text)

	response, err := qe.client.Generate(ctx, prompt)
	if err != nil {
		return nil, fmt.Errorf("LLM generation failed: %w", err)
	}

	analysis, err := parseAnalysis(response)
	if err != nil {
		// Fallback: use simple keyword extraction
		logger.Warn("LLM parse failed, using fallback", zap.Error(err))
		return qe.fallbackAnalysis(text), nil
	}

	// Ensure we have at least 5 queries
	if len(analysis.Queries) < 5 {
		analysis.Queries = append(analysis.Queries, qe.generateFallbackQueries(text, 5-len(analysis.Queries))...)
	}

	// Limit to 7 queries max
	if len(analysis.Queries) > 7 {
		analysis.Queries = analysis.Queries[:7]
	}

	logger.Info("Query expansion completed",
		zap.String("segment", text[:minInt(50, len(text))]),
		zap.Int("queries", len(analysis.Queries)))

	return analysis, nil
}

// fallbackAnalysis extracts simple keywords when LLM fails.
func (qe *QueryExpander) fallbackAnalysis(text string) *SegmentAnalysis {
	var nouns []string
	var verbs []string

	words := strings.Fields(text)
	for _, w := range words {
		w = strings.ToLower(strings.Trim(w, ".,;:!?\"'()[]{}"))
		if len(w) > 3 && isLikelyNoun(w) {
			nouns = append(nouns, w)
		}
		if isLikelyVerb(w) {
			verbs = append(verbs, w)
		}
	}

	queries := []string{strings.Join(nouns, " ")}
	if len(verbs) > 0 {
		queries = append(queries, strings.Join(verbs, " ")+" "+strings.Join(nouns, " "))
	}
	queries = append(queries, qe.generateFallbackQueries(text, 4)...)

	return &SegmentAnalysis{
		Topic:   strings.Join(nouns, " "),
		Actions: verbs,
		Objects: nouns,
		Mood:    "dynamic",
		Style:   "cinematic",
		Queries: queries[:minInt(7, len(queries))],
	}
}

// generateFallbackQueries creates basic queries from text.
func (qe *QueryExpander) generateFallbackQueries(text string, count int) []string {
	var queries []string

	// Common stock footage search patterns
	patterns := []string{
		"%s slow motion",
		"%s closeup",
		"%s cinematic",
		"%s professional",
		"%s action",
	}

	cleaned := strings.ToLower(strings.Trim(text, ".,;:!?\"'()[]{}"))
	for i := 0; i < count && i < len(patterns); i++ {
		queries = append(queries, fmt.Sprintf(patterns[i], cleaned))
	}

	return queries
}

// parseAnalysis parses the LLM response into a SegmentAnalysis struct.
func parseAnalysis(response string) (*SegmentAnalysis, error) {
	// Remove markdown code blocks if present
	re := regexp.MustCompile("```(?:json)?\\s*")
	cleaned := re.ReplaceAllString(response, "")
	cleaned = strings.TrimSpace(cleaned)

	// Try to find JSON object
	start := strings.Index(cleaned, "{")
	end := strings.LastIndex(cleaned, "}")
	if start == -1 || end == -1 || end <= start {
		return nil, fmt.Errorf("no JSON object found in response")
	}

	jsonStr := cleaned[start : end+1]

	var analysis SegmentAnalysis
	if err := json.Unmarshal([]byte(jsonStr), &analysis); err != nil {
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}

	// Validate required fields
	if analysis.Topic == "" {
		return nil, fmt.Errorf("missing topic")
	}
	if len(analysis.Queries) == 0 {
		return nil, fmt.Errorf("missing queries")
	}

	return &analysis, nil
}

// isLikelyNoun checks if a word is likely to be a noun (simplified).
func isLikelyNoun(word string) bool {
	// Simple heuristic: common Italian/English nouns
	commonNouns := map[string]bool{
		"boxe": true, "boxing": true, "ring": true, "fight": true, "fighter": true,
		"gym": true, "palestra": true, "cucina": true, "kitchen": true, "chef": true,
		"business": true, "meeting": true, "office": true, "city": true, "città": true,
		"nature": true, "natura": true, "mountain": true, "montagna": true,
		"technology": true, "tech": true, "computer": true, "telefono": true,
	}
	return commonNouns[word] || len(word) > 4
}

// isLikelyVerb checks if a word is likely to be a verb (simplified).
func isLikelyVerb(word string) bool {
	commonVerbs := map[string]bool{
		"training": true, "allenamento": true, "cooking": true, "cucinare": true,
		"running": true, "correre": true, "working": true, "lavorare": true,
		"fighting": true, "combattere": true, "presenting": true, "presentare": true,
	}
	return commonVerbs[word]
}
