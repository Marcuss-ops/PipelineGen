package textgen

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// doChatCompletion sends a request to an OpenAI-compatible Chat Completions API.
// Used by both OpenAI and Groq since they share the same API format.
func (g *Generator) doChatCompletion(ctx context.Context, url, apiKey string, req *GenerationRequest) (string, error) {
	messages := []map[string]string{}
	if req.SystemPrompt != "" {
		messages = append(messages, map[string]string{"role": "system", "content": req.SystemPrompt})
	}
	messages = append(messages, map[string]string{"role": "user", "content": req.Prompt})

	body := map[string]interface{}{
		"model":       req.Model,
		"messages":    messages,
		"temperature": req.Temperature,
		"max_tokens":  req.MaxTokens,
		"stream":      false,
	}
	if req.TopP > 0 {
		body["top_p"] = req.TopP
	}

	return g.doJSONPostWithAuth(ctx, url, "Bearer "+apiKey, body, "choices.0.message.content")
}

// doJSONPost sends a JSON POST request and extracts a field from the response.
// responseKey uses dot notation to navigate nested JSON (e.g., "choices.0.message.content").
func (g *Generator) doJSONPost(ctx context.Context, url string, payload interface{}, responseKey string) (string, error) {
	return g.doJSONPostWithAuth(ctx, url, "", payload, responseKey)
}

// doJSONPostWithAuth sends a JSON POST with optional Authorization header.
func (g *Generator) doJSONPostWithAuth(ctx context.Context, url, authHeader string, payload interface{}, responseKey string) (string, error) {
	jsonBody, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(jsonBody))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}

	resp, err := g.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("API returned status %d: %s", resp.StatusCode, string(respBody))
	}

	// Parse response and extract the requested key
	var result interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", fmt.Errorf("failed to parse response: %w", err)
	}

	return extractJSONKey(result, responseKey)
}

// extractJSONKey navigates a nested JSON structure using dot notation.
// Numeric keys (e.g., "0") are treated as array indices.
func extractJSONKey(data interface{}, key string) (string, error) {
	parts := strings.Split(key, ".")
	current := data

	for _, part := range parts {
		if current == nil {
			return "", fmt.Errorf("key %q not found in response", key)
		}

		// Try array index first
		var i int
		if n, err := fmt.Sscanf(part, "%d", &i); err == nil && n == 1 {
			arr, ok := current.([]interface{})
			if !ok || i >= len(arr) {
				return "", fmt.Errorf("key %q: array index %d out of bounds", key, i)
			}
			current = arr[i]
			continue
		}

		// Map key
		m, ok := current.(map[string]interface{})
		if !ok {
			return "", fmt.Errorf("key %q: expected object, got %T", key, current)
		}
		val, exists := m[part]
		if !exists {
			return "", fmt.Errorf("key %q not found in response", key)
		}
		current = val
	}

	switch v := current.(type) {
	case string:
		return v, nil
	default:
		bytes, err := json.Marshal(v)
		if err != nil {
			return "", fmt.Errorf("failed to serialize result: %w", err)
		}
		return string(bytes), nil
	}
}
