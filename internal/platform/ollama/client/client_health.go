package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/ollama/types"
)

// CheckHealth checks if Ollama is reachable
func (c *Client) CheckHealth(ctx context.Context) bool {
	req, err := http.NewRequestWithContext(ctx, "GET", c.baseURL+"/api/tags", nil)
	if err != nil {
		return false
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()

	return resp.StatusCode == http.StatusOK
}

// ListModels returns the list of available models
func (c *Client) ListModels(ctx context.Context) ([]types.Model, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", c.baseURL+"/api/tags", nil)
	if err != nil {
		return nil, err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to list models: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ollama returned status %d", resp.StatusCode)
	}

	var result types.ListModelsResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return result.Models, nil
}

// ListRunningModels returns models currently loaded in Ollama. This is the
// source of truth for residency; /api/tags only reports installed models.
func (c *Client) ListRunningModels(ctx context.Context) ([]types.RunningModel, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", c.baseURL+"/api/ps", nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to list running models: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ollama returned status %d from /api/ps", resp.StatusCode)
	}
	var result types.ListRunningModelsResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode running models response: %w", err)
	}
	return result.Models, nil
}

// IsModelResident checks live Ollama state. It deliberately does not trust
// the client's TTL cache because Ollama may evict a model independently.
func (c *Client) IsModelResident(ctx context.Context, model string) (bool, error) {
	model = strings.TrimSpace(model)
	if model == "" {
		model = c.model
	}
	models, err := c.ListRunningModels(ctx)
	if err != nil {
		return false, err
	}
	for _, running := range models {
		// Ollama may return a digest-qualified name while callers use the
		// configured tag. Compare the canonical tag prefix as well as the
		// exact wire name, without treating /api/tags availability as residency.
		name := strings.TrimSpace(running.Name)
		if name == model || strings.TrimSuffix(name, "@"+strings.TrimPrefix(name, "@")) == model {
			return true, nil
		}
		if at := strings.IndexByte(name, '@'); at > 0 && name[:at] == model {
			return true, nil
		}
	}
	return false, nil
}
