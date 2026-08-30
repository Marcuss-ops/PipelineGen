package client

import (
	"context"
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/ollama/types"
)

// WarmModel makes model residency an explicit, singleflight operation. The
// first caller pays the load; concurrent scene callers wait for that same
// load instead of independently entering Ollama's cold-start window.
func (c *Client) WarmModel(ctx context.Context, model string) error {
	if c == nil {
		return fmt.Errorf("ollama client is nil")
	}
	model = strings.TrimSpace(model)
	if model == "" {
		model = c.model
	}
	if model == "" || c.useVLLM || c.useNvidiaForLLM {
		return nil
	}

	_, err, _ := c.warmModelGroup.Do(model, func() (any, error) {
		resident, checkErr := c.IsModelResident(ctx, model)
		if checkErr != nil {
			return nil, fmt.Errorf("verify live residency for %q: %w", model, checkErr)
		}
		if resident {
			return nil, nil
		}

		// A minimal chat request forces Ollama to load the model while keeping
		// it resident. The real scene fan-out starts only after this returns.
		_, err := c.ChatDetailed(ctx, []types.Message{{Role: "system", Content: "warmup"}}, map[string]any{
			// Match the short-scene bucket used by the real fan-out. Ollama can
			// reload/reconfigure a resident model when the context changes, so a
			// 2048 probe followed by 4096 scene calls would create a false warmup.
			"model": model, "num_predict": 1, "num_ctx": 4096,
		}, nil)
		if err != nil {
			return nil, err
		}
		return nil, nil
	})
	if err != nil {
		return fmt.Errorf("warm Ollama model %q: %w", model, err)
	}
	return nil
}
