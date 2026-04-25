package client

import (
	"velox/go-master/internal/ml/ollama/types"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"go.uber.org/zap"
	"velox/go-master/pkg/logger"
)

// Generate genera testo con Ollama (Legacy API)
func (c *Client) Generate(ctx context.Context, prompt string) (string, error) {
	return c.GenerateWithOptions(ctx, c.model, prompt, nil)
}

// GenerateWithOptions genera testo con opzioni esplicite (Legacy API)
func (c *Client) GenerateWithOptions(ctx context.Context, model, prompt string, options map[string]interface{}) (string, error) {
	if model == "" {
		model = c.model
	}

	req := types.GenerateRequest{
		Model:   model,
		Prompt:  prompt,
		Stream:  false,
		Options: options,
	}

	body, err := json.Marshal(req)
	if err != nil {
		return "", fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/api/generate", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("ollama request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("ollama returned status %d", resp.StatusCode)
	}

	var result types.GenerateResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("failed to decode response: %w", err)
	}

	logger.Info("Ollama generate response received",
		zap.Int("chars", len(result.Response)),
	)

	return result.Response, nil
}

// GenerateStream genera testo con Ollama in modalità streaming
func (c *Client) GenerateStream(ctx context.Context, prompt string) (<-chan string, <-chan error) {
	return c.GenerateStreamWithOptions(ctx, c.model, prompt, nil)
}

// GenerateStreamWithOptions genera testo con opzioni esplicite in modalità streaming.
func (c *Client) GenerateStreamWithOptions(ctx context.Context, model, prompt string, options map[string]interface{}) (<-chan string, <-chan error) {
	textChan := make(chan string, 100)
	errChan := make(chan error, 1)

	if model == "" {
		model = c.model
	}

	req := types.GenerateRequest{
		Model:   model,
		Prompt:  prompt,
		Stream:  true,
		Options: options,
	}

	body, err := json.Marshal(req)
	if err != nil {
		errChan <- fmt.Errorf("failed to marshal request: %w", err)
		close(textChan)
		close(errChan)
		return textChan, errChan
	}

	go func() {
		defer close(textChan)
		defer close(errChan)

		httpReq, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/api/generate", bytes.NewReader(body))
		if err != nil {
			errChan <- err
			return
		}
		httpReq.Header.Set("Content-Type", "application/json")

		resp, err := c.httpClient.Do(httpReq)
		if err != nil {
			errChan <- fmt.Errorf("ollama request failed: %w", err)
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			errChan <- fmt.Errorf("ollama returned status %d", resp.StatusCode)
			return
		}

		decoder := json.NewDecoder(resp.Body)
		for {
			var result types.GenerateResponse
			if err := decoder.Decode(&result); err != nil {
				if err.Error() == "EOF" {
					break
				}
				errChan <- fmt.Errorf("failed to decode streaming response: %w", err)
				return
			}

			if result.Response != "" {
				textChan <- result.Response
			}

			if result.Done {
				break
			}
		}
	}()

	return textChan, errChan
}
