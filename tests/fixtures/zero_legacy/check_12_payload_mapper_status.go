//go:build ignore

// Package fixture — self-check fixture for Check 12 (TODO 16).
//
// This file (check_12_payload_mapper_status.go) demonstrates the
// legacy "lifecycle_state": <asset>.Status fallback in BuildPayload.
// The canonical key (per QDRANT-001 §(b)) is "lifecycle_state" sourced
// from asset.LifecycleState; falling back to asset.Status (the legacy
// column from migration 059) reads stale data on rows where both
// exist and have diverged.
package fixture

import (
	asset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

type assetShim struct {
	Status         string
	LifecycleState string
}

// Forbidden: payload key "lifecycle_state" sourced from the legacy
// asset.Status field. The canonical source is asset.LifecycleState.
//
// The `asset.Status` (no curly braces) shape is what the lint regex
// matches: `	"lifecycle_state":\s*\w+\.Status`. The previous fixture
// used `assetShim{}.Status` which broke the match because the regex
// does not allow `{` between the word and the `.Status` suffix.
func badPayload(asset *assetShim) map[string]interface{} {
	return map[string]interface{}{
		"lifecycle_state": asset.Status, // anti-pattern: legacy fallback
		"asset_id":        "asset-1",
	}
}
