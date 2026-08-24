package prompts

// BuildSystemPrompt returns the system prompt based on language and tone.
// It reads from the loaded YAML config. Falls back to hardcoded defaults
// if config is not initialized (e.g. in tests).
func BuildSystemPrompt(language, tone string) string {
	if cfg := Get(); cfg != nil {
		return cfg.RenderSystemPrompt(language, tone)
	}
	// Fallback (tests or config not loaded)
	return buildSystemPromptFallback(language, tone)
}

func buildSystemPromptFallback(language, tone string) string {
	basePrompt := "You are an exceptional storyteller and senior copywriter. Your task is to write COMPELLING, DETAIL-RICH, and NARRATIVELY POWERFUL video scripts."

	toneInstructions := map[string]string{
		"professional": "Use a documentary, authoritative, and serious style. Deeply analyze every aspect.",
		"casual":       "Use a colloquial, modern, and friendly style. Be engaging like a YouTube creator.",
		"enthusiastic": "Use an energetic, epic, and motivational style. Every sentence must convey passion.",
		"calm":         "Use a calm, reflective, and poetic style. Create an immersive atmosphere.",
		"funny":        "Use an ironic, brilliant, and funny style. Insert jokes or witty observations.",
		"educational":  "Use a clear, pedagogical, and structured style. Explain concepts simply but thoroughly.",
		"documentary":  "Use a documentary, authoritative, and serious style. Deeply analyze every aspect.",
	}

	prompt := basePrompt

	if language == "it" {
		prompt += " Write the ENTIRE script in ITALIAN. Write EXCLUSIVELY in Italian. Do NOT use English unless citing proper nouns or brand names."
	} else if language == "es" {
		prompt += " Write the ENTIRE script in SPANISH. Write EXCLUSIVELY in Spanish. Do NOT use English unless citing proper nouns or brand names."
	} else if language == "fr" {
		prompt += " Write the ENTIRE script in FRENCH. Write EXCLUSIVELY in French. Do NOT use English unless citing proper nouns or brand names."
	} else {
		prompt += " Write the ENTIRE script in ENGLISH. Write EXCLUSIVELY in English."
	}

	if toneInstr, ok := toneInstructions[tone]; ok {
		prompt += " " + toneInstr
	}

	return prompt
}
