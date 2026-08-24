// Package health provides concrete health-check adapters.
package health

import (
	"context"
	"fmt"
	"time"

	healthport "github.com/Marcuss-ops/PipelineGen/internal/application/system/health"
)

// DriveAboutProbe is the canonical port for a canonical OAuth Drive client.
// When set on a DriveChecker, CheckDrive calls Probe(ctx) directly,
// which reuses the production OAuth client with automatic token refresh
// and the canonical OAuth flow.
//
// Wave A Item 31 (June 2026): DriveChecker now holds ONLY this probe
// field. The legacy credsPath/tokenPath/aboutURL/client fields and the
// drive.ParseTokenFile + raw HTTP GET fallback have been REMOVED —
// the canonical composition wires drive.Admin.Ping (the production
// OAuth client) into DriveProbe, and the legacy token-file + raw HTTP
// GET path was the wrong boundary (it duplicated OAuth state, ignored
// token refresh, and forced the health check to depend on filesystem
// state rather than the production client). Deployments that do not
// wire a probe simply report applicable=false on /health — the canonical
// "capability is not configured" reporting contract.
type DriveAboutProbe func(ctx context.Context) error

// DriveChecker verifies Google Drive connectivity via the canonical
// DriveAboutProbe.
//
// Wave A Item 31 (June 2026): the legacy token-file + raw HTTP GET
// fallback has been removed. DriveChecker is now a thin wrapper around
// the typed DriveAboutProbe port. The production composition wires
// drive.Admin.Ping (which wraps About.Get internally with the
// canonical OAuth client + automatic token refresh) into DriveProbe;
// the admin CLI path simply leaves DriveProbe nil and surfaces
// applicable=false on the /health endpoint.
type DriveChecker struct {
	DriveProbe DriveAboutProbe
}

// NewDriveChecker returns a DriveChecker with no probe wired. The
// caller (composition root) is responsible for setting DriveProbe if
// a Drive capability is configured. The ctor takes no arguments
// because the legacy credsPath/tokenPath discovery has been
// REMOVED (Wave A Item 31) — the canonical probe is the only
// supported way to verify Drive, and the OAuth client is loaded
// exactly once at composition time (godlike/06 single-source rule).
func NewDriveChecker() *DriveChecker {
	return &DriveChecker{}
}

// CheckDrive reports the Drive capability status.
//
//   - DriveProbe == nil → {ok: true, applicable: false, note: "Drive
//     credentials not configured"}. This is the canonical "capability
//     not configured" reporting contract; /health does not 503 on
//     Drive-disabled deployments (matches the pre-Wave-A semantic for
//     the no-credentials path).
//   - DriveProbe != nil, returns nil → {ok: true, configured: true,
//     duration_ms}.
//   - DriveProbe != nil, returns error → {ok: false, error: "Drive
//     probe failed: <err>", duration_ms}.
func (c *DriveChecker) CheckDrive(ctx context.Context) healthport.CheckResult {
	start := time.Now()

	if c.DriveProbe == nil {
		return healthport.CheckResult{
			"ok":          true,
			"applicable":  false,
			"duration_ms": time.Since(start).Milliseconds(),
			"note":        "Drive credentials not configured",
		}
	}

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
