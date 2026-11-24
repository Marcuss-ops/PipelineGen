// Package reconciliation — reconciliation_verify.go contains the single-link
// verification wrapper of the AssetLocationReconciliationProcessor
// (verifyAndReconcile) and its result type. See
// processor_asset_location_reconciliation.go for the processor header and
// reconciliation_process.go for the per-scene verification loop.
package reconciliation

import (
	"context"
	"fmt"
	"strings"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// reconcileResult captures the outcome of a single link verification.
type reconcileResult struct {
	changed         bool
	ok              int
	updated         int
	missing         int
	trashed         int
	inaccessible    int
	malformed       int
	warning         string
	durableMutation bool
	assetChange     *scriptpkg.AssetLocationChange
	driveFileID     string
}

// locationOutcome is one row of the canonical verifier state machine. The map
// below is the only place that decides whether a state keeps, rewrites or
// clears a link, which counter it increments and what the operator-facing
// warning says. Adding a state means adding a row, not a second switch.
type locationOutcome struct {
	apply   func(*reconcileResult, *string, *scriptpkg.VerifiedLocation)
	warning string
}

var locationOutcomes = map[scriptpkg.LocationState]locationOutcome{
	scriptpkg.LocationStateVerified: {
		apply: func(result *reconcileResult, linkPtr *string, verified *scriptpkg.VerifiedLocation) {
			if strings.TrimSpace(*linkPtr) == strings.TrimSpace(verified.DriveLink) {
				result.ok = 1
				return
			}
			*linkPtr = verified.DriveLink
			result.changed = true
			result.durableMutation = true
			result.updated = 1
		},
	},
	scriptpkg.LocationStateUpdated: {
		apply: func(result *reconcileResult, linkPtr *string, verified *scriptpkg.VerifiedLocation) {
			*linkPtr = verified.DriveLink
			result.changed = true
			result.durableMutation = true
			result.updated = 1
		},
	},
	scriptpkg.LocationStateMissing: {
		apply: func(result *reconcileResult, linkPtr *string, _ *scriptpkg.VerifiedLocation) {
			*linkPtr = ""
			result.changed = true
			result.durableMutation = true
			result.missing = 1
		},
		warning: "asset_location_reconciliation: %s link in %s is MISSING (Drive file not found): asset=%s",
	},
	scriptpkg.LocationStateTrashed: {
		apply: func(result *reconcileResult, linkPtr *string, _ *scriptpkg.VerifiedLocation) {
			*linkPtr = ""
			result.changed = true
			result.durableMutation = true
			result.trashed = 1
		},
		warning: "asset_location_reconciliation: %s link in %s is TRASHED (file in Drive trash): asset=%s",
	},
	scriptpkg.LocationStateInaccessible: {
		apply: func(result *reconcileResult, linkPtr *string, _ *scriptpkg.VerifiedLocation) {
			*linkPtr = ""
			result.changed = true
			result.durableMutation = true
			result.inaccessible = 1
		},
		warning: "asset_location_reconciliation: %s link in %s is INACCESSIBLE (permission denied): asset=%s",
	},
	scriptpkg.LocationStateMalformed: {
		apply: func(result *reconcileResult, linkPtr *string, _ *scriptpkg.VerifiedLocation) {
			*linkPtr = ""
			result.changed = true
			result.durableMutation = true
			result.malformed = 1
		},
		warning: "asset_location_reconciliation: %s link in %s is MALFORMED (cannot extract file ID): asset=%s",
	},
	scriptpkg.LocationStateOrphanDriveFile: {
		apply: func(result *reconcileResult, linkPtr *string, _ *scriptpkg.VerifiedLocation) {
			*linkPtr = ""
			result.changed = true
			result.durableMutation = true
			result.missing = 1
		},
		warning: "asset_location_reconciliation: %s link in %s is an ORPHAN (file on Drive but no SQLite record): asset=%s",
	},
	scriptpkg.LocationStateBrokenAssetLocation: {
		apply: func(result *reconcileResult, linkPtr *string, _ *scriptpkg.VerifiedLocation) {
			*linkPtr = ""
			result.changed = true
			result.durableMutation = true
			result.missing = 1
		},
		warning: "asset_location_reconciliation: %s link in %s has a BROKEN location (SQLite references missing Drive file): asset=%s",
	},
	scriptpkg.LocationStateDuplicate: {
		apply: func(result *reconcileResult, linkPtr *string, _ *scriptpkg.VerifiedLocation) {
			*linkPtr = ""
			result.changed = true
			result.durableMutation = true
			result.malformed = 1
		},
		warning: "asset_location_reconciliation: %s link in %s is a DUPLICATE (multiple assets share same drive_file_id): asset=%s",
	},
}

// verifyAndReconcile calls the verifier for a single link and updates the link
// pointer in place. Returns a reconcileResult with counters and an optional
// warning string.
//
// Transport errors (network timeout, Drive API 5xx) are surfaced as warnings
// and clear only the downstream link. Confirmed invalid states produce durable
// mutations; the injected committer persists those changes together with the
// pgvector outbox event.
func (p *AssetLocationReconciliationProcessor) verifyAndReconcile(
	ctx context.Context,
	assetID string,
	fileID string,
	link string,
	linkPtr *string,
	label string,
	sceneID string,
) (result reconcileResult, err error) {
	defer func() {
		if !result.durableMutation || strings.TrimSpace(assetID) == "" || strings.HasPrefix(assetID, "voiceover:") || label == "subtitle" || linkPtr == nil {
			return
		}
		// Preserve the verified Drive identity even when the published link is
		// cleared. The durable committer intentionally clears only the public
		// URL so operators can diagnose or republish the asset.
		result.assetChange = &scriptpkg.AssetLocationChange{
			AssetID:     strings.TrimSpace(assetID),
			DriveFileID: strings.TrimSpace(result.driveFileID),
			DriveLink:   strings.TrimSpace(*linkPtr),
		}
	}()

	verified, err := p.verifier.Verify(ctx, assetID, fileID, link)
	if err != nil {
		// BestEffort keeps generation running, but fails closed for
		// publication: an unverified link must never reach the document,
		// manifest, or persisted SpecScene.
		*linkPtr = ""
		transportResult := reconcileResult{
			changed: true,
			warning: fmt.Sprintf("asset_location_reconciliation: transport error verifying %s link in %s (link cleared): %v", label, sceneID, err),
		}
		if p.mutator != nil {
			return transportResult, fmt.Errorf("%w: asset_location_reconciliation transport verification failed for %s in %s: %w", scriptpkg.ErrPostprocessFailed, label, sceneID, err)
		}
		return transportResult, nil
	}
	if verified == nil {
		// A verifier that cannot produce a result has not established that the
		// link is usable. Fail closed rather than publishing an unverified
		// location.
		*linkPtr = ""
		unknownResult := reconcileResult{
			changed: true,
			warning: fmt.Sprintf("asset_location_reconciliation: %s link in %s has UNKNOWN verification state (verifier returned no result; link cleared): asset=%s", label, sceneID, assetID),
		}
		if p.mutator != nil {
			return unknownResult, fmt.Errorf("%w: asset_location_reconciliation verifier returned no result for %s in %s", scriptpkg.ErrPostprocessFailed, label, sceneID)
		}
		return unknownResult, nil
	}

	result.driveFileID = verified.DriveFileID
	if verified.State == scriptpkg.LocationStateVerified || verified.State == scriptpkg.LocationStateUpdated {
		if strings.TrimSpace(verified.DriveFileID) == "" || strings.TrimSpace(verified.DriveLink) == "" {
			*linkPtr = ""
			return reconcileResult{
				changed:         true,
				durableMutation: true,
				driveFileID:     verified.DriveFileID,
				warning:         fmt.Sprintf("asset_location_reconciliation: %s link in %s has incomplete canonical Drive metadata (link cleared): asset=%s", label, sceneID, assetID),
			}, nil
		}
	}

	outcome, known := locationOutcomes[verified.State]
	if !known {
		// New or corrupt verifier states must never skip validation. Preserve
		// generation as best-effort, but clear the link and expose the degraded
		// outcome to status classification.
		*linkPtr = ""
		return reconcileResult{
			changed:         true,
			durableMutation: true,
			driveFileID:     verified.DriveFileID,
			warning:         fmt.Sprintf("asset_location_reconciliation: %s link in %s has UNKNOWN verification state %q (link cleared): asset=%s", label, sceneID, verified.State, assetID),
		}, nil
	}

	outcome.apply(&result, linkPtr, verified)
	if outcome.warning != "" {
		result.warning = fmt.Sprintf(outcome.warning, label, sceneID, assetID)
	}
	return result, nil
}
