package clip

import "strings"

// extractEntities extracts named entities from text
func (s *SemanticSuggester) extractEntities(text string) []Entity {
	var entities []Entity

	words := strings.Fields(text)
	for i, word := range words {
		cleaned := strings.Trim(word, ".,!?;:\\\"'()[]{}")

		if len(cleaned) > 1 && cleaned[0] >= 'A' && cleaned[0] <= 'Z' {
			if i+1 < len(words) {
				nextWord := strings.Trim(words[i+1], ".,!?;:\\\"'()[]{}")
				if len(nextWord) > 1 && nextWord[0] >= 'A' && nextWord[0] <= 'Z' {
					fullName := cleaned + " " + nextWord
					entities = append(entities, Entity{
						Value: fullName,
						Type:  "PERSON",
					})
					i++
					continue
				}
			}

			if len(cleaned) > 2 {
				entities = append(entities, Entity{
					Value: cleaned,
					Type:  "PERSON_OR_PLACE",
				})
			}
		}
	}

	return entities
}

// extractActionVerbs extracts action verbs from sentence
func (s *SemanticSuggester) extractActionVerbs(sentence string) []string {
	var verbs []string

	actionVerbs := []string{
		"saluta", "greet", "greets", "greeting",
		"parla", "talk", "talks", "speaking", "speak",
		"cammina", "walk", "walks", "walking",
		"corre", "run", "runs", "running",
		"guida", "drive", "drives", "driving",
		"spiega", "explain", "explains", "explaining",
		"mostra", "show", "shows", "showing",
		"presenta", "present", "presents", "presenting",
		"dimostra", "demonstrate", "demonstrates",
		"intervista", "interview", "interviews",
		"risponde", "answer", "answers", "answering",
		"chiede", "ask", "asks", "asking",
		"ride", "laugh", "laughs", "laughing",
		"sorride", "smile", "smiles", "smiling",
		"stringe", "shake", "shakes", "handshake",
		"abbraccia", "hug", "hugs", "hugging",
		"balla", "dance", "dances", "dancing",
		"canta", "sing", "sings", "singing",
	}

	sentenceLower := strings.ToLower(sentence)

	for _, verb := range actionVerbs {
		if strings.Contains(sentenceLower, verb) {
			verbs = append(verbs, verb)
		}
	}

	return verbs
}

// detectGroupFromSentence detects the group from sentence context
func (s *SemanticSuggester) detectGroupFromSentence(sentence string) string {
	sentenceLower := strings.ToLower(sentence)

	switch {
	case strings.Contains(sentenceLower, "intervista") || strings.Contains(sentenceLower, "interview"):
		return "interviews"
	case strings.Contains(sentenceLower, "natura") || strings.Contains(sentenceLower, "nature"):
		return "nature"
	case strings.Contains(sentenceLower, "tecnologia") || strings.Contains(sentenceLower, "tech"):
		return "tech"
	case strings.Contains(sentenceLower, "business") || strings.Contains(sentenceLower, "azienda"):
		return "business"
	case strings.Contains(sentenceLower, "città") || strings.Contains(sentenceLower, "city"):
		return "urban"
	default:
		return ""
	}
}

// splitIntoSentences splits text into sentences
func (s *SemanticSuggester) splitIntoSentences(text string) []string {
	var sentences []string
	var current strings.Builder

	for _, char := range text {
		current.WriteRune(char)

		if char == '.' || char == '!' || char == '?' {
			sentence := strings.TrimSpace(current.String())
			if len(sentence) > 0 {
				sentences = append(sentences, sentence)
			}
			current.Reset()
		}
	}

	remaining := strings.TrimSpace(current.String())
	if len(remaining) > 0 {
		sentences = append(sentences, remaining)
	}

	return sentences
}
