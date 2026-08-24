// Package diagnostics — system_prober_drive.go: probeDriveFolder
// (Step 4 follow-up, July 2026).
//
// godlike/06 SSOT: probeDriveFolder is the SINGLE canonical owner of
// the drive_folder probe. The 3-layer nil-safe guard (nil closure /
// empty rootID / non-nil error) lives in one place — adding parallel
// drive_folder probes anywhere else (e.g. an adapter-specific
// variant) would be a godlike/06 violation.
//
// godlike/07 NO-FAKE-AVAILABILITY §22: the probe calls the canonical
// ProbeFolderAccess closure injected from the composition root — the
// probe does NOT in-line a fallback to "drive_folder" = rootFolderID
// (the legacy silent-success anti-pattern). When the closure is
// nil, the probe returns Error="drive_folder_probe_unwired" honestly
// rather than fake-OK.
package diagnostics

import (
	"context"
	"strings"
	"time"

	artlist "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers/artlist"
)

// probeDriveFolder returns the canonical drive_folder probe result.
// Calls the ProbeFolderAccess closure injected from the composition
// root against the configured Artlist Drive root (resolve via
// artlist.ResolveRootFolderID(cfg)). nil-safe in three layers:
//
//   - ProbeFolderAccess == nil: composition-time wiring gap, surface
//     Error="drive_folder_probe_unwired" honestly rather than fake-OK.
//   - ProbeFolderRootID == "": operator did not configure the root
//     folder, surface Error="drive_folder_root_not_configured" with
//     the operator-fix hint (cfg.Drive.ArtlistFolder()).
//   - ProbeFolderAccess returns non-nil error: surface the verbatim
//     error message from the underlying probe call.
//
// Forward-pointer (post-Commit 2): the canonical
// delivery.Publisher interface (in
// internal/application/assets/delivery/publisher.go) does not expose
// ProbeFolderAccess today — a follow-up commit will lift it onto
// the canonical publisher port surface so the prober can call it
// without the composition root needing to construct an ad-hoc
// closure. Until then the probe remains honest with
// Error="drive_folder_probe_unwired".
//
// DefaultProbeTimeout (canonical, system_prober.go) is the shared
// per-probe wall-clock budget referenced by both ProbeAll and this
// drive probe — same-package symbol resolution makes the cross-file
// reference transparent.
func (p *AdminSystemProber) probeDriveFolder(ctx context.Context) artlist.ProbeResult {
	start := time.Now()

	if p.ProbeFolderAccess == nil {
		return artlist.ProbeResult{
			OK:        false,
			Error:     "drive_folder_probe_unwired",
			Detail:    "composition root did not inject ProbeFolderAccess closure (canonical delivery.Publisher interface does not expose ProbeFolderAccess today; forward-pointer to a follow-up commit that lifts ProbeFolderAccess onto the canonical publisher port)",
			ElapsedMs: time.Since(start).Milliseconds(),
		}
	}

	rootID := strings.TrimSpace(p.ProbeFolderRootID)
	if rootID == "" {
		return artlist.ProbeResult{
			OK:        false,
			Error:     "drive_folder_root_not_configured",
			Detail:    "composition root passed empty ProbeFolderRootID (cfg.Drive.ArtlistFolder() returned empty; operator must configure cfg.Drive.ArtlistRootFolder for the diagnostics endpoint to probe folder reachability)",
			ElapsedMs: time.Since(start).Milliseconds(),
		}
	}

	timeout := p.ProbeTimeout
	if timeout <= 0 {
		timeout = DefaultProbeTimeout
	}
	probeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	if err := p.ProbeFolderAccess(probeCtx, rootID); err != nil {
		return artlist.ProbeResult{
			OK:        false,
			Error:     "drive_folder_unreachable",
			Detail:    "ProbeFolderAccess(" + rootID + ") returned: " + err.Error(),
			ElapsedMs: time.Since(start).Milliseconds(),
		}
	}

	return artlist.ProbeResult{
		OK:        true,
		Detail:    "drive folder probe ok: rootID=" + rootID + ", elapsed=" + time.Since(start).String(),
		ElapsedMs: time.Since(start).Milliseconds(),
	}
}
