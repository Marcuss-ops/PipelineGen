package prompts

import (
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/ollama/types"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// BuildChatMessages builds the message list for the chat API.
// If req.WebContext is non-empty, it is prepended as RAG context.
func BuildChatMessages(req *types.TextGenerationRequest) []types.Message {
	maxChars := req.MaxChars
	if maxChars <= 0 {
		maxChars = 0 // unlimited
	}
	// MaxChars is also used by the canonical plain-text pipeline as a
	// per-scene character budget. It must not switch the prompt to the
	// legacy JSON contract: that contract conflicts with the plain-text
	// instruction appended below and can make Gemma return no content.
	isStructured := maxChars > 0 && req.OutputMode == types.OutputModeScriptV1

	durationMinutes := req.DurationMinutes
	if durationMinutes == 0 && req.Duration > 0 {
		durationMinutes = req.Duration / 60
	}
	if durationMinutes == 0 {
		durationMinutes = 1
	}

	targetWords := durationMinutes * resolvedWordsPerMinute(req.WordsPerMinute)
	if req.MinWords > 0 {
		targetWords = req.MinWords
	}

	sanitizedSource := types.SanitizeInput(req.SourceText)
	sanitizedTitle := types.SanitizeInput(req.Title)

	var userContent string
	if cfg := Get(); cfg != nil {
		if isStructured {
			rendered, err := cfg.RenderStructuredScriptGeneration(
				req.Duration, durationMinutes, sanitizedTitle, req.Tone,
				sanitizedSource, maxChars,
			)
			if err == nil {
				userContent = rendered
			}
		} else {
			rendered, err := cfg.RenderScriptGeneration(
				req.Duration, durationMinutes, sanitizedTitle, req.Tone,
				sanitizedSource, targetWords,
			)
			if err == nil {
				userContent = rendered
			}
		}
	}
	if userContent == "" && !isStructured {
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
	if userContent == "" && isStructured {
		// Build per-clip JSON prompt with actual clip IDs
		clipIDsList := "clip_ids: " + strings.Join(req.ClipIDs, ", ")
		if req.ClipIDs == nil {
			clipIDsList = ""
		}
		userContent = fmt.Sprintf(`For each clip below, write a VERY CONCISE %d-character description.

Reference input:
"%s"

Title: %s
Style: %s
%s

You MUST respond ONLY with a raw JSON array — no markdown, no code fences, no explanation.
Use this exact structure:
[
  {"clip_id": "CLIP_ID_HERE", "text": "max %d chars of concise description"}
]

Rules:
- Each "clip_id" must be one of the clip_ids listed above.
- Each "text" value must be at most %d characters.
- Be factual and concise. No intros, no conclusions, no meta-commentary.
- Return ONLY the JSON array, no other text.`,
			maxChars, sanitizedSource, sanitizedTitle, req.Tone, clipIDsList, maxChars, maxChars)
	}

	if req.Prompt != "" && req.Prompt != req.SourceText {
		userContent = prependOverriding(req.Prompt) + userContent
	}

	if req.WebContext != "" {
		userContent = req.WebContext + "\n" + userContent
	}

	userContent = applyGroundingPolicy(req.GroundingPolicy, userContent)

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

	targetWords := durationMinutes * resolvedWordsPerMinute(req.WordsPerMinute)
	if req.MinWords > 0 {
		targetWords = req.MinWords
	}

	sanitizedSource := types.SanitizeInput(req.SourceText)
	sanitizedTitle := types.SanitizeInput(req.Title)

	var taskBody string
	if cfg := Get(); cfg != nil {
		rendered, err := cfg.RenderScriptGeneration(
			req.Duration, durationMinutes, sanitizedTitle, req.Tone,
			sanitizedSource, targetWords,
		)
		if err == nil {
			taskBody = rendered
		}
	}

	if taskBody == "" {
		taskBody = fmt.Sprintf(`TASK: Write a true NARRATIVE DOCUMENTARY of %d seconds (about %d minutes).

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
			req.Duration, durationMinutes, sanitizedTitle, req.Tone,
			sanitizedSource, durationMinutes, targetWords,
		)
	}

	// Apply grounding policy to the task body so it sits after the
	// system prompt once that is prepended.
	taskBody = applyGroundingPolicy(req.GroundingPolicy, taskBody)

	if req.Prompt != "" && req.Prompt != req.SourceText {
		taskBody = prependOverriding(req.Prompt) + taskBody
	}

	return BuildSystemPrompt(req.Language, req.Tone) + "\n\n" + taskBody
}

// applyGroundingPolicy prepends policy-specific grounding instructions
// to the user prompt when a policy is set. It controls how the model
// weights source_text against clip evidence.
func applyGroundingPolicy(policy string, content string) string {
	var instruction string
	switch policy {
	case scriptpkg.GroundingPolicyClipsPrimary:
		instruction = "GROUNDING POLICY — CLIPS PRIMARY: The supplied clip evidence is the MAIN source of information. The reference input (source_text) is only supporting context. Anchor the narrative primarily on what is visible or described in the clips. Do not invent scenes, dialogue, or events not supported by the clip evidence."
	case scriptpkg.GroundingPolicySourcePrimary:
		instruction = "GROUNDING POLICY — SOURCE PRIMARY: The reference input (source_text) is the MAIN source of information. The supplied clips are only visual support. Anchor the narrative primarily on the written source text. Clips may illustrate the text but must not contradict it."
	case scriptpkg.GroundingPolicyBalanced:
		instruction = "GROUNDING POLICY — BALANCED: Give equal weight to the reference input (source_text) and the supplied clip evidence. The final narrative must represent both sources fairly and must not ignore either. Reconcile any contradiction in favor of the source_text."
	}
	if instruction == "" {
		return content
	}
	return instruction + "\n\n" + content
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
