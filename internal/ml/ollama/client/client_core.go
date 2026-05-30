package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
	"velox/go-master/internal/ml/ollama/types"

	"velox/go-master/internal/logger"

	"go.uber.org/zap"
)

// NewClient creates a new Ollama client
func NewClient(baseURL, model string, timeoutSeconds int) *Client {
	if timeoutSeconds <= 0 {
		timeoutSeconds = types.DefaultTimeoutSeconds
	}

	return &Client{
		baseURL:        baseURL,
		model:          model,
		httpClient:     &http.Client{Timeout: time.Duration(timeoutSeconds) * time.Second},
		circuitBreaker: NewCircuitBreaker(types.CircuitBreakerFailures, types.CircuitBreakerTimeout*time.Second),
	}
}

// Chat executes chat with retry, fallback, and circuit breaker
func (c *Client) Chat(ctx context.Context, messages []types.Message, options map[string]interface{}) (string, error) {
	return c.chatWithRetryAndFallback(ctx, messages, options, types.MaxRetries)
}

// chatWithRetryAndFallback implements retry logic with model fallback
func (c *Client) chatWithRetryAndFallback(ctx context.Context, messages []types.Message, options map[string]interface{}, maxRetries int) (string, error) {
	// Build fallback chain including current model
	modelChain := []string{c.model}
	if fallbacks, ok := modelFallbackChains[c.model]; ok {
		modelChain = append(modelChain, fallbacks...)
	}

	var lastErr error

	for _, model := range modelChain {
		if !c.circuitBreaker.AllowRequest() {
			logger.Warn("Circuit breaker open, skipping model", zap.String("model", model))
			continue
		}

		for attempt := 0; attempt < maxRetries; attempt++ {
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			default:
			}

			resp, err := c.doChatRequest(ctx, model, messages, options)
			if err == nil {
				c.circuitBreaker.RecordSuccess()
				return resp, nil
			}

			lastErr = err
			logger.Warn("Chat request failed",
				zap.String("model", model),
				zap.Int("attempt", attempt+1),
				zap.Error(err),
			)

			// Wait before retry with exponential backoff
			if attempt < maxRetries-1 {
				backoff := time.Duration(attempt+1) * 2 * time.Second
				select {
				case <-time.After(backoff):
				case <-ctx.Done():
					return "", ctx.Err()
				}
			}
		}

		c.circuitBreaker.RecordFailure()
		logger.Warn("All retries failed for model, trying fallback", zap.String("model", model))
	}

	if lastErr != nil {
		return "", fmt.Errorf("all models failed, last error: %w", lastErr)
	}
	return "", fmt.Errorf("all models failed without specific error")
}

// doChatRequest executes a single chat request
func (c *Client) doChatRequest(ctx context.Context, model string, messages []types.Message, options map[string]interface{}) (string, error) {
	if c.useNvidiaForLLM && c.nvidiaAPIKey != "" {
		type NvidiaMessage struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		}
		type NvidiaChatPayload struct {
			Model       string          `json:"model"`
			Messages    []NvidiaMessage `json:"messages"`
			Temperature float64         `json:"temperature,omitempty"`
			MaxTokens   int             `json:"max_tokens,omitempty"`
			Stream      bool            `json:"stream"`
		}

		nvMsgs := make([]NvidiaMessage, len(messages))
		for i, m := range messages {
			nvMsgs[i] = NvidiaMessage{
				Role:    m.Role,
				Content: m.Content,
			}
		}

		nvModel := c.nvidiaLLMModel
		if nvModel == "" {
			nvModel = "meta/llama-3.1-8b-instruct"
		}

		payload := NvidiaChatPayload{
			Model:    nvModel,
			Messages: nvMsgs,
			Stream:   false,
		}

		body, err := json.Marshal(payload)
		if err != nil {
			return "", err
		}

		httpReq, err := http.NewRequestWithContext(ctx, "POST", "https://integrate.api.nvidia.com/v1/chat/completions", bytes.NewReader(body))
		if err != nil {
			return "", err
		}
		httpReq.Header.Set("Content-Type", "application/json")
		httpReq.Header.Set("Authorization", "Bearer "+c.nvidiaAPIKey)

		resp, err := c.httpClient.Do(httpReq)
		if err != nil {
			return "", err
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			var errBody []byte
			if b, errRead := io.ReadAll(resp.Body); errRead == nil {
				errBody = b
			}
			return "", fmt.Errorf("nvidia nim chat returned status %d: %s", resp.StatusCode, string(errBody))
		}

		type NvidiaChoice struct {
			Message struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"message"`
		}
		type NvidiaResponse struct {
			Choices []NvidiaChoice `json:"choices"`
		}

		var result NvidiaResponse
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			return "", err
		}

		if len(result.Choices) == 0 {
			return "", fmt.Errorf("nvidia nim returned empty choices")
		}

		logger.Info("NVIDIA NIM chat response received",
			zap.String("model", nvModel),
			zap.Int("chars", len(result.Choices[0].Message.Content)),
		)

		return result.Choices[0].Message.Content, nil
	}

	req := types.ChatRequest{
		Model:    model,
		Messages: messages,
		Stream:   false,
		Options:  options,
	}

	body, err := json.Marshal(req)
	if err != nil {
		return "", err
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/api/chat", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("ollama chat returned status %d", resp.StatusCode)
	}

	var result types.ChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}

	logger.Info("Ollama chat response received",
		zap.String("model", model),
		zap.Int("chars", len(result.Message.Content)),
		zap.Int("words", len(strings.Fields(result.Message.Content))),
	)

	return result.Message.Content, nil
}
