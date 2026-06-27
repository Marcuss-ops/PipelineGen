// Package fixture — self-check fixture for Check 14 (TODO 16).
//
// This file (check_14_buildpayload_status_key.go) demonstrates the
// legacy "status" payload key in BuildPayload. The canonical payload
// key is "lifecycle_state" (QDRANT-001 §(b)); the "status" key is the
// QDRANT-RECOVERY-001 legacy that QDRANT-001 removed. A struct-field
// reference (`asset.Status` etc.) is what the lint catches; literal
// strings like `"status": "pending"` are not in scope.
package fixture

type assetShim struct {
	Status         string
	LifecycleState string
}

// Forbidden: payload key "status" sourced from a struct field. The
// canonical key is "lifecycle_state" sourced from asset.LifecycleState.
func badBuildPayload(asset *assetShim) map[string]interface{} {
	return map[string]interface{}{
		"status":     asset.Status,         // anti-pattern: legacy payload key
		"asset_id":   "asset-1",
		"media_type": "video",
	}
}
