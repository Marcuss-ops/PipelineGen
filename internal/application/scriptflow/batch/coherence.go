package batch

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/ollama/prompts"
	ollamatypes "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/ollama/types"
	retry "github.com/Marcuss-ops/PipelineGen/pkg/retry"
	textutil "github.com/Marcuss-ops/PipelineGen/pkg/textutil"

	"go.uber.org/zap"
)

// minCoherenceWords is the minimum total script length for which the
// coherence pass is worth running. Very short scripts rarely have
// repetition / transition issues.
const minCoherenceWords = 500

// coherenceTolerancePercent is the maximum allowed word-count deviation
// after the coherence pass. If the corrected script deviates more than
// this from the original, we fall back to the merged script.
const coherenceTolerancePercent = 10

// coherencePass runs a post-merge coherence check on the full script.
// It asks the LLM to remove repetitions, strengthen transitions, and
// ensure tonal consistency without adding new facts.
func (s *BatchService) coherencePass(
	ctx context.Context,
	req *GenerateBatchRequest,
	mergedScript string,
) (string, error) {
	if s.generator == nil || strings.TrimSpace(mergedScript) == "" {
		return mergedScript, nil
	}

	originalWords := textutil.CountWords(mergedScript)
	if originalWords < minCoherenceWords {
		return mergedScript, nil
	}

	prompt := buildCoherencePrompt(mergedScript, req.Language, req.DocTitle)

	// Dynamic num_predict: give the model enough room to emit the whole
	// corrected script plus a small margin. Never exceed 8000.
	predictLimit := originalWords * 2
	if predictLimit < 4096 {
		predictLimit = 4096
	}
	if predictLimit > 8000 {
		predictLimit = 8000
	}

	// Use a generous timeout because the coherence pass reads the full script.
	coherenceCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()

	var res *ollamatypes.GenerationResult
	genErr := retry.Do(coherenceCtx, func() error {
		var innerErr error
		res, innerErr = s.generator.GenerateScript(coherenceCtx, ollamatypes.TextGenerationRequest{
			Language:   req.Language,
			Tone:       req.Tone,
			Model:      req.Model,
			Prompt:     prompt,
			SourceText: mergedScript,
			Title:      req.DocTitle,
			Options:    map[string]any{"num_predict": predictLimit},
		})
		if innerErr != nil {
			return fmt.Errorf("coherence pass generation attempt failed: %w", innerErr)
		}
		return nil
	}, retry.RetryOptions{
		MaxAttempts:    2,
		InitialBackoff: 3 * time.Second,
		BackoffFactor:  2.0,
	})
	if genErr != nil {
		return mergedScript, fmt.Errorf("coherence pass generation failed after retries: %w", genErr)
	}

	corrected := strings.TrimSpace(res.Script)
	if corrected == "" {
		return mergedScript, fmt.Errorf("coherence pass returned empty output")
	}

	correctedWords := textutil.CountWords(corrected)
	deviation := abs(correctedWords - originalWords)
	if originalWords > 0 && (deviation*100/originalWords) > coherenceTolerancePercent {
		s.log.Warn("coherence pass deviated too much from original word count, falling back",
			zap.Int("original", originalWords),
			zap.Int("corrected", correctedWords),
			zap.Int("deviation_percent", deviation*100/originalWords),
		)
		return mergedScript, fmt.Errorf("coherence pass deviated %d%% from original word count", deviation*100/originalWords)
	}

	return corrected, nil
}

func abs(a int) int {
	if a < 0 {
		return -a
	}
	return a
}

// chapterHeaderPattern matches the multilingual chapter markers produced by
// mergeBatchResults (e.g. "## Chapter 1: Topic", "## Capitolo 1: Topic").
// It captures the topic text after the chapter number.
var chapterHeaderPattern = regexp.MustCompile(`(?m)^##\s+(?:Chapter|Capitolo|Chapitre|Capítulo|Kapitel)\s+\d+:\s*(.*)$`)

// rebuildGeneratedPartsFromMergedScript parses a merged script back into a
// slice of GeneratedPart so that the Doc, translations, and DB save all use
// the same coherent text.  For noChapters mode the entire script (minus the
// title line) becomes a single part.
func rebuildGeneratedPartsFromMergedScript(docTitle, script string, noChapters bool, language string) []GeneratedPart {
	if strings.TrimSpace(script) == "" {
		return nil
	}
	// Drop the top-level "# Title" line that mergeBatchResults adds.
	script = strings.TrimSpace(script)
	if strings.HasPrefix(script, "# ") {
		if idx := strings.Index(script, "\n"); idx != -1 {
			script = strings.TrimSpace(script[idx+1:])
		}
	}

	if noChapters {
		return []GeneratedPart{{
			topic:   docTitle,
			content: script,
		}}
	}

	matches := chapterHeaderPattern.FindAllStringIndex(script, -1)
	if len(matches) == 0 {
		// No chapter headers found — treat as a single coherent part.
		return []GeneratedPart{{
			topic:   docTitle,
			content: script,
		}}
	}

	var parts []GeneratedPart
	for i, match := range matches {
		headerStart := match[0]
		headerEnd := match[1]
		topic := strings.TrimSpace(script[headerStart+3 : headerEnd]) // strip "## "
		// Remove the "Chapter N: " prefix from the topic.
		if idx := strings.Index(topic, ": "); idx != -1 {
			topic = strings.TrimSpace(topic[idx+2:])
		}

		contentStart := headerEnd
		contentEnd := len(script)
		if i+1 < len(matches) {
			contentEnd = matches[i+1][0]
		}
		content := strings.TrimSpace(script[contentStart:contentEnd])
		// Strip the trailing "---" separator that mergeBatchResults adds.
		content = strings.TrimSuffix(content, "---")
		content = strings.TrimSpace(content)
		if content == "" {
			continue
		}
		parts = append(parts, GeneratedPart{
			topic:   topic,
			content: content,
		})
	}
	return parts
}

// buildCoherencePrompt creates a controlled editing prompt that tells the
// LLM exactly what to fix (repetitions, transitions, tone) and what NOT
// to do (add facts, change meaning, add meta-text).
func buildCoherencePrompt(script, language, title string) string {
	if cfg := prompts.Get(); cfg != nil {
		rendered, err := cfg.RenderCoherencePass(script, language, title)
		if err == nil {
			return rendered
		}
	}
	// Fallback
	var b strings.Builder
	b.WriteString("You are a professional documentary script editor. ")
	b.WriteString("Your task is to improve the coherence and continuity of the following script. ")
	b.WriteString("You must NOT rewrite the entire script from scratch; only apply the specific fixes listed below.\n\n")
	b.WriteString("FIXES TO APPLY:\n")
	b.WriteString("1. Remove exact or near-exact repetitions of sentences or ideas between adjacent sections.\n")
	b.WriteString("2. Strengthen weak transitions between sections so the narrative flows naturally.\n")
	b.WriteString("3. Ensure the tone is consistent from the opening to the closing.\n")
	b.WriteString("4. Verify the opening is strong and engaging; verify the closing is not generic or recycled.\n")
	b.WriteString("5. If a section ends with a hook or cliffhanger, make sure the next section picks it up naturally.\n\n")
	b.WriteString("RULES:\n")
	b.WriteString("- Do NOT add new facts, data, or examples not already present in the script.\n")
	b.WriteString("- Do NOT change the meaning or argument of any section.\n")
	b.WriteString("- Do NOT add meta-text, chapter labels, timestamps, or speaker labels.\n")
	b.WriteString("- Do NOT shorten the script significantly; preserve all substantive content.\n")
	b.WriteString("- Return ONLY the corrected full script text, with no introductory or closing remarks.\n\n")
	if title != "" {
		b.WriteString(fmt.Sprintf("DOCUMENT TITLE: %s\n\n", title))
	}
	b.WriteString("SCRIPT:\n")
	b.WriteString(script)
	return b.String()
}
