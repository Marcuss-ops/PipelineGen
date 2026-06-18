package gemmamemory

import (
	"regexp"
	"strings"
)

// ctaPattern matches common call-to-action phrasings across supported languages.
var ctaPattern = regexp.MustCompile(
	`(?i)\b(` +
		`subscribe|like\s+(and|&)\s+comment|comment\s+below|` +
		`follow\s+us|follow\s+me|share\s+this|check\s+out|` +
		`let\s+me\s+know|join\s+us|hit\s+(the\s+)?(like|subscribe|bell)|` +
		`smash\s+(that\s+)?(like|subscribe)|` +
		`iscriviti|metti\s+(mi\s+)?like|commenta\s+(qui\s+)?(sotto|qua)|` +
		`seguici|seguimi|condividi|fammi\s+sapere|unisciti|` +
		`suscr[ií]bete|dale\s+like|comenta\s+(abajo|aqu[ií])|` +
		`s[ií]gueme|s[ií]guenos|comparte|` +
		`abonnez[-\s]?vous|mettez\s+un\s+like|aimez\s+(et\s+)?commentez|` +
		`suivez[-\s]?nous|suivez[-\s]?moi|partagez|` +
		`abonnieren|gib\s+(mir\s+)?einen?\s+like|kommentier(e|en)?|` +
		`folge\s+(uns|mir)|teilen|` +
		`inscreva[-\s]?se|deixe\s+seu\s+like|comente\s+(aqui|abaixo)|` +
		`siga[-\s]?me|compartilhe` +
		`)\b`,
)

// ExtractMemories extracts reusable memory entries from a completed generation.
func ExtractMemories(input SaveGenerationInput, genID, topicKey, outputText string) []SaveMemoryInput {
	var memories []SaveMemoryInput

	words := strings.Fields(outputText)
	structureSummary := outputText
	if len(words) > 50 {
		structureSummary = strings.Join(words[:50], " ") + "..."
	}
	memories = append(memories, SaveMemoryInput{
		ChannelID:          input.ChannelID,
		MemoryType:         MemoryTypeScriptStructure,
		TopicKey:           topicKey,
		Title:              input.Title,
		Summary:            structureSummary,
		ContentText:        "",
		SourceGenerationID: genID,
		SourceJobID:        input.JobID,
	})

	paragraphs := strings.Split(outputText, "\n\n")
	if len(paragraphs) > 0 {
		intro := strings.TrimSpace(paragraphs[0])
		if len(intro) > 20 && len(intro) < 500 {
			memories = append(memories, SaveMemoryInput{
				ChannelID:          input.ChannelID,
				MemoryType:         MemoryTypeSuccessfulHook,
				TopicKey:           topicKey,
				Title:              input.Title,
				Summary:            intro,
				SourceGenerationID: genID,
				SourceJobID:        input.JobID,
			})
		}
	}

	if len(paragraphs) > 1 {
		cta := strings.TrimSpace(paragraphs[len(paragraphs)-1])
		if len(cta) > 20 && len(cta) < 500 && ctaPattern.MatchString(cta) {
			memories = append(memories, SaveMemoryInput{
				ChannelID:          input.ChannelID,
				MemoryType:         MemoryTypeReusableCTA,
				TopicKey:           topicKey,
				Title:              input.Title,
				Summary:            cta,
				SourceGenerationID: genID,
				SourceJobID:        input.JobID,
			})
		}
	}

	return memories
}
