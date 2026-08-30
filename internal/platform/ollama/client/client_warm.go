package client

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/ollama/types"
)

const modelResidencyTTL = 30 * time.Minute

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

	c.residencyMu.Lock()
	resident := time.Now().Before(c.residentUntil[model])
	c.residencyMu.Unlock()
	if resident {
		return nil
	}

	_, err, _ := c.warmModelGroup.Do(model, func() (any, error) {
		c.residencyMu.Lock()
		if time.Now().Before(c.residentUntil[model]) {
			c.residencyMu.Unlock()
			return nil, nil
		}
		c.residencyMu.Unlock()

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
		c.residencyMu.Lock()
		c.residentUntil[model] = time.Now().Add(modelResidencyTTL)
		c.residencyMu.Unlock()
		return nil, nil
	})
	if err != nil {
		return fmt.Errorf("warm Ollama model %q: %w", model, err)
	}
	return nil
}
