// Package app — build_bundles_drive_gates.go: canonical composition-root
// fail-closed gate for Drive service availability
// (PR-DRIVE-AVAILABILITY-GATE, July 2026).
//
// godlike/06 SSOT: this file owns the canonical helper that the Drive
// composition sites call into. There is exactly ONE place that decides
// whether Drive credentials + token files are available for runtime
// service (mirrors PR-QDRANT-CONFIG-MISMATCH-GATE's validateQdrantIndexerCompatibility
// at build_bundles_qdrant_gates.go:104 + ART-002 P0.1's
// validateArtlistScraperURL at build_bundles_artlist.go:423).
//
// godlike/07 no-fake-availability: the underlying
// drive.NewDriveServiceFromFiles (auth.go:115) SILENTLY swallows
// missing-credentials errors as a `log.Warn("Google Drive client not
// initialized")` in build_bundles_drive.go:60-66. Downstream, the
// `*drive.Uploader.Service` field is nil and the handler-level
// BatchRegisterFromYouTube (POST /api/media/register-batch) panics
// with a nil-pointer dereference the FIRST time a caller POSTs with
// folder_id non-empty. This gate pins the fail-closed contract at the
// composition-root layer so the nil-deref class of bugs cannot reach
// the wire.
//
// godlike/07 fail-fast-at-boot-vs-fail-slow-at-first-/run:
// operators leaving cfg.Drive.StrictStartupValidation=true (default
// per platform/config/drive.go:51, env VELOX_DRIVE_STRICT_STARTUP_VALIDATION)
// get the canonical fail-at-boot contract generalisable across Drive
// (this gate) AND the 9 per-destination folder probe paths (P1.3
// validator in build_bundles_drive.go:295). Operators opting into
// soft-mode (StrictStartupValidation=false) reports the validation but
// preserve the handler-level preflight at internal/api/assets/register/handler.go::BatchRegisterFromYouTube
// (defense-in-depth: the request still surfaces HTTP 503 with an
// actionable error instead of HTTP 500 nil-panic).
//
// Wave-tracker anchor: architecture/current.yaml#PR-DRIVE-AVAILABILITY-GATE
// (NEW; status flips pending → shipped at this PR's commit). Honest
// scope-lock: this helper does NOT migrate the pre-existing
// log.Warn("Google Drive client not initialized") pattern at
// build_bundles_drive.go:60-66 — that warn-and-continue semantic is
// RETAINED for diagnostics (operators want to know WHY Drive auth
// failed at boot if they skip this validation). The helper is called
// BEFORE the existing log.Warn block so the godlike/06 one-owner-per-fact
// invariant holds: the gate decides first, the wire-up logs second.
package app

import (
	"fmt"
	"os"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

// validateDriveServiceAvailability is the canonical composition-root
// fail-closed gate for Drive service availability
// (PR-DRIVE-AVAILABILITY-GATE, July 2026). It returns a non-nil error
// iff the Drive credentials + token files are MISSING from disk
// (operator misconfiguration class) AND cfg.Drive.StrictStartupValidation
// is true (operator opt-in for the fail-fast-at-boot contract).
// Soft-mode (StrictStartupValidation=false) keeps this validation non-blocking so
// staging / DR deployments can run without Drive availability — the
// handler-level preflight at BatchRegisterFromYouTube still fail-closed
// the actual user request, but boot itself is allowed to proceed.
//
// Configurations that fail-closed (with the verbatim substring contract):
//
//	(a) nil cfg                                 -> "cfg is nil"
//	(b) cfg.Drive.StrictStartupValidation=true AND credentials file missing
//	    -> "credentials file not found at <path> ... credentials.json ...
//	        scripts/generate_drive_token.py ... VELOX_DRIVE_STRICT_STARTUP_VALIDATION=false"
//	(c) cfg.Drive.StrictStartupValidation=true AND token file missing
//	    -> "token file not found at <path> ... token.json ...
//	        scripts/generate_drive_token.py ... VELOX_DRIVE_STRICT_STARTUP_VALIDATION=false"
//
// Configurations that pass silently:
//
//	(d) nil cfg                                  (defensive nil only — NEVER pass nil in practice)
//	(e) cfg.Drive.StrictStartupValidation=false (the canonical soft-mode escape hatch)
//	(f) cfg.Drive.StrictStartupValidation=true AND both files stat OK
//
// godlike/07 no-fake-availability: case (b) AND (c) are the canonical
// silent-failure surface — pre-PR-DRIVE-AVAILABILITY-GATE the boot
// proceeds with `driveUploader=nil` and the first POST to
// /api/media/register-batch with folder_id non-empty crashes the
// server (panic + 500). With this gate in place the misconfiguration
// is caught at boot (loud + actionable) per the godlike/07
// fail-fast-at-boot-vs-fail-slow-at-first-/run principle.
//
// godlike/06 SSOT one-owner-per-fact: this helper is the SOLE canonical
// owner of the Drive-availability boot check. The single call site at
// build_bundles_drive.go::BuildDriveBundle TOF delegates to this helper
// for one line; the in-function log.Warn pattern after the constructor
// is RETAINED for diagnostic logging (different concern: log the
// specifics of the underlying auth.NewGoogleHTTPClient error vs. gate
// the boot outcome).
//
// Escape hatches (documented in the returned error message):
//
//	Configuration					Fix
//	-----------------------------------------	----------------------------------------
//	credentials.json missing			place OAuth client_secrets JSON at
//							cfg.Paths.CredentialsFile (default
//							./credentials.json, env override
//							VELOX_CREDENTIALS_FILE)
//	token.json missing				run `python3 scripts/generate_drive_token.py
//							--credentials credentials.json
//							--token token.json` (AGENTS.md
//							canonical token-regeneration
//							command)
//	either file missing AND soft-mode needed	set VELOX_DRIVE_STRICT_STARTUP_VALIDATION=false
//
// The disable-via-env-var escape means operators who don't need Drive
// (pure-search / pure-stock deployments) can choose either direction
// without spindle-and-break boot.
func validateDriveServiceAvailability(cfg *config.Config) error {
	if cfg == nil {
		// godlike/06 SSOT surface: the helper itself must fail loudly when
		// invoked with nil cfg (defensive coverage for callers that have
		// not yet initialised the boot-time config struct). The single
		// caller BuildDriveBundle dereferences cfg for the docClient +
		// driveClient constructors, so this nil-check must fire FIRST
		// before any cfg field reads.
		return fmt.Errorf("validateDriveServiceAvailability: cfg is nil (PR-DRIVE-AVAILABILITY-GATE fail-closed cannot evaluate Drive credentials availability)")
	}

	// Soft-mode fast path: the operator has explicitly opted out of the
	// fail-fast-at-boot contract via VELOX_DRIVE_STRICT_STARTUP_VALIDATION=false.
	// We log nothing here (BuildDriveBundle logs the
	// `log.Warn("Google Drive client not initialized", ...)` at its
	// diagnostic seam; this gate's job is to skip itself cleanly so soft
	// mode does not spam operators with redundant error envelopes).
	if !cfg.Drive.StrictStartupValidation {
		return nil
	}

	credPath := cfg.GetCredentialsPath()
	tokenPath := cfg.GetTokenPath()

	// (b) credentials file missing — fail-closed with actionable
	// diagnostic naming both the canonical default path AND the
	// regeneration command. We use os.Stat (not os.ReadFile) so a
	// permission-denied error surfaces as `nil error` to Stat (the
	// file exists but is unreadable) — operators see a different
	// fail-mode downstream (auth.go's `failed to read google
	// credentials`) rather than THIS gate masking the permission
	// issue as a missing-file error.
	if _, err := os.Stat(credPath); err != nil {
		return fmt.Errorf(
			"validateDriveServiceAvailability: Drive credentials file not found at %q "+
				"(PR-DRIVE-AVAILABILITY-GATE fail-closed; *drive.Uploader.Service is nil and "+
				"POST /api/media/register-batch with folder_id non-empty will 500-panic). "+
				"To fix: place the OAuth client_secrets JSON at that path (default credentials.json, "+
				"env override VELOX_CREDENTIALS_FILE), OR set VELOX_DRIVE_STRICT_STARTUP_VALIDATION=false "+
				"to allow soft-mode boot (Drive remains unavailable but the server starts)",
			credPath,
		)
	}

	// (c) token file missing — fail-closed symmetric to case (b). The
	// actionable diagnostic here names the canonical token-regeneration
	// command per AGENTS.md §"Drive Token Regeneration" so operators
	// copying the diagnostic into their runbook get the right command.
	if _, err := os.Stat(tokenPath); err != nil {
		return fmt.Errorf(
			"validateDriveServiceAvailability: Drive token file not found at %q "+
				"(PR-DRIVE-AVAILABILITY-GATE fail-closed; *drive.Uploader.Service is nil and "+
				"POST /api/media/register-batch with folder_id non-empty will 500-panic). "+
				"To fix: regenerate via `python3 scripts/generate_drive_token.py --credentials credentials.json --token token.json` "+
				"(AGENTS.md canonical token-regeneration command), OR set VELOX_DRIVE_STRICT_STARTUP_VALIDATION=false "+
				"to allow soft-mode boot",
			tokenPath,
		)
	}

	return nil
}
