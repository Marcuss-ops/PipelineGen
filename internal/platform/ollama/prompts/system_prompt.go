package prompts

import "strings"

// BuildSystemPrompt returns the system prompt based on language and tone.
func BuildSystemPrompt(language, tone string) string {
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

	language = strings.ToLower(strings.ReplaceAll(strings.TrimSpace(language), "_", "-"))
	languageNames := map[string]string{
		"en": "English", "it": "Italian", "es": "Spanish", "fr": "French",
		"de": "German", "pt": "Portuguese", "pt-br": "Brazilian Portuguese",
		"pl": "Polish", "ru": "Russian", "tr": "Turkish", "id": "Indonesian",
	}
	name, known := languageNames[language]
	if !known {
		name = language
	}
	prompt += " Write the ENTIRE script in " + name + ". Use exclusively that language, except for proper nouns and brand names. Do not default to English."

	if toneInstr, ok := toneInstructions[tone]; ok {
		prompt += " " + toneInstr
	}

	return prompt
}
