package assetop

import (
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
)

// ExistingAssetEvidence is the source-agnostic evidence available before a
// potentially expensive download starts. Callers populate it from the
// canonical media_assets row for the same stable asset identity.
type ExistingAssetEvidence struct {
	DriveFileID string
	DriveLink   string
	FileHash    string
}

// ExistingAssetDecision is the canonical pre-download strategy verdict.
// Skip=true means the caller must not download, transcode or upload again.
type ExistingAssetDecision struct {
	Skip   bool
	Reason string
}

const (
	ExistingReasonNone          = ""
	ExistingReasonDrivePresent  = "drive_present"
	ExistingReasonVerifiedAsset = "verified_drive_and_hash"
)

// ResolveExistingAssetStrategy centralizes verify/skip/replace semantics for
// every media source. It deliberately uses only canonical persisted evidence:
//
//   - skip: skip when a Drive identity/link already exists.
//   - verify: skip only when Drive evidence AND a persisted content hash exist.
//   - replace: always continue with acquisition and processing.
//
// The stable source identity check belongs to the caller: this resolver must be
// invoked only with the existing row for the exact asset being considered.
func ResolveExistingAssetStrategy(strategy string, evidence ExistingAssetEvidence) ExistingAssetDecision {
	normalized := asset.NormalizeStrategy(strategy, false)
	hasDrive := strings.TrimSpace(evidence.DriveFileID) != "" || strings.TrimSpace(evidence.DriveLink) != ""
	hasHash := strings.TrimSpace(evidence.FileHash) != ""

	switch normalized {
	case asset.StrategyReplace:
		return ExistingAssetDecision{}
	case asset.StrategySkip:
		if hasDrive {
			return ExistingAssetDecision{Skip: true, Reason: ExistingReasonDrivePresent}
		}
	case asset.StrategyVerify:
		if hasDrive && hasHash {
			return ExistingAssetDecision{Skip: true, Reason: ExistingReasonVerifiedAsset}
		}
	}

	return ExistingAssetDecision{}
}
