// Package health provides concrete health-check adapters.
package health

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	healthport "github.com/Marcuss-ops/PipelineGen/internal/application/system/health"
)

// QdrantChecker verifies the Qdrant vector store.
type QdrantChecker struct {
	url         string
	collection  string
	enabled     bool
	client      *http.Client
}

// NewQdrantChecker creates a Qdrant-health checker.
func NewQdrantChecker(url, collection string, enabled bool) *QdrantChecker {
	if url == "" {
		url = "http://127.0.0.1:6333"
	}
	return &QdrantChecker{
		url:        url,
		collection: collection,
		enabled:    enabled,
		client:     &http.Client{Timeout: 3 * time.Second},
	}
}

// CheckQdrant verifies the vector store is reachable.
func (c *QdrantChecker) CheckQdrant(ctx context.Context) healthport.CheckResult {
	start := time.Now()

	if !c.enabled {
		return healthport.CheckResult{
			"ok": true, "duration_ms": time.Since(start).Milliseconds(),
			"enabled": false, "note": "vector search disabled",
		}
	}

	readyzURL := fmt.Sprintf("%s/readyz", c.url)
	req, err := http.NewRequestWithContext(ctx, "GET", readyzURL, nil)
	if err != nil {
		return healthport.CheckResult{
			"ok": false, "duration_ms": time.Since(start).Milliseconds(),
			"error": "failed to create request",
		}
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return healthport.CheckResult{
			"ok": false, "duration_ms": time.Since(start).Milliseconds(),
			"error": "failed to connect to Qdrant",
		}
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return healthport.CheckResult{
			"ok": false, "duration_ms": time.Since(start).Milliseconds(),
			"error": fmt.Sprintf("Qdrant returned HTTP %d", resp.StatusCode),
		}
	}

	// Get collection points count.
	var pointsCount int64 = -1
	collURL := fmt.Sprintf("%s/collections/%s", c.url, c.collection)
	req2, err := http.NewRequestWithContext(ctx, "GET", collURL, nil)
	if err == nil {
		resp2, err := c.client.Do(req2)
		if err == nil {
			defer resp2.Body.Close()
			if resp2.StatusCode == http.StatusOK {
				var collResp struct {
					Result struct {
						PointsCount int64 `json:"points_count"`
					} `json:"result"`
				}
				if json.NewDecoder(resp2.Body).Decode(&collResp) == nil {
					pointsCount = collResp.Result.PointsCount
				}
			}
		}
	}

	result := healthport.CheckResult{
		"ok":          true,
		"enabled":     true,
		"collection":  c.collection,
		"duration_ms": time.Since(start).Milliseconds(),
	}
	if pointsCount >= 0 {
		result["points_count"] = pointsCount
	}

	return result
}
