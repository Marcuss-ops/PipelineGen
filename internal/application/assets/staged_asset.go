// Package assets — staged_asset.go.
//
// Deprecated: StagedAsset has moved to internal/kernel/asset.StagedAsset.
// This file provides a backward-compatibility alias so existing callers
// reference `assets.StagedAsset` without churn during migration.
//
// godlike/06 SSOT: the canonical type lives in kernel/asset; this
// alias is a transitional forwarder only.
package assets

import "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"

// StagedAsset is a backward-compatibility alias.
// Deprecated: use asset.StagedAsset directly.
type StagedAsset = asset.StagedAsset
