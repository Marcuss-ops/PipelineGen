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

// RenderScriptRegeneration renders the regeneration prompt.
func (c *Config) RenderScriptRegeneration(title, tone, script string) (string, error) {
	if c == nil {
		return "", fmt.Errorf("prompts config not initialized")
	}
	return render(c.ScriptRegeneration.User, map[string]any{
		"Title":  title,
		"Tone":   tone,
		"Script": script,
	})
}

// RenderEntityExtraction renders the entity extraction prompt.
func (c *Config) RenderEntityExtraction(text string, entityCount int) (string, error) {
	if c == nil {
		return "", fmt.Errorf("prompts config not initialized")
	}
	return render(c.EntityExtraction, map[string]any{
		"Text":        text,
		"EntityCount": entityCount,
	})
}

// RenderTimelineRouting renders the timeline asset routing prompt.
func (c *Config) RenderTimelineRouting(topic, opening, closing string, keywords, entities []string, stockFolders, artlistFolders string) (string, error) {
	if c == nil {
		return "", fmt.Errorf("prompts config not initialized")
	}
	return render(c.TimelineRouting, map[string]any{
		"Topic":          topic,
		"Opening":        opening,
		"Closing":        closing,
		"Keywords":       joinStrings(keywords),
		"Entities":       joinStrings(entities),
		"StockFolders":   stockFolders,
		"ArtlistFolders": artlistFolders,
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
