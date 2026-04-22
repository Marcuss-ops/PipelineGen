package scriptdocs

import (
	"context"
	"fmt"
	"strings"
	"time"

	"velox/go-master/pkg/logger"
	"go.uber.org/zap"
)

// extractSemanticVisualTags uses the LLM to identify exactly 3 visual tags for a given phrase.
// These tags are used for high-precision clip matching and dynamic searching.
func (s *ScriptDocService) extractSemanticVisualTags(ctx context.Context, frase, topic string) []string {
	if s.generator == nil || len(frase) < 10 {
		return nil
	}

	// Use a single prompt to get exactly 3 tags
	prompt := fmt.Sprintf(`As a film director, identify the 3 most important visual concepts or objects to show for this script line.
Topic: %s
Sentence: "%s"

Respond ONLY with the 3 concepts in English, separated by commas.
Example: mountain, hiker, sunset`, topic, frase)

	// Short timeout for performance
	genCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()

	resp, err := s.generator.Generate(genCtx, prompt)
	if err != nil {
		logger.Warn("LLM semantic tagging failed", zap.Error(err), zap.String("phrase", truncate(frase, 50)))
		return nil
	}

	// Parse response
	rawTags := strings.Split(resp, ",")
	var tags []string
	for _, t := range rawTags {
		clean := strings.ToLower(strings.TrimSpace(t))
		// Basic cleanup of punctuation
		clean = strings.Trim(clean, ".\"'-")
		if clean != "" && len(clean) > 2 {
			tags = append(tags, clean)
		}
	}

	// Limit to 3 tags as requested
	if len(tags) > 3 {
		tags = tags[:3]
	}

	if len(tags) > 0 {
		logger.Info("Semantic visual tags extracted", 
			zap.String("phrase", truncate(frase, 50)), 
			zap.Strings("tags", tags),
		)
	}

	return tags
}
