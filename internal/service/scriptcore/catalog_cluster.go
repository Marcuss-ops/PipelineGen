package scriptcore

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"velox/go-master/internal/ml/ollama/types"
)

// clusterClipsViaLLM sends search results to the LLM for thematic clustering.
func (b *ClipSourceBuilder) clusterClipsViaLLM(ctx context.Context, topic string, clips []searchClipSummary, maxClips int) (*catalogLLMResponse, error) {
	prompt := buildCatalogClusterPrompt(topic, clips, maxClips)

	llmCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()

	response, err := b.ollamaCli.Chat(llmCtx, []types.Message{
		{Role: "system", Content: catalogSystemPrompt},
		{Role: "user", Content: prompt},
	}, map[string]any{
		"temperature": 0.1,
		"num_predict": 3072,
	})
	if err != nil {
		return nil, fmt.Errorf("LLM clustering call failed: %w", err)
	}

	result, err := parseCatalogResponse(response)
	if err != nil {
		return nil, fmt.Errorf("parse catalog response: %w", err)
	}

	return result, nil
}

func buildCatalogClusterPrompt(topic string, clips []searchClipSummary, maxClips int) string {
	var b strings.Builder

	b.WriteString(fmt.Sprintf("Analyze the following %d video clips related to \"%s\".\n\n", len(clips), topic))
	b.WriteString("CLIPS:\n\n")

	for i, clip := range clips {
		b.WriteString(fmt.Sprintf("--- CLIP %d ---\n", i+1))
		b.WriteString(fmt.Sprintf("ID: %s\n", clip.ID))
		b.WriteString(fmt.Sprintf("Title: %s\n", clip.Name))
		b.WriteString(fmt.Sprintf("Summary: %s\n", clip.Summary))
		if clip.Topics != "" && clip.Topics != "[]" {
			b.WriteString(fmt.Sprintf("Topics: %s\n", clip.Topics))
		}
		if clip.Quality > 0 {
			b.WriteString(fmt.Sprintf("Quality: %.2f\n", clip.Quality))
		}
		b.WriteString("\n")
	}

	b.WriteString(fmt.Sprintf(`
Your task:
1. Group these clips into thematic clusters based on their content.
2. For each cluster, suggest a narrative role: "main", "supporting", "transition", "closing".
3. Rate coverage as "sufficient" (enough clips for this theme), "partial" (some clips but thin), or "insufficient" (too few to use).
4. Select at most %d clips total, prioritizing the best clusters.
5. Consider clip quality, summary relevance, and topic alignment.

Respond with valid JSON only — no markdown, no extra text:
{
  "title": "Suggested documentary title",
  "clusters": [
    {
      "theme": "Cluster theme name",
      "clip_ids": ["id1", "id2"],
      "role": "main|supporting|transition|closing",
      "coverage": "sufficient|partial|insufficient",
      "reason": "Why this cluster fits the narrative"
    }
  ],
  "warnings": ["optional warning about missing coverage or gaps"]
}`, maxClips))

	return b.String()
}

const catalogSystemPrompt = `You are a professional documentary editor and catalog analyst. Your expertise is:
1. Analyzing video clip catalogs and finding thematic patterns
2. Grouping clips into narrative clusters that tell a coherent story
3. Rating coverage quality for each theme
4. Recommending the best subset of clips for a documentary script

You are conservative: prefer "partial" or "insufficient" coverage over overclaiming.
You ALWAYS respond with valid JSON only — no markdown, no extra text.`

func parseCatalogResponse(raw string) (*catalogLLMResponse, error) {
	cleaned := raw
	if idx := strings.Index(cleaned, "{"); idx != -1 {
		cleaned = cleaned[idx:]
	}
	if idx := strings.LastIndex(cleaned, "}"); idx != -1 {
		cleaned = cleaned[:idx+1]
	}
	cleaned = strings.TrimSpace(cleaned)
	cleaned = strings.TrimPrefix(cleaned, "```json")
	cleaned = strings.TrimPrefix(cleaned, "```")
	cleaned = strings.TrimSuffix(cleaned, "```")
	cleaned = strings.TrimSpace(cleaned)

	var result catalogLLMResponse
	if err := json.Unmarshal([]byte(cleaned), &result); err != nil {
		return nil, fmt.Errorf("failed to parse catalog JSON: %w", err)
	}

	// Basic validation
	if len(result.Clusters) == 0 {
		return nil, fmt.Errorf("LLM returned zero clusters")
	}

	return &result, nil
}
