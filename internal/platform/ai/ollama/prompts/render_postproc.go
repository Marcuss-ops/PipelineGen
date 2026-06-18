package prompts

import "fmt"

// RenderQAPass renders the QA pass prompt.
func (c *Config) RenderQAPass(script, language, title, tone string) (string, error) {
	if c == nil {
		return "", fmt.Errorf("prompts config not initialized")
	}
	toneBlock := ""
	if tone != "" {
		toneBlock = fmt.Sprintf("REQUESTED TONE: %s\n", tone)
	}
	titleBlock := ""
	if title != "" {
		titleBlock = fmt.Sprintf("DOCUMENT TITLE: %s\n", title)
	}
	body, err := render(c.QAPass.Body, map[string]any{
		"ToneBlock":  toneBlock,
		"TitleBlock": titleBlock,
		"Script":     script,
	})
	if err != nil {
		return "", err
	}
	return c.QAPass.System + "\n\n" + body, nil
}

// RenderCoherencePass renders the coherence pass prompt.
func (c *Config) RenderCoherencePass(script, language, title string) (string, error) {
	if c == nil {
		return "", fmt.Errorf("prompts config not initialized")
	}
	titleBlock := ""
	if title != "" {
		titleBlock = fmt.Sprintf("DOCUMENT TITLE: %s\n", title)
	}
	body, err := render(c.CoherencePass.Body, map[string]any{
		"TitleBlock": titleBlock,
		"Script":     script,
	})
	if err != nil {
		return "", err
	}
	return c.CoherencePass.System + "\n\n" + body, nil
}

// RenderExpand renders the expand prompt.
func (c *Config) RenderExpand(topic, chapter string, targetWords, currentWords int, guidelines string) (string, error) {
	if c == nil {
		return "", fmt.Errorf("prompts config not initialized")
	}
	guidelinesBlock := ""
	if guidelines != "" {
		guidelinesBlock = fmt.Sprintf("[GUIDELINES]\n%s\n[/GUIDELINES]\n", guidelines)
	}
	return render(c.Expand.Body, map[string]any{
		"CurrentWords":    currentWords,
		"TargetWords":     targetWords,
		"Deficit":         targetWords - currentWords,
		"GuidelinesBlock": guidelinesBlock,
		"Topic":           topic,
		"Chapter":         chapter,
	})
}

// RenderCompress renders the compress prompt.
func (c *Config) RenderCompress(topic, chapter string, targetWords, currentWords int, guidelines string) (string, error) {
	if c == nil {
		return "", fmt.Errorf("prompts config not initialized")
	}
	guidelinesBlock := ""
	if guidelines != "" {
		guidelinesBlock = fmt.Sprintf("[GUIDELINES]\n%s\n[/GUIDELINES]\n", guidelines)
	}
	return render(c.Compress.Body, map[string]any{
		"CurrentWords":    currentWords,
		"TargetWords":     targetWords,
		"Excess":          currentWords - targetWords,
		"GuidelinesBlock": guidelinesBlock,
		"Topic":           topic,
		"Chapter":         chapter,
	})
}

// RenderQualityCompress renders the quality compress prompt.
func (c *Config) RenderQualityCompress(topic, chapter string, targetWords int, guidelines string) (string, error) {
	if c == nil {
		return "", fmt.Errorf("prompts config not initialized")
	}
	guidelinesBlock := ""
	if guidelines != "" {
		guidelinesBlock = fmt.Sprintf("[GUIDELINES]\n%s\n[/GUIDELINES]\n", guidelines)
	}
	return render(c.QualityCompress, map[string]any{
		"Topic":           topic,
		"Chapter":         chapter,
		"TargetWords":     targetWords,
		"GuidelinesBlock": guidelinesBlock,
	})
}
