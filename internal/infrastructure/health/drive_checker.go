// Package health provides concrete health-check adapters.
package health

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	healthport "github.com/Marcuss-ops/PipelineGen/internal/application/system/health"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
)

// DriveAboutProbe is an optional port for a canonical OAuth Drive client.
// When set on a DriveChecker, CheckDrive calls Probe(ctx) instead of
// manually reading the token file and issuing a raw HTTP GET to the Drive
// About API. The production composition wires a *gdrive.Service.About.Get
// adapter here so the health probe uses automatic token refresh and the
// canonical OAuth flow.
//
// codex/health-ready-contract (June 2026): resolves the TODO at the
// original line 22 — "prefer reusing a probe on the canonical OAuth
// Drive client".
type DriveAboutProbe func(ctx context.Context) error

// DriveChecker verifies Google Drive connectivity.
//
// When DriveProbe is non-nil, CheckDrive uses it for the liveness check
// (canonical OAuth client with automatic token refresh). When nil, the
// legacy token-file + raw HTTP approach is used as a fallback.
//
// fix(health) close-out (June 2026, problem #2 final cleanup): the
// inline JSON token parse was extracted to drive.ParseTokenFile
// (internal/infrastructure/drive/tokensource.go) so the parsing logic
// has its own testable surface independent of the HTTP request path.
type DriveChecker struct {
	credsPath  string
	tokenPath  string
	aboutURL   string // overridable for tests via direct struct literal (httptest.Server URL)
	client     *http.Client
	DriveProbe DriveAboutProbe // optional: canonical OAuth Drive client probe (nil → fall back to token-file HTTP)
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

	// codex/health-ready-contract (June 2026): when a canonical
	// DriveAboutProbe is wired, use it instead of the token-file path.
	// This reuses the production OAuth client with automatic token
	// refresh and eliminates the manual token-file read + raw HTTP GET.
	if c.DriveProbe != nil {
		if err := c.DriveProbe(ctx); err != nil {
			return healthport.CheckResult{
				"ok":          false,
				"duration_ms": time.Since(start).Milliseconds(),
				"error":       fmt.Sprintf("Drive probe failed: %v", err),
			}
		}
		return healthport.CheckResult{
			"ok":          true,
			"duration_ms": time.Since(start).Milliseconds(),
			"configured":  true,
		}
	}

	if c.credsPath == "" || c.tokenPath == "" {
		return healthport.CheckResult{
			"ok": true, "applicable": false,
			"duration_ms": time.Since(start).Milliseconds(),
			"note":        "Drive credentials not configured",
		}
	}

	accessToken, err := drive.ParseTokenFile(c.tokenPath)
	if err != nil {
		switch {
		case errors.Is(err, drive.ErrTokenUnreadable):
			return healthport.CheckResult{
				"ok":          false,
				"duration_ms": time.Since(start).Milliseconds(),
				"error":       drive.ErrTokenUnreadable.Error(),
			}
		case errors.Is(err, drive.ErrTokenInvalidAccessToken):
			return healthport.CheckResult{
				"ok":          false,
				"duration_ms": time.Since(start).Milliseconds(),
				"error":       drive.ErrTokenInvalidAccessToken.Error(),
			}
		case errors.Is(err, drive.ErrTokenUnavailable):
			return healthport.CheckResult{
				"ok":          false,
				"duration_ms": time.Since(start).Milliseconds(),
				"error":       err.Error(),
			}
		}
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
