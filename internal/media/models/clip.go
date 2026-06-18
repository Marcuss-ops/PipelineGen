package models

import (
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/core/domain/asset"
)

// MediaAsset is a type alias for the canonical asset.MediaAsset.
// This alias exists purely for backward compatibility during the migration.
// New code MUST use asset.MediaAsset directly.
// This alias will be removed once all consumers are migrated.
type MediaAsset = asset.MediaAsset

// IndexingCheckpoint represents a checkpoint for the indexing process
type IndexingCheckpoint struct {
	ID            string    `json:"id"`
	Path          string    `json:"path"`
	LastIndexedAt time.Time `json:"last_indexed_at"`
	Metadata      string    `json:"metadata"`
}
