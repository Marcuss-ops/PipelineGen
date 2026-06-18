package prompts

import (
	"fmt"
	"strings"
	"github.com/Marcuss-ops/PipelineGen/internal/ml/ollama/types"
)

// BuildChatMessages builds the message list for the chat API.
// If req.WebContext is non-empty, it is prepended as RAG context.
func BuildChatMessages(req *types.TextGenerationRequest) []types.Message {
	durationMinutes := req.DurationMinutes
	if durationMinutes == 0 && req.Duration > 0 {
		durationMinutes = req.Duration / 60
	}
	if durationMinutes == 0 {
		durationMinutes = 1
	}

	targetWords := durationMinutes * types.WordsPerMinute
	if req.MinWords > 0 {
		targetWords = req.MinWords
	}

	sanitizedSource := types.SanitizeInput(req.SourceText)
	sanitizedTitle := types.SanitizeInput(req.Title)

	var userContent string
	if cfg := Get(); cfg != nil {
		rendered, err := cfg.RenderScriptGeneration(
			req.Duration, durationMinutes, sanitizedTitle, req.Tone,
			sanitizedSource, targetWords,
		)
		if err == nil {
			userContent = rendered
		}
	}
	if userContent == "" {
		userContent = fmt.Sprintf(`TASK: Write a true NARRATIVE DOCUMENTARY of %d seconds (about %d minutes).

VIDEO TITLE: %s
NARRATIVE STYLE: %s

REFERENCE INPUT / INSTRUCTIONS:
"%s"

STRICT QUALITY REQUIREMENTS (FAILURE IS NOT AN OPTION):
1. LENGTH: This video lasts %d minutes. You MUST write at least %d words.
2. STYLE: Cinematic and immersive.
3. FORMAT: Write as straight continuous prose only.
4. NO META-TEXT: Write ONLY the spoken script.
5. NO TIMESTAMPS: Do not include ANY time markers like [0:00], (0:15), [INIZIO], or ranges.
6. NO SPEAKER LABELS: Do NOT write "Narrator:", "Narratore:", "Voice:", "Voce:", or any other label. Start directly with the story.
7. NO STAGE DIRECTIONS: Do not include descriptions of shots, music, or tone in brackets.

SCRIPT:`, req.Duration, durationMinutes, sanitizedTitle, req.Tone, sanitizedSource, durationMinutes, targetWords)
	}

	if req.Prompt != "" && req.Prompt != req.SourceText {
		userContent = prependOverriding(req.Prompt) + userContent
	}

	if req.WebContext != "" {
		userContent = req.WebContext + "\n" + userContent
	}

	return []types.Message{
		{Role: "system", Content: BuildSystemPrompt(req.Language, req.Tone)},
		{Role: "user", Content: userContent},
	}
}

// BuildRegenerationChatMessages builds the message list for script regeneration.
func BuildRegenerationChatMessages(req *types.RegenerationRequest) []types.Message {
	sanitizedScript := types.SanitizeInput(req.OriginalScript)
	sanitizedTitle := types.SanitizeInput(req.Title)

	var userContent string
	if cfg := Get(); cfg != nil {
		rendered, err := cfg.RenderScriptRegeneration(sanitizedTitle, req.Tone, sanitizedScript)
		if err == nil {
			userContent = rendered
		}
	}
	if userContent == "" {
		userContent = fmt.Sprintf(`Rewrite the following documentary script in a cleaner, more compelling form.

VIDEO TITLE: %s
NARRATIVE STYLE: %s

SCRIPT TO REWRITE:
"%s"

STRICT RULES:
1. Return ONLY the rewritten spoken script.
2. Keep it as straight continuous prose.
3. Do not add timestamps, headings, labels, or stage directions.
4. Preserve the original subject and factual content unless the rewrite improves clarity or flow.

SCRIPT:`, sanitizedTitle, req.Tone, sanitizedScript)
	}

	return []types.Message{
		{Role: "system", Content: BuildSystemPrompt(req.Language, req.Tone)},
		{Role: "user", Content: userContent},
	}
}

// BuildTextPrompt builds the prompt for text generation (no chat messages wrapper).
func BuildTextPrompt(req *types.TextGenerationRequest) string {
	durationMinutes := req.DurationMinutes
	if durationMinutes == 0 && req.Duration > 0 {
		durationMinutes = req.Duration / 60
	}
	if durationMinutes == 0 {
		durationMinutes = 1
	}

	targetWords := durationMinutes * types.WordsPerMinute
	if req.MinWords > 0 {
		targetWords = req.MinWords
	}

	sanitizedSource := types.SanitizeInput(req.SourceText)
	sanitizedTitle := types.SanitizeInput(req.Title)

	var mainPrompt string
	if cfg := Get(); cfg != nil {
		rendered, err := cfg.RenderScriptGeneration(
			req.Duration, durationMinutes, sanitizedTitle, req.Tone,
			sanitizedSource, targetWords,
		)
		if err == nil {
			mainPrompt = BuildSystemPrompt(req.Language, req.Tone) + "\n\n" + rendered
		}
	}

	if mainPrompt == "" {
		mainPrompt = fmt.Sprintf(`%s

TASK: Write a true NARRATIVE DOCUMENTARY of %d seconds (about %d minutes).

VIDEO TITLE: %s
NARRATIVE STYLE: %s

REFERENCE INPUT / INSTRUCTIONS:
"%s"

STRICT QUALITY REQUIREMENTS (FAILURE IS NOT AN OPTION):
1. LENGTH: This video lasts %d minutes. You MUST write at least %d words.
2. STYLE: Cinematic and immersive.
3. FORMAT: Write as straight continuous prose only.
4. NO META-TEXT: Write ONLY the spoken script.
5. NO TIMESTAMPS: Do not include ANY time markers like [0:00], (0:15), [INIZIO], or ranges.
6. NO SPEAKER LABELS: Do NOT write "Narrator:", "Narratore:", "Voice:", "Voce:", or any other label. Start directly with the story.
7. NO STAGE DIRECTIONS: Do not include descriptions of shots, music, or tone in brackets.

SCRIPT:`,
			BuildSystemPrompt(req.Language, req.Tone),
			req.Duration, durationMinutes, sanitizedTitle, req.Tone,
			sanitizedSource, durationMinutes, targetWords,
		)
	}

	if req.Prompt != "" && req.Prompt != req.SourceText {
		mainPrompt = prependOverriding(req.Prompt) + mainPrompt
	}

	return mainPrompt
}

// prependOverriding builds the overriding instructions block from config or fallback.
func prependOverriding(rawPrompt string) string {
	promptBlock := types.SanitizeInput(rawPrompt)
	if cfg := Get(); cfg != nil {
		rendered, err := cfg.RenderOverridingInstructions(promptBlock)
		if err == nil {
			return rendered
		}
	}
	return "## OVERRIDING WRITING INSTRUCTIONS — THESE TAKE ABSOLUTE PRIORITY ##\n" +
		promptBlock +
		"\n\n## END OF OVERRIDING INSTRUCTIONS ##\n\n" +
		"IMPORTANT: The task below is a FORMATTING TEMPLATE only. " +
		"Follow the OVERRIDING INSTRUCTIONS above for CONTENT, STYLE, STRUCTURE, and LENGTH. " +
		"Do NOT write a video script. Write according to the instructions above.\n\n"
}

// unused import guard
var _ = strings.TrimSpace
