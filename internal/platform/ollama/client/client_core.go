package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/ollama/types"

	logger "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/logging"
	retry "github.com/Marcuss-ops/PipelineGen/pkg/retry"

	"go.uber.org/zap"
)

// NewClient creates a new Ollama client
func NewClient(baseURL, model string, timeoutSeconds int) *Client {
	if timeoutSeconds <= 0 {
		timeoutSeconds = types.DefaultTimeoutSeconds
	}

	return &Client{
		baseURL:    baseURL,
		model:      model,
		httpClient: &http.Client{Timeout: time.Duration(timeoutSeconds) * time.Second},
		breakers:   make(map[string]*CircuitBreaker),
	}
}

// Chat executes chat with retry, fallback, and circuit breaker.
// Supports overriding the model by passing options["model"] as a string.
//
// The optional `format` argument is propagated to the wire as a
// TOP-LEVEL `format` field on the `/api/chat` body (NOT inside
// `options`). When non-nil Ollama forces the model response to be
// syntactically valid JSON. P0.2 (June 2026): the script-engine
// adapter passes a `"json"` RawMessage when caller requested
// OutputModeScriptV1 so the V1 contract is enforced at both the
// prompt-suffix and wire-format layers.
//
// Pass nil to disable native JSON-mode (free-form prose).
func (c *Client) Chat(ctx context.Context, messages []types.Message, options map[string]any, format json.RawMessage) (string, error) {
	model := c.model
	if options != nil {
		if optModel, ok := options["model"].(string); ok && optModel != "" {
			model = optModel
		}
	}
	return c.chatWithRetryAndFallback(ctx, model, messages, options, format, types.MaxRetries)
}

// chatWithRetryAndFallback implements retry logic with model fallback
func (c *Client) chatWithRetryAndFallback(ctx context.Context, model string, messages []types.Message, options map[string]any, format json.RawMessage, maxRetries int) (string, error) {
	// Build fallback chain including current model
	modelChain := []string{model}
	if fallbacks, ok := modelFallbackChains[model]; ok {
		modelChain = append(modelChain, fallbacks...)
	}

	var lastErr error
	attemptedAny := false

	for _, model := range modelChain {
		breaker := c.breakerFor(model)
		if !breaker.AllowRequest() {
			logger.Warn("Circuit breaker open, skipping model", zap.String("model", model))
			continue
		}
		attemptedAny = true

		attempt := 0
		resp, err := retry.DoWithValue(ctx, func() (string, error) {
			idx := attempt
			attempt++
			r, e := c.doChatRequest(ctx, model, messages, options, format)
			if e != nil {
				logger.Warn("Chat request failed",
					zap.String("model", model),
					zap.Int("attempt", idx+1),
					zap.Error(e),
				)
			}
			return r, e
		}, retry.RetryOptions{
			MaxAttempts:    maxRetries,
			InitialBackoff: 2 * time.Second,
			BackoffFactor:  1.0,
		})

		if err == nil {
			breaker.RecordSuccess()
			return resp, nil
		}

		lastErr = err
		breaker.RecordFailure()
		logger.Warn("All retries failed for model, trying fallback", zap.String("model", model))
	}

	if lastErr != nil {
		return "", fmt.Errorf("all models failed, last error: %w", lastErr)
	}
	if !attemptedAny {
		return "", fmt.Errorf("all models skipped by circuit breaker")
	}
	return "", fmt.Errorf("all models failed without specific error")
}

// doChatRequest executes a single chat request
func (c *Client) doChatRequest(ctx context.Context, model string, messages []types.Message, options map[string]any, format json.RawMessage) (string, error) {
	if c.useVLLM {
		return c.vllmChatRequest(ctx, model, messages)
	}

	if c.useNvidiaForLLM && c.nvidiaAPIKey != "" {
		return c.nvidiaChatRequest(ctx, messages)
	}

	// Add keep_alive to model options to avoid reloading the model on every request.
	// When set, Ollama keeps the model in GPU VRAM for the specified duration.
	// Callers can override by passing options["keep_alive"].
	if options == nil {
		options = map[string]any{}
	}
	if _, hasKeepAlive := options["keep_alive"]; !hasKeepAlive {
		options["keep_alive"] = "30m"
	}

	req := types.ChatRequest{
		Model:    model,
		Messages: messages,
		Stream:   false,
		Options:  options,
		// P0.2 (June 2026): thread Format as a TOP-LEVEL body field.
		// Ollama interprets a top-level `format` value as the JSON-mode
		// constraint; an `options`-nested `format` would be silently
		// ignored (or treated as a model param).
		Format: format,
	}
	think := false
	req.Think = &think

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
		if errors.Is(err, context.Canceled) {
			return "", err
		}
		return "", &retry.TransientInfrastructureError{Err: err}
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode == http.StatusServiceUnavailable || resp.StatusCode == http.StatusBadGateway || resp.StatusCode == http.StatusGatewayTimeout {
			return "", &retry.TransientInfrastructureError{Err: fmt.Errorf("ollama chat returned status %d", resp.StatusCode)}
		}
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

// vllmChatRequest sends a chat request to a vLLM server via the OpenAI-compatible API.
// vLLM provides continuous batching: multiple concurrent requests share GPU cycles
// at the token level instead of serial FIFO processing.
func (c *Client) vllmChatRequest(ctx context.Context, model string, messages []types.Message) (string, error) {
	type openAIMessage struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	type openAIChatPayload struct {
		Model       string          `json:"model"`
		Messages    []openAIMessage `json:"messages"`
		Temperature float64         `json:"temperature,omitempty"`
		MaxTokens   int             `json:"max_tokens,omitempty"`
		Stream      bool            `json:"stream"`
	}

	oaMsgs := make([]openAIMessage, len(messages))
	for i, m := range messages {
		oaMsgs[i] = openAIMessage{
			Role:    m.Role,
			Content: m.Content,
		}
	}

	payload := openAIChatPayload{
		Model:    model,
		Messages: oaMsgs,
		Stream:   false,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.vllmURL+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return "", err
		}
		return "", &retry.TransientInfrastructureError{Err: err}
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var errBody []byte
		if b, errRead := io.ReadAll(resp.Body); errRead == nil {
			errBody = b
		}
		if resp.StatusCode == http.StatusServiceUnavailable || resp.StatusCode == http.StatusBadGateway || resp.StatusCode == http.StatusGatewayTimeout {
			return "", &retry.TransientInfrastructureError{Err: fmt.Errorf("vllm chat returned status %d: %s", resp.StatusCode, string(errBody))}
		}
		return "", fmt.Errorf("vllm chat returned status %d: %s", resp.StatusCode, string(errBody))
	}

	type openAIChoice struct {
		Message struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"message"`
	}
	type openAIResponse struct {
		Choices []openAIChoice `json:"choices"`
	}

	var result openAIResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}

	if len(result.Choices) == 0 {
		return "", fmt.Errorf("vllm returned empty choices")
	}

	logger.Info("vLLM chat response received",
		zap.String("model", model),
		zap.Int("chars", len(result.Choices[0].Message.Content)),
	)

	return result.Choices[0].Message.Content, nil
}

// nvidiaChatRequest sends a chat request to NVIDIA NIM cloud API.
func (c *Client) nvidiaChatRequest(ctx context.Context, messages []types.Message) (string, error) {
	type nvidiaMessage struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	type nvidiaChatPayload struct {
		Model       string          `json:"model"`
		Messages    []nvidiaMessage `json:"messages"`
		Temperature float64         `json:"temperature,omitempty"`
		MaxTokens   int             `json:"max_tokens,omitempty"`
		Stream      bool            `json:"stream"`
	}

	nvMsgs := make([]nvidiaMessage, len(messages))
	for i, m := range messages {
		nvMsgs[i] = nvidiaMessage{
			Role:    m.Role,
			Content: m.Content,
		}
	}

	nvModel := c.nvidiaLLMModel
	if nvModel == "" {
		nvModel = "meta/llama-3.1-8b-instruct"
	}

	payload := nvidiaChatPayload{
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
		if errors.Is(err, context.Canceled) {
			return "", err
		}
		return "", &retry.TransientInfrastructureError{Err: err}
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var errBody []byte
		if b, errRead := io.ReadAll(resp.Body); errRead == nil {
			errBody = b
		}
		if resp.StatusCode == http.StatusServiceUnavailable || resp.StatusCode == http.StatusBadGateway || resp.StatusCode == http.StatusGatewayTimeout {
			return "", &retry.TransientInfrastructureError{Err: fmt.Errorf("nvidia nim chat returned status %d: %s", resp.StatusCode, string(errBody))}
		}
		return "", fmt.Errorf("nvidia nim chat returned status %d: %s", resp.StatusCode, string(errBody))
	}

	type nvidiaChoice struct {
		Message struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"message"`
	}
	type nvidiaResponse struct {
		Choices []nvidiaChoice `json:"choices"`
	}

	var result nvidiaResponse
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
