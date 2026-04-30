package script

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"velox/go-master/internal/ml/ollama"
	"velox/go-master/internal/ml/ollama/client"
	"velox/go-master/internal/ml/ollama/types"
)

type artlistTagSuggestion struct {
	Tags []string `json:"tags"`
}

func suggestArtlistSearchTags(ctx context.Context, gen *ollama.Generator, req ScriptDocsRequest, phrase, narrative string) []string {
	client := (*client.Client)(nil)
	if gen != nil {
		client = gen.GetClient()
	}
	if client == nil {
		return nil
	}
	model := client.Model()
	if strings.TrimSpace(model) == "" {
		model = types.DefaultModel
	}

	prompt := fmt.Sprintf(`You are an assistant for Artlist clip search.
Suggest ONLY search tags in English, short and concrete.
Do not invent clips, do not describe the phrase, do not return explanations.

TOPIC: %s
PHRASE: %s
FULL TEXT:
%s

Return only pure JSON in the format {"tags":["tag1","tag2","tag3"]}.`, req.Topic, phrase, narrative)

	raw, err := client.GenerateWithOptions(ctx, model, prompt, map[string]interface{}{
		"temperature": types.SuggestionTemperature,
		"num_predict": types.SuggestionNumPredict,
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
	if len(tags) > types.MaxArtlistTags {
		tags = tags[:types.MaxArtlistTags]
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
