// Package health provides concrete health-check adapters.
package health

import (
	"context"
	"fmt"
	"net/http"
	"time"

	healthport "github.com/Marcuss-ops/PipelineGen/internal/application/system/health"
)

// QdrantChecker verifies the vector index backend is reachable.
//
// QDRANT-005 (June 2026): the checker performs a lightweight GET on
// {baseURL}/collections — the cheapest Qdrant endpoint that exercises
// HTTP plumbing AND schema resolution. Using a heavier check (scroll,
// search) would stall the health endpoint under transient Qdrant
// slowness without adding signal.
//
// When disabled (Enabled=false), the checker returns {ok:true, applicable:false}
// so /health?check=qdrant doesn't report unhealthy when Qdrant is
// intentionally opted out.
type QdrantChecker struct {
	baseURL string
	apiKey  string
	enabled bool
	client  *http.Client
}

// NewQdrantChecker creates a Qdrant health checker. When enabled is false,
// CheckQdrant returns {ok:true, applicable:false} without contacting Qdrant.
func NewQdrantChecker(baseURL, apiKey string, enabled bool) *QdrantChecker {
	return &QdrantChecker{
		baseURL: baseURL,
		apiKey:  apiKey,
		enabled: enabled,
		client:  &http.Client{Timeout: 3 * time.Second},
	}
}

// CheckQdrant implements healthport.QdrantChecker.
func (c *QdrantChecker) CheckQdrant(ctx context.Context) healthport.CheckResult {
	start := time.Now()

	if !c.enabled {
		return healthport.CheckResult{
			"ok":          true,
			"applicable":  false,
			"duration_ms": time.Since(start).Milliseconds(),
			"note":        "Qdrant capability not enabled",
		}
	}

	if c.baseURL == "" {
		return healthport.CheckResult{
			"ok":          false,
			"duration_ms": time.Since(start).Milliseconds(),
			"error":       "Qdrant base URL not configured",
		}
	}

	url := c.baseURL + "/collections"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return healthport.CheckResult{
			"ok":          false,
			"duration_ms": time.Since(start).Milliseconds(),
			"error":       fmt.Sprintf("failed to build Qdrant request: %v", err),
		}
	}
	if c.apiKey != "" {
		req.Header.Set("X-Api-Key", c.apiKey)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return healthport.CheckResult{
			"ok":          false,
			"duration_ms": time.Since(start).Milliseconds(),
			"error":       "Qdrant API unreachable",
		}
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return healthport.CheckResult{
			"ok":          false,
			"duration_ms": time.Since(start).Milliseconds(),
			"error":       fmt.Sprintf("Qdrant API returned HTTP %d", resp.StatusCode),
		}
	}

	return healthport.CheckResult{
		"ok":          true,
		"duration_ms": time.Since(start).Milliseconds(),
		"configured":  true,
	}
}

// Compile-time assertion: QdrantChecker satisfies the application port.
var _ healthport.QdrantChecker = (*QdrantChecker)(nil)
