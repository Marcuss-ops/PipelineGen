// Package health provides concrete health-check adapters.
package health

import (
	"context"
	"fmt"
	"net/http"
	"time"

	healthport "github.com/Marcuss-ops/PipelineGen/internal/application/system/health"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
)

// DriveChecker verifies Google Drive connectivity.
//
// fix(health) close-out (June 2026, problem #2 final cleanup): the
// inline JSON token parse was extracted to drive.ParseTokenFile
// (internal/infrastructure/drive/tokensource.go) so the parsing logic
// has its own testable surface independent of the HTTP request path.
//
// TODO(codex/health-ready-contract): prefer reusing a probe on the
// canonical OAuth Drive client (gdrive.Service.About.Get) instead of
// manually reading and using an access token. The production composition
// already constructs a *gdrive.Service with automatic token refresh —
// the DriveChecker should accept an optional Drive client port and use
// it for the About.Get probe. If this migration enlarges the PR, keep
// the existing token-file probe with this TODO and separate the port.
type DriveChecker struct {
	credsPath string
	tokenPath string
	aboutURL  string // overridable for tests via direct struct literal (httptest.Server URL)
	client    *http.Client
}

// defaultDriveAboutURL is the canonical Google Drive "about" probe.
const defaultDriveAboutURL = "https://www.googleapis.com/drive/v3/about?fields=user"

func NewDriveChecker(credsPath, tokenPath string) *DriveChecker {
	return &DriveChecker{
		credsPath: credsPath,
		tokenPath: tokenPath,
		aboutURL:  defaultDriveAboutURL,
		client:    &http.Client{Timeout: 3 * time.Second},
	}
}

func (c *DriveChecker) CheckDrive(ctx context.Context) healthport.CheckResult {
	start := time.Now()

	if c.credsPath == "" || c.tokenPath == "" {
		return healthport.CheckResult{
			"ok": true, "applicable": false,
			"duration_ms": time.Since(start).Milliseconds(),
			"note":        "Drive credentials not configured",
		}
	}

	accessToken, err := drive.ParseTokenFile(c.tokenPath)
	if err != nil {
		return healthport.CheckResult{
			"ok":          false,
			"duration_ms": time.Since(start).Milliseconds(),
			"error":       fmt.Sprintf("token unavailable: %v", err),
		}
	}

	req, err := http.NewRequestWithContext(ctx, "GET",
		c.aboutURL, nil)
	if err != nil {
		return healthport.CheckResult{
			"ok":          false,
			"duration_ms": time.Since(start).Milliseconds(),
			"error":       "failed to create Drive request",
		}
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := c.client.Do(req)
	if err != nil {
		return healthport.CheckResult{
			"ok":          false,
			"duration_ms": time.Since(start).Milliseconds(),
			"error":       "Drive API unreachable",
		}
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return healthport.CheckResult{
			"ok":          false,
			"duration_ms": time.Since(start).Milliseconds(),
			"error":       fmt.Sprintf("Drive API returned HTTP %d", resp.StatusCode),
		}
	}

	return healthport.CheckResult{
		"ok":          true,
		"duration_ms": time.Since(start).Milliseconds(),
		"configured":  true,
	}
}
