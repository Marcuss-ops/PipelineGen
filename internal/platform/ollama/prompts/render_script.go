package prompts

import "fmt"

// RenderScriptGeneration renders the main script generation user prompt.
func (c *Config) RenderScriptGeneration(duration int, durationMinutes int, title, tone, sourceText string, targetWords int) (string, error) {
	if c == nil {
		return "", fmt.Errorf("prompts config not initialized")
	}
	return render(c.ScriptGeneration.User, map[string]any{
		"Duration":        duration,
		"DurationMinutes": durationMinutes,
		"Title":           title,
		"Tone":            tone,
		"SourceText":      sourceText,
		"TargetWords":     targetWords,
	})
}

// RenderStructuredScriptGeneration renders the structured (per-clip JSON) prompt.
func (c *Config) RenderStructuredScriptGeneration(duration int, durationMinutes int, title, tone, sourceText string, maxChars int) (string, error) {
	if c == nil {
		return "", fmt.Errorf("prompts config not initialized")
	}
	return render(c.ScriptGeneration.Structured, map[string]any{
		"Duration":        duration,
		"DurationMinutes": durationMinutes,
		"Title":           title,
		"Tone":            tone,
		"SourceText":      sourceText,
		"MaxChars":        maxChars,
	})
}

// RenderOverridingInstructions renders the overriding instructions block.
func (c *Config) RenderOverridingInstructions(promptBlock string) (string, error) {
	if c == nil {
		return "", fmt.Errorf("prompts config not initialized")
	}
	return render(c.ScriptGeneration.OverridingInstructions, map[string]any{
		"PromptBlock": promptBlock,
	})
}

// RenderClassification renders the classification prompt.
func (c *Config) RenderClassification(title, categories string) (string, error) {
	if c == nil {
		return "", fmt.Errorf("prompts config not initialized")
	}
	return render(c.Classification, map[string]any{
		"Title":      title,
		"Categories": categories,
	})
}
