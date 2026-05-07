package prompts

// BuildSystemPrompt returns the system prompt based on language and tone
// NOTE: Simplified to English-only prompts since tags/stopwords are English-only
func BuildSystemPrompt(language, tone string) string {
	// Base system prompt (English only - LLM responds better, tags are in English)
	basePrompt := "You are an exceptional storyteller and senior copywriter. Your task is to write COMPELLING, DETAIL-RICH, and NARRATIVELY POWERFUL video scripts."

	// Tone instructions
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

	// Add language instruction
	if language == "it" {
		prompt += " Write the ENTIRE script in ITALIAN. Write EXCLUSIVELY in Italian. Do NOT use English unless citing proper nouns or brand names."
	} else {
		prompt += " Write the ENTIRE script in ENGLISH. Write EXCLUSIVELY in English."
	}

	// Add tone instruction
	if toneInstr, ok := toneInstructions[tone]; ok {
		prompt += " " + toneInstr
	}

	return prompt
}
