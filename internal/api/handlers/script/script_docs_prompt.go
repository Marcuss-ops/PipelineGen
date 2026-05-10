package script

import (
	"fmt"
	"strings"
)

func buildPrompt(topic string, duration int, language, template string) string {
	wordCount := duration * 3
	style := "documentary"
	switch strings.ToLower(strings.TrimSpace(template)) {
	case "storytelling":
		style = "storytelling"
	case "top10":
		style = "top 10"
	case "biography":
		style = "biography"
	}

	return fmt.Sprintf(
		"Generate a %s text about %s in %s. Target length %d words, with a minimum of %d and a maximum of %d words. Write at least 3 complete paragraphs. Return only the final text, without introductions, titles, technical notes, meta-comments, or phrases like 'okay, here's'. If the content risks being too short, expand it with details, transitions, and coherent context until it reaches the target.",
		style, topic, language, wordCount, wordCount-25, wordCount+25,
	)
}
