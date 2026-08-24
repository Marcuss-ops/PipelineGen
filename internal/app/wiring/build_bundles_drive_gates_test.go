// Package app — build_bundles_drive_gates_test.go: TDD coverage for the
// PR-DRIVE-AVAILABILITY-GATE composition-root fail-closed helper
// (mirror of PR-QDRANT-CONFIG-MISMATCH-GATE / ART-002 P0.1 test surface
// — godlike/06 SSOT).
//
// Scope (per architecture/current.yaml#PR-DRIVE-AVAILABILITY-GATE.linked_issues):
//   - TestValidateDriveServiceAvailability_NilCfg_ReturnsError:
//     defensive nil-guard coverage (godlike/06 SSOT surface).
//   - TestValidateDriveServiceAvailability_StrictModeAndBothFilesPresent_ReturnsNil:
//     the canonical happy path (operator has valid credentials.json +
//     token.json on disk AND strict-mode is on; gate is a no-op).
//   - TestValidateDriveServiceAvailability_StrictModeAndCredentialsMissing_FailsClosed:
//     the CRITICAL RED POINT surfaced by the original 500-panic report:
//     credentials.json is missing from disk; *drive.Uploader.Service is nil;
//     POST /api/media/register-batch with folder_id non-empty would 500.
//     The helper fail-closes the misconfiguration at boot per godlike/07
//     no-fake-availability.
//   - TestValidateDriveServiceAvailability_StrictModeAndTokenMissing_FailsClosed:
//     symmetric to the credentials case: token.json missing is the canonical
//     "Drive auth was once wired but the token expired or got deleted"
//     failure mode. Same fail-closed contract.
//   - TestValidateDriveServiceAvailability_SoftModeAndFilesMissing_ReturnsNil:
//     operators opting out of the fail-fast-at-boot contract via
//     VELOX_DRIVE_STRICT_STARTUP_VALIDATION=false retain the soft-mode
//     pre-PR surface (boot proceeds; handler-level preflight still
//     fail-closed 503 at request time per godlike/07 defense-in-depth).
//
// The 3rd + 4th tests are the canonical godlike/07 no-fake-availability
// cases (the entire reason this helper exists). They pin BOTH the
// failing condition AND the actionable fix hint so future operators
// can copy/paste the env-var names + commands from the runtime error
// message into their deployment config without consulting docs.
//
// Defense-in-depth preview: this helper is called once at TOF
// BuildDriveBundle. The unit-test surface targets the helper directly
// per godlike/06 SSOT; per-call-site integration coverage lives in
// the composition-test canary (future PR).
package wiring

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

// withTempDriveFiles creates a temp directory with both credentials.json
// AND token.json (empty files; existence is what the gate probes).
// Returns the temp dir; callers should defer os.RemoveAll.
func withTempDriveFiles(t *testing.T) (dir string, credPath string, tokenPath string) {
	t.Helper()
	dir = t.TempDir()
	credPath = filepath.Join(dir, "credentials.json")
	tokenPath = filepath.Join(dir, "token.json")
	require.NoError(t, os.WriteFile(credPath, []byte(`{"installed":{"client_id":"x","client_secret":"y"}}`), 0600))
	require.NoError(t, os.WriteFile(tokenPath, []byte(`{"access_token":"a","refresh_token":"r","token_type":"Bearer"}`), 0600))
	return dir, credPath, tokenPath
}

// cfgWithDrivePaths builds a minimal config with strict-mode turned ON
// AND CredentialsFile / TokenFile wired to the supplied paths. Returns
// a pointer the caller can mutate further.
func cfgWithDrivePaths(credPath, tokenPath string, strictMode bool) *config.Config {
	return &config.Config{
		Drive: config.DriveConfig{
			StrictStartupValidation: strictMode,
		},
		Paths: config.PathsConfig{
			CredentialsFile: credPath,
			TokenFile:       tokenPath,
		},
	}
}

// TestValidateDriveServiceAvailability_NilCfg_ReturnsError: defensive
// coverage for the nil-cfg case. Returns a typed error so the single
// BuildDriveBundle call site propagates the godlike/07 fail-closed
// pattern: the helper is invoked UPFRONT and any error short-circuits
// the enclosing fn before it dereferences cfg for its own reads.
func TestValidateDriveServiceAvailability_NilCfg_ReturnsError(t *testing.T) {
	err := validateDriveServiceAvailability(nil)
	require.Error(t, err, "nil cfg must fail-closed (godlike/06 SSOT defensive surface)")
	assert.Contains(t, err.Error(), "cfg is nil",
		"error must name the nil-receiver condition so operators can grep it in logs")
	assert.Contains(t, err.Error(), "PR-DRIVE-AVAILABILITY-GATE",
		"error must cite the wave-tracker anchor for audit traceability")
}

// TestValidateDriveServiceAvailability_StrictModeAndBothFilesPresent_ReturnsNil:
// when the operator has wired credentials.json + token.json correctly
// AND strict-mode is on (the canonical production happy path), the
// gate is a no-op and BuildDriveBundle proceeds with the real
// *drive.Uploader instantiation. This pins the canonical success path.
func TestValidateDriveServiceAvailability_StrictModeAndBothFilesPresent_ReturnsNil(t *testing.T) {
	_, credPath, tokenPath := withTempDriveFiles(t)
	cfg := cfgWithDrivePaths(credPath, tokenPath, true /* strictMode */)
	err := validateDriveServiceAvailability(cfg)
	assert.NoError(t, err,
		"strict-mode + both files present is the canonical operator-correct happy path — gate is a no-op")
}

// TestValidateDriveServiceAvailability_StrictModeAndCredentialsMissing_FailsClosed:
// the CANONICAL godlike/07 no-fake-availability case from the original
// 500-panic report. credentials.json is missing from disk AND
// strict-mode is on — *drive.Uploader.Service is nil and the
// BatchRegisterFromYouTube handler NEVER catches the misconfiguration
// because the panic happens DEEP inside sourcing.Service on the
// first clip registration that has folder_id non-empty. The helper
// aborts the boot loudly with an actionable fix hint naming the
// canonical default path AND the soft-mode escape hatch.
func TestValidateDriveServiceAvailability_StrictModeAndCredentialsMissing_FailsClosed(t *testing.T) {
	_, _, tokenPath := withTempDriveFiles(t)                        // partial setup: token OK, creds MISSING
	bogusCredPath := filepath.Join(t.TempDir(), "credentials.json") // not written
	cfg := cfgWithDrivePaths(bogusCredPath, tokenPath, true /* strictMode */)

	err := validateDriveServiceAvailability(cfg)
	require.Error(t, err,
		"PR-DRIVE-AVAILABILITY-GATE: strict-mode + missing credentials.json must fail-closed (godlike/07 no-fake-availability; the RED POINT surfaced by the original 500-panic bug)")

	// 5-substring contract assertion (godlike/07 fail-closed coupling).
	assert.Contains(t, err.Error(), bogusCredPath,
		"error must surface the failing file path so operators can grep it in deployment config")
	assert.Contains(t, err.Error(), "credentials file not found",
		"error must name the failing file class (NOT just propagate os.Stat.IsNotExist)")
	assert.Contains(t, err.Error(), "credentials.json",
		"error must name the canonical default filename for grep-ability across operators")
	assert.Contains(t, err.Error(), "PR-DRIVE-AVAILABILITY-GATE",
		"error must cite the wave-tracker anchor for audit traceability")
	assert.Contains(t, err.Error(), "VELOX_DRIVE_STRICT_STARTUP_VALIDATION=false",
		"error must include the soft-mode escape hatch for operators who don't need Drive")
}

// TestValidateDriveServiceAvailability_StrictModeAndTokenMissing_FailsClosed:
// symmetric to the credentials case. token.json missing is the canonical
// "Drive auth was once wired but the token expired or got deleted"
// failure mode — same BootCrashWithoutHelp class. The helper fail-closes
// with the canonical token-regeneration command per AGENTS.md
// §"Drive Token Regeneration" so operators get the right command in
// the boot log without consulting docs.
func TestValidateDriveServiceAvailability_StrictModeAndTokenMissing_FailsClosed(t *testing.T) {
	_, credPath, _ := withTempDriveFiles(t)                    // partial setup: creds OK, token MISSING
	bogusTokenPath := filepath.Join(t.TempDir(), "token.json") // not written
	cfg := cfgWithDrivePaths(credPath, bogusTokenPath, true /* strictMode */)

	err := validateDriveServiceAvailability(cfg)
	require.Error(t, err,
		"PR-DRIVE-AVAILABILITY-GATE: strict-mode + missing token.json must fail-closed (godlike/07 no-fake-availability)")

	// 5-substring contract assertion — note the regeneration-cmd substring
	// differs from the credentials case (the actionable fix is the
	// `generate_drive_token.py` command, not file-rename).
	assert.Contains(t, err.Error(), bogusTokenPath,
		"error must surface the failing file path so operators can grep it in deployment config")
	assert.Contains(t, err.Error(), "token file not found",
		"error must name the failing file class")
	assert.Contains(t, err.Error(), "token.json",
		"error must name the canonical default filename for grep-ability across operators")
	assert.Contains(t, err.Error(), "scripts/generate_drive_token.py",
		"error must name the canonical AGENTS.md regeneration command so operators get the right runbook step")
	assert.Contains(t, err.Error(), "VELOX_DRIVE_STRICT_STARTUP_VALIDATION=false",
		"error must include the soft-mode escape hatch for operators who don't need Drive")
}

// TestValidateDriveServiceAvailability_SoftModeAndFilesMissing_ReturnsNil:
// operators opting out of the fail-fast-at-boot contract via
// VELOX_DRIVE_STRICT_STARTUP_VALIDATION=false bypass the gate. Both
// credential + token files are missing, but soft-mode says "boot
// anyway, fail at request time". The pre-existing log.Warn
// diagnostic at build_bundles_drive.go:60-66 still surfaces WHY
// Drive auth failed. This pins the soft-mode pass-through contract.
func TestValidateDriveServiceAvailability_SoftModeAndFilesMissing_ReturnsNil(t *testing.T) {
	bogusCredPath := filepath.Join(t.TempDir(), "credentials.json") // not written
	bogusTokenPath := filepath.Join(t.TempDir(), "token.json")      // not written
	cfg := cfgWithDrivePaths(bogusCredPath, bogusTokenPath, false /* softMode */)

	err := validateDriveServiceAvailability(cfg)
	assert.NoError(t, err,
		"soft-mode (StrictStartupValidation=false) bypasses the boot gate entirely; "+
			"the handler-level preflight at BatchRegisterFromYouTube still fail-closed 503 at request time "+
			"per godlike/07 defense-in-depth")
}
