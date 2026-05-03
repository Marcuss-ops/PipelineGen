// Package ollama defines adapter interfaces for Ollama LLM operations.
//
// STATUS: EXPERIMENTAL - Interface defined but not yet implemented or used.
// TODO: Implement and migrate LLM operations to use this adapter.
package ollama

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

type GenerateInput struct {
	Model  string
	Prompt string
}

type GenerateResult struct {
	Text string
}

type OllamaAdapter interface {
	Generate(ctx context.Context, input GenerateInput) (*GenerateResult, error)
}

type OllamaClient struct {
	baseURL string
}

func NewOllamaClient(baseURL string) *OllamaClient {
	if baseURL == "" {
		baseURL = "http://localhost:11434"
	}
	return &OllamaClient{baseURL: baseURL}
}

func (o *OllamaClient) Generate(ctx context.Context, input GenerateInput) (*GenerateResult, error) {
	reqBody := map[string]interface{}{
		"model":  input.Model,
		"prompt": input.Prompt,
		"stream": false,
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", o.baseURL+"/api/generate", jsonReader(jsonData))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to call ollama: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	var result struct {
		Response string `json:"response"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &GenerateResult{Text: result.Response}, nil
}
