package ollama

import (
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/ollama/client"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/ollama/types"
)

func estimateDurationSecondsWithWPM(wordCount, wordsPerMinute int) int {
	if wordCount <= 0 {
		return 0
	}
	if wordsPerMinute <= 0 {
		wordsPerMinute = types.WordsPerMinute // compatibility for non-pipeline callers
	}
	return (wordCount * 60) / wordsPerMinute
}

func setTextDefaults(req *types.TextGenerationRequest) {
	types.ApplyDefaults(req)
}

// SearchQueryForScript builds a web search query from the script request.
// Returns empty string if the request doesn't benefit from web search
// (e.g. when the source text is substantial enough on its own).
func SearchQueryForScript(req types.TextGenerationRequest) string {
	// Skip web search if the source text is substantial (user already provided context)
	if len(strings.TrimSpace(req.SourceText)) > 500 {
		return ""
	}

	// Use the title/topic as the primary search query
	query := strings.TrimSpace(req.Title)
	if query == "" {
		query = strings.TrimSpace(req.Prompt)
	}
	if query == "" {
		q := strings.TrimSpace(req.SourceText)
		if len(q) > 200 {
			q = q[:200]
		}
		query = q
	}
	if query == "" {
		return ""
	}

	return client.SearchQueryFromTopic(query)
}
