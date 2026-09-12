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
	return c.IsModelResidentWithContext(ctx, model, 0)
}

// IsModelResidentWithContext checks that a live model has the requested
// runner context as well as the requested name. Ollama may keep the same
// model loaded while rebuilding the runner for a different context length;
// treating that state as ready would move the cold reload into the first
// production request.
func (c *Client) IsModelResidentWithContext(
	ctx context.Context, model string, requiredContext int64,
) (bool, error) {
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
		nameMatches := name == model ||
			strings.TrimSuffix(name, "@"+strings.TrimPrefix(name, "@")) == model
		if at := strings.IndexByte(name, '@'); at > 0 && name[:at] == model {
			nameMatches = true
		}
		if nameMatches && (requiredContext <= 0 || running.ContextLength == requiredContext) {
			return true, nil
		}
	}
	return false, nil
}
