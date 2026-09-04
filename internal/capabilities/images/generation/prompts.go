package generation

import (
	"fmt"
	"strings"
)

type Section struct {
	Title string `json:"title" binding:"required" example:"Castello Medievale"`
	Text string `json:"text" example:"Descrizione della scena..."`
	Style string `json:"style" example:"medievale"`
}

const (
	SectionImageWidth = 1344
	SectionImageHeight = 768
)

func BuildPrimaryPrompt(sec Section, topic string) string {
	for _, p := range BuildSectionPrompts(sec, topic) {
		if strings.TrimSpace(p) != "" { return p }
	}
	return ""
}

// BuildSectionPrompts is exported only so the root compatibility facade can
// preserve existing package-local characterization tests during the cutover.
func BuildSectionPrompts(sec Section, topic string) []string {
	var prompts []string
	if sec.Title != "" {
		prompts = append(prompts, fmt.Sprintf("cinematic documentary image of %s", sec.Title), fmt.Sprintf("professional stock photo of %s", sec.Title))
	}
	if topic != "" && !strings.EqualFold(topic, sec.Title) {
		prompts = append(prompts, fmt.Sprintf("cinematic documentary image of %s, %s theme", sec.Title, topic), fmt.Sprintf("high quality photography of %s related to %s", sec.Title, topic))
	}
	if text := strings.TrimSpace(sec.Text); text != "" {
		if len(text) > 100 { text = text[:100] }
		prompts = append(prompts, text)
	}
	if topic != "" { prompts = append(prompts, fmt.Sprintf("documentary image about %s", topic)) }
	return prompts
}
