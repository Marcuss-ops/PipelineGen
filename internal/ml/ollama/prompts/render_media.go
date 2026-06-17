package prompts

import "fmt"

// RenderSystemPrompt builds the system prompt from base + tone + language.
func (c *Config) RenderSystemPrompt(language, tone string) string {
	if c == nil {
		return ""
	}
	prompt := c.System.Base
	if langSuffix, ok := c.System.Languages[language]; ok {
		prompt += " " + langSuffix
	} else if langSuffix, ok := c.System.Languages["default"]; ok {
		prompt += " " + langSuffix
	}
	if toneInstr, ok := c.System.Tones[tone]; ok {
		prompt += " " + toneInstr
	}
	return prompt
}

// RenderDescription renders the description generation prompt.
func (c *Config) RenderDescription(mediaType, prompt, style string) (system, user string, err error) {
	if c == nil {
		return "", "", fmt.Errorf("prompts config not initialized")
	}
	user, err = render(c.Description.User, map[string]any{
		"MediaType": mediaType,
		"Prompt":    prompt,
		"Style":     style,
	})
	return c.Description.System, user, err
}

// RenderVisualPrompt renders the visual prompt generation prompt.
func (c *Config) RenderVisualPrompt(text, topic, style string) (system, user string, err error) {
	if c == nil {
		return "", "", fmt.Errorf("prompts config not initialized")
	}
	user, err = render(c.VisualPrompt.User, map[string]any{
		"Text":  text,
		"Topic": topic,
		"Style": style,
	})
	return c.VisualPrompt.System, user, err
}

// RenderTranslation renders the translation prompt.
func (c *Config) RenderTranslation(text, targetLanguage string) (system, user string, err error) {
	if c == nil {
		return "", "", fmt.Errorf("prompts config not initialized")
	}
	user, err = render(c.Translation.User, map[string]any{
		"TargetLanguage": targetLanguage,
		"Text":           text,
	})
	return c.Translation.System, user, err
}

// RenderVideoMetadata renders the video metadata prompt.
func (c *Config) RenderVideoMetadata(title string) (system, user string, err error) {
	if c == nil {
		return "", "", fmt.Errorf("prompts config not initialized")
	}
	user, err = render(c.VideoMetadata.User, map[string]any{
		"Title": title,
	})
	return c.VideoMetadata.System, user, err
}
