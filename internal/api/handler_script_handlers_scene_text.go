package api

import "strings"

// splitIntoSentences splits text into sentences based on punctuation.
func splitIntoSentences(text string) []string {
	var sentences []string
	var current strings.Builder

	text = strings.ReplaceAll(text, "\n", " ")
	text = strings.ReplaceAll(text, "\r", " ")

	runes := []rune(text)
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		current.WriteRune(r)
		if r == '.' || r == '!' || r == '?' {
			if i+1 == len(runes) || runes[i+1] == ' ' {
				s := strings.TrimSpace(current.String())
				if s != "" {
					sentences = append(sentences, s)
				}
				current.Reset()
			}
		}
	}
	s := strings.TrimSpace(current.String())
	if s != "" {
		sentences = append(sentences, s)
	}
	return sentences
}

// splitScriptIntoSceneImages splits a script into scenes of N sentences each (for image generation).
func splitScriptIntoSceneImages(script string, sentencesPerImage int) []string {
	if sentencesPerImage <= 0 {
		sentencesPerImage = 10
	}
	sentences := splitIntoSentences(script)
	if len(sentences) == 0 {
		return nil
	}
	var scenes []string
	var current []string
	for _, s := range sentences {
		current = append(current, s)
		if len(current) == sentencesPerImage {
			scenes = append(scenes, strings.Join(current, " "))
			current = nil
		}
	}
	if len(current) > 0 {
		scenes = append(scenes, strings.Join(current, " "))
	}
	return scenes
}

// truncatePrompt extracts a short visual description from scene text.
// Takes the first 1-2 sentences and caps at maxLen characters.
func truncatePrompt(text string, maxLen int) string {
	sentences := splitIntoSentences(text)
	if len(sentences) == 0 {
		if len(text) > maxLen {
			return text[:maxLen]
		}
		return text
	}
	prompt := sentences[0]
	if len(sentences) > 1 && len(prompt)+len(sentences[1])+1 <= maxLen {
		prompt += " " + sentences[1]
	}
	if len(prompt) > maxLen {
		prompt = prompt[:maxLen]
	}
	return prompt
}
