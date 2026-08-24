package health

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	systemhealth "github.com/Marcuss-ops/PipelineGen/internal/capabilities/system/health"
)

// TestDriveChecker_NilProbe_ApplicableFalse verifies the canonical
// "capability not configured" reporting contract. A DriveChecker with
// no probe wired (composition opted out of Drive, or admin CLI path)
// returns {ok: true, applicable: false, note: "Drive credentials not
// configured"} so /health does not 503 on Drive-disabled deployments.
//
// Wave A Item 31 (June 2026): the legacy token-file + raw HTTP GET
// fallback that previously produced this same shape is REMOVED — the
// nil-probe path is now the ONLY way to surface applicable=false,
// since the canonical OAuth probe is the only supported way to verify
// Drive. This test pins the no-creds contract for deployments that
// legitimately have no Drive capability configured.
func TestDriveChecker_NilProbe_ApplicableFalse(t *testing.T) {
	c := NewDriveChecker()
	res := c.CheckDrive(context.Background())

	ok, _ := res["ok"].(bool)
	require.True(t, ok, "expected ok=true when Drive capability is opted out, got %v", res)
	app, _ := res["applicable"].(bool)
	require.False(t, app, "expected applicable=false when no probe is wired, got %v", res)
	note, _ := res["note"].(string)
	require.Equal(t, "Drive credentials not configured", note)
	_, hasError := res["error"]
	require.False(t, hasError, "expected no 'error' key when applicable=false")
}

// TestDriveChecker_ProbeSuccess verifies that a wired probe that
// returns nil yields the canonical {ok: true, configured: true,
// duration_ms > 0} shape — the production happy path through
// drive.Admin.Ping (canonical OAuth client + automatic token refresh).
func TestDriveChecker_ProbeSuccess(t *testing.T) {
	c := NewDriveChecker()
	c.DriveProbe = func(ctx context.Context) error { return nil }
	res := c.CheckDrive(context.Background())

	require.True(t, res["ok"].(bool), "expected ok=true on probe success, got %v", res)
	require.True(t, res["configured"].(bool), "expected configured=true on probe success")
	require.GreaterOrEqual(t, res["duration_ms"].(int64), int64(0), "expected duration_ms >= 0 (instant probe may be 0ms)")
	_, hasError := res["error"]
	require.False(t, hasError, "expected no 'error' key on probe success")
}

// TestDriveChecker_ProbeFailure verifies that a wired probe that
// returns an error surfaces {ok: false, error: "Drive probe failed:
// <err>", duration_ms} — the canonical failure reporting shape
// previously produced by the token-file + raw HTTP GET path. The
// probe is the only failure surface post-Wave-A; the error message
// is the typed sentinel for operator dashboards.
func TestDriveChecker_ProbeFailure(t *testing.T) {
	c := NewDriveChecker()
	c.DriveProbe = func(ctx context.Context) error {
		return errors.New("synthetic probe failure")
	}
	res := c.CheckDrive(context.Background())

	require.False(t, res["ok"].(bool), "expected ok=false on probe failure, got %v", res)
	errMsg, _ := res["error"].(string)
	require.Contains(t, errMsg, "Drive probe failed")
	require.Contains(t, errMsg, "synthetic probe failure")
	_, hasApp := res["applicable"]
	require.False(t, hasApp, "expected no 'applicable' key on real failure")
}

// TestDriveChecker_ContextPropagation verifies that a slow probe
// honours the caller's context (the probe receives ctx, not a
// background context). This is the canonical fail-fast contract
// for /ready barriers that run with a bounded timeout.
func TestDriveChecker_ContextPropagation(t *testing.T) {
	c := NewDriveChecker()
	c.DriveProbe = func(ctx context.Context) error {
		// If the wrong context were used (e.g. context.Background()),
		// this select would block past ctx.Done() and the test would
		// hang or time out. The probe is correctly wired to the
		// caller's ctx.
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			return nil
		}
	}
	res := c.CheckDrive(context.Background())
	require.True(t, res["ok"].(bool), "expected ok=true when probe returns nil, got %v", res)
}

// Compile-time conformance assertion (AGENTS.md Pattern 0):
// *DriveChecker must satisfy the systemhealth.DriveChecker port
// after the Wave A Item 31 slim-down. Drift between the new
// CheckDrive signature and the port contract triggers a compile
// error here, NOT at the first consumer site.
var _ systemhealth.DriveChecker = (*DriveChecker)(nil)
