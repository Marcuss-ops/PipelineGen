// Package health provides concrete health-check adapters.
package health

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"

	healthport "github.com/Marcuss-ops/PipelineGen/internal/application/system/health"
)

// DriveChecker verifies Google Drive connectivity.
type DriveChecker struct {
	credsPath string
	tokenPath string
	client    *http.Client
}

// NewDriveChecker creates a Drive-health checker.
func NewDriveChecker(credsPath, tokenPath string) *DriveChecker {
	return &DriveChecker{
		credsPath: credsPath,
		tokenPath: tokenPath,
		client:    &http.Client{Timeout: 3 * time.Second},
	}
}

// CheckDrive verifies the Drive token and API are reachable.
//
// fix/health-capabilities-optional Commit 2: when Drive credentials are
// not configured, return {ok: true, applicable: false} instead of
// {ok: false}. Deployments without Drive integration (e.g. unit tests,
// batch CI runners) should not have the health endpoint return HTTP
// 503 solely because the Drive capability is opted out. A check that
// is not applicable contributes ok=true and is excluded from the
// fail-closed aggregation in service.go::Check.
func (c *DriveChecker) CheckDrive(ctx context.Context) healthport.CheckResult {
	start := time.Now()

	if c.credsPath == "" || c.tokenPath == "" {
		return healthport.CheckResult{
			"ok": true, "applicable": false,
			"duration_ms": time.Since(start).Milliseconds(),
			"note":        "Drive credentials not configured",
		}
	}

	tokenBytes, err := os.ReadFile(c.tokenPath)
	if err != nil {
		return healthport.CheckResult{
			"ok": false, "duration_ms": time.Since(start).Milliseconds(),
			"error": "token file not readable",
		}
	}

	var tokenData struct {
		AccessToken string `json:"access_token"`
	}
	if json.Unmarshal(tokenBytes, &tokenData) != nil || tokenData.AccessToken == "" {
		return healthport.CheckResult{
			"ok": false, "duration_ms": time.Since(start).Milliseconds(),
			"error": "token file invalid or missing access_token",
		}
	}

	req, err := http.NewRequestWithContext(ctx, "GET",
		"https://www.googleapis.com/drive/v3/about?fields=user", nil)
	if err != nil {
		return healthport.CheckResult{
			"ok": false, "duration_ms": time.Since(start).Milliseconds(),
			"error": "failed to create Drive request",
		}
	}
	req.Header.Set("Authorization", "Bearer "+tokenData.AccessToken)

	resp, err := c.client.Do(req)
	if err != nil {
		return healthport.CheckResult{
			"ok": false, "duration_ms": time.Since(start).Milliseconds(),
			"error": "Drive API unreachable",
		}
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return healthport.CheckResult{
			"ok": false, "duration_ms": time.Since(start).Milliseconds(),
			"error": fmt.Sprintf("Drive API returned HTTP %d", resp.StatusCode),
		}
	}

	return healthport.CheckResult{
		"ok":          true,
		"duration_ms": time.Since(start).Milliseconds(),
		"configured":  true,
	}
}
