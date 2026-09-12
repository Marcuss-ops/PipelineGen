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

	logger "github.com/Marcuss-ops/PipelineGen/internal/platform/logging"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/ollama/types"
	concurrent "github.com/Marcuss-ops/PipelineGen/pkg/concurrent"
	retry "github.com/Marcuss-ops/PipelineGen/pkg/retry"

	"go.uber.org/zap"
)

// Generate genera testo con Ollama (Legacy API)
func (c *Client) Generate(ctx context.Context, prompt string) (string, error) {
	return c.GenerateWithOptions(ctx, c.model, prompt, nil)
}

// GenerateMetrics carries the timing/token facts returned by Ollama's legacy
// /api/generate endpoint. Durations are nanoseconds, matching Ollama's wire
// contract, so callers can derive cold-start and token-throughput metrics.
type GenerateMetrics struct {
	Model                string
	LoadDurationNS       int64
	PromptEvalCount      int64
	PromptEvalDurationNS int64
	EvalCount            int64
	EvalDurationNS       int64
	TotalDurationNS      int64
}

func (m *GenerateMetrics) ModelLoadMS() int64 {
	if m == nil {
		return 0
	}
	return m.LoadDurationNS / 1e6
}

func (m *GenerateMetrics) TokensPerSecond() float64 {
	if m == nil || m.EvalDurationNS <= 0 {
		return 0
	}
	return float64(m.EvalCount) / (float64(m.EvalDurationNS) / 1e9)
}

// GenerateResult is the metrics-aware result of a legacy generation call.
type GenerateResult struct {
	Content string
	Metrics *GenerateMetrics
}

// GenerateWithOptions genera testo con opzioni esplicite (Legacy API).
// It remains a string-returning compatibility wrapper; use GenerateDetailed
// when the caller needs Ollama timing/token facts.
func (c *Client) GenerateWithOptions(ctx context.Context, model, prompt string, options map[string]any) (string, error) {
	result, err := c.GenerateDetailed(ctx, model, prompt, options)
	if err != nil {
		return "", err
	}
	return result.Content, nil
}

// GenerateDetailed is the metrics-aware legacy /api/generate call.
func (c *Client) GenerateDetailed(ctx context.Context, model, prompt string, options map[string]any) (GenerateResult, error) {
	if model == "" {
		model = c.model
	}

	format, think, keepAlive, options := extractGenerateControls(options)
	req := types.GenerateRequest{
		Model:     model,
		Prompt:    prompt,
		Stream:    false,
		KeepAlive: keepAlive,
		Think:     think,
		Format:    format,
		Options:   options,
	}

	body, err := json.Marshal(req)
	if err != nil {
		return GenerateResult{}, fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/api/generate", bytes.NewReader(body))
	if err != nil {
		return GenerateResult{}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return GenerateResult{}, fmt.Errorf("ollama request failed: %w", err)
		}
		return GenerateResult{}, fmt.Errorf("ollama request failed: %w", &retry.TransientInfrastructureError{Err: err})
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode == http.StatusServiceUnavailable || resp.StatusCode == http.StatusBadGateway || resp.StatusCode == http.StatusGatewayTimeout {
			return GenerateResult{}, fmt.Errorf("ollama returned status %d: %w", resp.StatusCode, &retry.TransientInfrastructureError{Err: fmt.Errorf("ollama returned status %d", resp.StatusCode)})
		}
		return GenerateResult{}, fmt.Errorf("ollama returned status %d", resp.StatusCode)
	}

	var result types.GenerateResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return GenerateResult{}, fmt.Errorf("failed to decode response: %w", err)
	}

	logger.Info("Ollama generate response received",
		zap.Int("chars", len(result.Response)),
	)

	return GenerateResult{
		Content: result.Response,
		Metrics: &GenerateMetrics{
			Model:                model,
			LoadDurationNS:       result.LoadDuration,
			PromptEvalCount:      result.PromptEvalCount,
			PromptEvalDurationNS: result.PromptEvalDuration,
			EvalCount:            result.EvalCount,
			EvalDurationNS:       result.EvalDuration,
			TotalDurationNS:      result.TotalDuration,
		},
	}, nil
}

// extractGenerateFormat keeps the public options map backward-compatible
// while placing Ollama's structured-output format at the request level. The
// Ollama API ignores format when it is nested inside options.
func extractGenerateControls(options map[string]any) (any, *bool, string, map[string]any) {
	if len(options) == 0 {
		return nil, nil, "30m", nil
	}
	copyOptions := make(map[string]any, len(options))
	var format any
	var think *bool
	keepAlive := "30m"
	for key, value := range options {
		if key == "format" {
			format = value
			continue
		}
		if key == "think" {
			if enabled, ok := value.(bool); ok {
				think = &enabled
			}
			continue
		}
		if key == "keep_alive" {
			if value, ok := value.(string); ok && strings.TrimSpace(value) != "" {
				keepAlive = value
			}
			continue
		}
		copyOptions[key] = value
	}
	if len(copyOptions) == 0 {
		copyOptions = nil
	}
	return format, think, keepAlive, copyOptions
}

// SimpleGenerate is a convenience wrapper for the common pattern of calling
// /api/generate with a per-call timeout and model override. It wraps
// GenerateWithOptions and applies context.WithTimeout when timeout > 0.
// Pass opts=nil when no extra options are needed.
func (c *Client) SimpleGenerate(ctx context.Context, model, prompt string, timeout time.Duration, opts map[string]any) (string, error) {
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	return c.GenerateWithOptions(ctx, model, prompt, opts)
}

// GenerateStream genera testo con Ollama in modalità streaming
func (c *Client) GenerateStream(ctx context.Context, prompt string) (<-chan string, <-chan error) {
	return c.GenerateStreamWithOptions(ctx, c.model, prompt, nil)
}

// GenerateStreamWithOptions genera testo con opzioni esplicite in modalità streaming.
func (c *Client) GenerateStreamWithOptions(ctx context.Context, model, prompt string, options map[string]any) (<-chan string, <-chan error) {
	textChan := make(chan string, types.StreamBufferSize)
	errChan := make(chan error, 1)

	if model == "" {
		model = c.model
	}

	format, think, keepAlive, requestOptions := extractGenerateControls(options)
	req := types.GenerateRequest{
		Model:     model,
		Prompt:    prompt,
		Stream:    true,
		KeepAlive: keepAlive,
		Think:     think,
		Format:    format,
		Options:   requestOptions,
	}

	body, err := json.Marshal(req)
	if err != nil {
		errChan <- fmt.Errorf("failed to marshal request: %w", err)
		close(textChan)
		close(errChan)
		return textChan, errChan
	}

	concurrent.SafeGo("ollama-generate-stream", func() {
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
			if errors.Is(err, context.Canceled) {
				errChan <- fmt.Errorf("ollama request failed: %w", err)
			} else {
				errChan <- fmt.Errorf("ollama request failed: %w", &retry.TransientInfrastructureError{Err: err})
			}
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			if resp.StatusCode == http.StatusServiceUnavailable || resp.StatusCode == http.StatusBadGateway || resp.StatusCode == http.StatusGatewayTimeout {
				errChan <- fmt.Errorf("ollama returned status %d: %w", resp.StatusCode, &retry.TransientInfrastructureError{Err: fmt.Errorf("ollama returned status %d", resp.StatusCode)})
			} else {
				errChan <- fmt.Errorf("ollama returned status %d", resp.StatusCode)
			}
			return
		}

		decoder := json.NewDecoder(resp.Body)
		for {
			var result types.GenerateResponse
			if err := decoder.Decode(&result); err != nil {
				if errors.Is(err, io.EOF) {
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
	})

	return textChan, errChan
}
