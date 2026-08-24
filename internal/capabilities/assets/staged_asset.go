// Package assets — staged_asset.go. StagedAsset moved to
// capabilities/assets/ports/ (canonical owner). This file re-exports
// for backward compatibility.
package assets

import "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/ports"

// StagedAsset is a backward-compatibility alias.
// Deprecated: use ports.StagedAsset or kernel/asset.StagedAsset directly.
type StagedAsset = ports.StagedAsset
