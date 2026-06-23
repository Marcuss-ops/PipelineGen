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

// QdrantChecker verifies the Qdrant vector store is ready AND the
// required collection is present.
//
// fix(health) close-out (June 2026, problem #2 final cleanup): the
// previous implementation probed /readyz + best-effort /collections/<n>
// (nested `if err == nil` chains silently degraded a missing-collection
// signal into nil points_count). Qdrant reports /readyz=OK on a
// broker that has no collections, so /readyz alone is insufficient —
// the collection presence probe is now HARD-required: a non-200 from
// /collections/<c> flips ok=false with the diagnostic message
// pinned for ops triage.
type QdrantChecker struct {
	url        string
	collection string
	enabled    bool
	client     *http.Client
}

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

func (c *QdrantChecker) CheckQdrant(ctx context.Context) healthport.CheckResult {
	start := time.Now()

	if !c.enabled {
		return healthport.CheckResult{
			"ok": true, "applicable": false,
			"duration_ms": time.Since(start).Milliseconds(),
			"note":        "vector search disabled",
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
			"error": fmt.Sprintf("Qdrant /readyz returned HTTP %d", resp.StatusCode),
		}
	}

	collURL := fmt.Sprintf("%s/collections/%s", c.url, c.collection)
	req2, err := http.NewRequestWithContext(ctx, "GET", collURL, nil)
	if err != nil {
		return healthport.CheckResult{
			"ok": false, "duration_ms": time.Since(start).Milliseconds(),
			"error": "failed to create collection request",
		}
	}
	resp2, err := c.client.Do(req2)
	if err != nil {
		return healthport.CheckResult{
			"ok": false, "duration_ms": time.Since(start).Milliseconds(),
			"error": fmt.Sprintf("Qdrant collection %q unreachable", c.collection),
		}
	}
	defer resp2.Body.Close()

	if resp2.StatusCode == http.StatusNotFound {
		return healthport.CheckResult{
			"ok": false, "duration_ms": time.Since(start).Milliseconds(),
			"error": fmt.Sprintf("Qdrant collection %q missing (404)", c.collection),
		}
	}
	if resp2.StatusCode != http.StatusOK {
		return healthport.CheckResult{
			"ok": false, "duration_ms": time.Since(start).Milliseconds(),
			"error": fmt.Sprintf("Qdrant collection %q returned HTTP %d", c.collection, resp2.StatusCode),
		}
	}

	var collResp struct {
		Result struct {
			Status      string `json:"status"`
			PointsCount int64  `json:"points_count"`
		} `json:"result"`
	}
	if err := json.NewDecoder(resp2.Body).Decode(&collResp); err != nil {
		return healthport.CheckResult{
			"ok": false, "duration_ms": time.Since(start).Milliseconds(),
			"error": fmt.Sprintf("Qdrant collection %q response malformed: %v", c.collection, err),
		}
	}

	result := healthport.CheckResult{
		"ok":           true,
		"enabled":      true,
		"collection":   c.collection,
		"duration_ms":  time.Since(start).Milliseconds(),
		"points_count": collResp.Result.PointsCount,
	}
	if collResp.Result.Status != "" {
		result["collection_status"] = collResp.Result.Status
	}
	return result
}
