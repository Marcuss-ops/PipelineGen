package script

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"velox/go-master/internal/ml/ollama"
)

type artlistTagSuggestion struct {
	Tags []string `json:"tags"`
}

func suggestArtlistSearchTags(ctx context.Context, gen *ollama.Generator, req ScriptDocsRequest, phrase, narrative string) []string {
	client := (*ollama.Client)(nil)
	if gen != nil {
		client = gen.GetClient()
	}
	if client == nil {
		return nil
	}

	prompt := fmt.Sprintf(`Sei un assistente per la ricerca di clip Artlist.
Devi suggerire SOLO tag di ricerca in inglese, brevi e concreti.
Non inventare clip, non descrivere la frase, non restituire spiegazioni.

TOPIC: %s
FRASE: %s
TESTO COMPLETO:
%s

Restituisci solo JSON puro nel formato {"tags":["tag1","tag2","tag3"]}.`, req.Topic, phrase, narrative)

	raw, err := client.GenerateWithOptions(ctx, "gemma3:4b", prompt, map[string]interface{}{
		"temperature": 0.2,
		"num_predict": 128,
	})
	if err != nil {
		return nil
	}

	var suggestion artlistTagSuggestion
	cleaned := stripCodeFence(raw)
	jsonPayload := extractJSONObject(cleaned)
	if jsonPayload == "" {
		return nil
	}
	if err := json.Unmarshal([]byte(jsonPayload), &suggestion); err != nil {
		return nil
	}

	tags := uniqueStrings(normalizeTagList(suggestion.Tags))
	if len(tags) == 0 {
		return nil
	}
	if len(tags) > 5 {
		tags = tags[:5]
	}
	return tags
}

func normalizeTagList(tags []string) []string {
	out := make([]string, 0, len(tags))
	for _, tag := range tags {
		tag = strings.TrimSpace(tag)
		if tag == "" {
			continue
		}
		tag = strings.ToLower(tag)
		out = append(out, tag)
	}
	return out
}
