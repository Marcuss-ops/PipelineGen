package ollama

import (
	"context"
	"net/http"
	"strings"
	"encoding/json"

	"velox/go-master/internal/core/scriptdoc"
)

type ScriptGenerator struct {
	baseURL string
	model   string
	client  *http.Client
}

func NewScriptGenerator(baseURL, model string) *ScriptGenerator {
	return &ScriptGenerator{
		baseURL: baseURL,
		model:   model,
		client:  &http.Client{},
	}
}

func (g *ScriptGenerator) Generate(ctx context.Context, input scriptdoc.GenerationInput) (*scriptdoc.GeneratedScript, error) {
	prompt := buildScriptPrompt(input)

	reqBody := map[string]interface{}{
		"model":  g.model,
		"prompt": prompt,
		"stream": false,
	}

	body, _ := json.Marshal(reqBody)
	req, _ := http.NewRequestWithContext(ctx, "POST", g.baseURL+"/api/generate", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")

	resp, err := g.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var result struct {
		Response string `json:"response"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	return parseScriptResponse(result.Response, input.Topic), nil
}

func buildScriptPrompt(input scriptdoc.GenerationInput) string {
	return "Generate a script about: " + input.Topic
}

func parseScriptResponse(response, topic string) *scriptdoc.GeneratedScript {
	return &scriptdoc.GeneratedScript{
		Title:   topic,
		Content: response,
	}
}
