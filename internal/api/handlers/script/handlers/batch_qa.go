package handlers

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/ai/ollama/prompts"
	ollamatypes "github.com/Marcuss-ops/PipelineGen/internal/platform/ai/ollama/types"
	"github.com/Marcuss-ops/PipelineGen/pkg/retry"
	"github.com/Marcuss-ops/PipelineGen/pkg/textutil"

	"go.uber.org/zap"
)

// minQAWords is the minimum total script length for which the QA pass is
// worth running. Very short scripts rarely have the structural issues the
// QA pass is designed to catch.
const minQAWords = 500

// qaTolerancePercent is the maximum allowed word-count deviation after the
// QA pass. If the corrected script deviates more than this from the original,
// we fall back to the input script.
const qaTolerancePercent = 10

// qaPass runs a global quality check on the full merged script.
// It verifies factual accuracy, opening/closing strength, generic endings,
// and global redundancy without adding new facts.
func (h *ScriptFlowHandler) qaPass(
	ctx context.Context,
	req *GenerateBatchRequest,
	script string,
) (string, error) {
	if h.generator == nil || strings.TrimSpace(script) == "" {
		return script, nil
	}

	originalWords := textutil.CountWords(script)
	if originalWords < minQAWords {
		return script, nil
	}

	prompt := buildQAPrompt(script, req.Language, req.DocTitle, req.Tone)

	predictLimit := originalWords * 2
	if predictLimit < 4096 {
		predictLimit = 4096
	}
	if predictLimit > 8000 {
		predictLimit = 8000
	}

	qaCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()

	var res *ollamatypes.GenerationResult
	genErr := retry.Do(qaCtx, func() error {
		var innerErr error
		res, innerErr = h.generator.GenerateScript(qaCtx, ollamatypes.TextGenerationRequest{
			Language:   req.Language,
			Tone:       req.Tone,
			Model:      req.Model,
			Prompt:     prompt,
			SourceText: script,
			Title:      req.DocTitle,
			Options:    map[string]any{"num_predict": predictLimit},
		})
		if innerErr != nil {
			return fmt.Errorf("qa pass generation attempt failed: %w", innerErr)
		}
		return nil
	}, retry.Options{
		MaxAttempts:    2,
		InitialBackoff: 3 * time.Second,
		BackoffFactor:  2.0,
	})
	if genErr != nil {
		return script, fmt.Errorf("qa pass generation failed after retries: %w", genErr)
	}

	corrected := strings.TrimSpace(res.Script)
	if corrected == "" {
		return script, fmt.Errorf("qa pass returned empty output")
	}

	correctedWords := textutil.CountWords(corrected)
	deviation := abs(correctedWords - originalWords)
	if originalWords > 0 && (deviation*100/originalWords) > qaTolerancePercent {
		h.log.Warn("qa pass deviated too much from original word count, falling back",
			zap.Int("original", originalWords),
			zap.Int("corrected", correctedWords),
			zap.Int("deviation_percent", deviation*100/originalWords),
		)
		return script, fmt.Errorf("qa pass deviated %d%% from original word count", deviation*100/originalWords)
	}

	return corrected, nil
}

// buildQAPrompt creates a controlled quality-review prompt that tells the
// LLM exactly what to check (factual accuracy, opening/closing, generic
// endings, redundancy) and what NOT to do (add facts, change meaning).
func buildQAPrompt(script, language, title, tone string) string {
	if cfg := prompts.Get(); cfg != nil {
		rendered, err := cfg.RenderQAPass(script, language, title, tone)
		if err == nil {
			return rendered
		}
	}
	// Fallback
	var b strings.Builder
	b.WriteString("You are a senior documentary script quality reviewer. ")
	b.WriteString("Your task is to review and improve the following script. ")
	b.WriteString("You must NOT rewrite the entire script from scratch; only apply the specific fixes listed below.\n\n")

	b.WriteString("QUALITY CHECKS TO APPLY:\n")
	b.WriteString("1. FACTUAL CONSISTENCY: Verify that every claim, date, number, or statistic in the script is internally consistent. If a claim contradicts an earlier statement or looks invented without support, remove it or replace it with a more cautious phrasing.\n")
	b.WriteString("2. OPENING STRENGTH: The first paragraph must be engaging and immediately relevant to the topic. If it starts with a generic filler (e.g., \"In today's world...\", \"Since the beginning of time...\"), rewrite it with a concrete hook or story.\n")
	b.WriteString("3. CLOSING STRENGTH: The final paragraph must not be a recycled generic conclusion (e.g., \"In conclusion...\", \"As we have seen...\"). It should end with a memorable takeaway, a call to action, or a forward-looking insight specific to the topic.\n")
	b.WriteString("4. GENERIC ENDING DETECTION: If the last 2-3 sentences are vague platitudes that could apply to any topic, replace them with specific, substantive closing thoughts.\n")
	b.WriteString("5. GLOBAL REDUNDANCY: If the same idea or example is repeated in non-adjacent sections, remove the weaker repetition.\n")
	b.WriteString("6. TONE CONSISTENCY: Ensure the entire script maintains the requested tone (e.g., documentary, educational, narrative). Fix any sudden shifts in style.\n")
	b.WriteString("7. STRUCTURAL CHECK: Verify the script follows a logical progression: hook → context → core content → resolution → strong closing. Fix any section that breaks this flow.\n\n")

	b.WriteString("RULES:\n")
	b.WriteString("- Do NOT add new facts, data, or examples not already present in the script.\n")
	b.WriteString("- Do NOT change the meaning or argument of any section.\n")
	b.WriteString("- Do NOT add meta-text, chapter labels, timestamps, or speaker labels.\n")
	b.WriteString("- Do NOT shorten the script significantly; preserve all substantive content.\n")
	b.WriteString("- Return ONLY the corrected full script text, with no introductory or closing remarks.\n\n")

	if tone != "" {
		b.WriteString(fmt.Sprintf("REQUESTED TONE: %s\n\n", tone))
	}
	if title != "" {
		b.WriteString(fmt.Sprintf("DOCUMENT TITLE: %s\n\n", title))
	}
	b.WriteString("SCRIPT:\n")
	b.WriteString(script)
	return b.String()
}
