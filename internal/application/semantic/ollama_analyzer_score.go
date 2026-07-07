package semantic

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"go.uber.org/zap"
)

// Score satisfies monitor.VideoAnalyzer.
//
// Builds the score prompt from the transcript + topic list, calls Ollama,
// parses the JSON response with a markdown-fallback regex search.
// Score is clamped to 0..100 (LLM drift is rare but documented).
//
// Errors:
//   - "ollama.SimpleGenerate: ..." (subprocess + JSON parse fallback path)
//   - "parse ollama response (fallback also failed): ..." (when the
//     primary parse + markdown fallback both fail)
func (a *OllamaAnalyzer) Score(ctx context.Context, transcript string, keywords []string) (int, string, error) {
	if a.ollamaClient == nil {
		return 0, "", fmt.Errorf("OllamaAnalyzer.Score: ollama client not wired (composition bug — pass root.AI.OllamaClient into semantic.NewOllamaAnalyzer)")
	}

	keywordsStr := strings.Join(keywords, ", ")
	prompt := fmt.Sprintf(`You are a content classifier. Analyze this video transcript and determine if the video discusses any of these topics: %s.

Transcript:
%s

Respond with a JSON object ONLY, no other text:
{
  "score": <0-100 integer>,
  "matched_keyword": "<the single best-matching keyword or empty string if none>",
  "reason": "<one-sentence justification>"
}

Rules:
- Score 0 = not relevant at all
- Score 100 = entirely about the topic
- Pick the single best-matching keyword from the list (empty if none matches)
- Consider the entire transcript, not just the first few lines.`, keywordsStr, transcript)

	responseStr, err := a.ollamaClient.SimpleGenerate(ctx, a.model, prompt, 60*time.Second, map[string]any{"format": "json"})
	if err != nil {
		return 0, "", fmt.Errorf("ollama call: %w", err)
	}

	var parsed struct {
		Score          int    `json:"score"`
		MatchedKeyword string `json:"matched_keyword"`
		Reason         string `json:"reason"`
	}
	if err := json.Unmarshal([]byte(responseStr), &parsed); err != nil {
		// Ollama sometimes wraps responses in markdown fences — fall back
		// to the JSON-regex search before declaring parse failure.
		if jsonMatch := jsonRegexFind([]byte(responseStr)); jsonMatch != nil {
			if err2 := json.Unmarshal(jsonMatch, &parsed); err2 != nil {
				return 0, "", fmt.Errorf("parse ollama response (fallback also failed): %w, raw: %s", err, responseStr)
			}
		} else {
			return 0, "", fmt.Errorf("parse ollama response: %w, raw: %s", err, responseStr)
		}
	}

	// Clamp score to the documented 0..100 range (LLM drift tolerated).
	if parsed.Score < 0 || parsed.Score > 100 {
		parsed.Score = 0
	}

	a.log.Debug("OllamaAnalyzer.Score result",
		zap.Int("score", parsed.Score),
		zap.String("matched_keyword", parsed.MatchedKeyword),
		zap.String("reason", parsed.Reason))
	return parsed.Score, parsed.MatchedKeyword, nil
}

// jsonRegexFind attempts to extract a JSON object from a string that
// may be wrapped in markdown. Sole consumer is Score above (FindSegments
// uses inline strings.Index/LastIndex for array extraction).
//
// Migrated unchanged from pre-Step-9 monitor/vtt_helpers.go where it
// was the JSON-parse fallback for the Score (object-shape) flow.
func jsonRegexFind(data []byte) []byte {
	s := string(data)
	// Try { ... } block (Score response shape).
	start := strings.Index(s, "{")
	if start >= 0 {
		end := strings.LastIndex(s, "}")
		if end > start {
			return []byte(s[start : end+1])
		}
	}
	return nil
}
