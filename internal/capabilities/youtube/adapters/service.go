// Package adapters — service.go holds the shared Service struct consumed by
// the metadata helpers in this package.
//
// Phase 1b (June 2026): the original mega-package youtube.Service was moved to
// usecase/; a local *Service receiver remained here for the metadata helpers.
//
// MEDIA-SSOT (September 2026, BRIDGE REMOVED): the former
// `Service.dispatchOrIndex(ctx, clip, hash)` canonical-writer entry point,
// its `assetRepo detail.Repository` field and the `ServiceDeps.AssetRepo`
// dependency are DELETED. They had ZERO production callers (only a test
// exercised them) and the field was a `detail.Repository` — i.e. the SQLite
// type-switch bridge. Wiring that bridge into a media write is exactly the
// write split-brain class that let the YouTube enrichment persist
// media_assets on SQLite while the media SSOT was PostgreSQL, and it is
// invisible to SQL-level gates because the split happens behind a method
// call. Deleting the dead entry point makes that shape unrepresentable here.
//
// The live YouTube asset-writer entry point is
// internal/app/wiring.newYouTubeAssetWriter -> youTubeAssetWriter
// (canonical dispatcher: commit-and-index on the media SSOT).
package adapters

import (
	"go.uber.org/zap"

	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/dto"
	youtubeports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/ports"
)

// Service is the shared state container consumed by the adapters-level
// metadata helpers. Every field is nil-safe: methods guard against nil
// receivers before access.
//
// Fields are unexported because all accessors are methods in this package.
type Service struct {
	log    *zap.Logger
	cfg    youtubetypes.RuntimeConfig
	ollama youtubeports.OllamaClientPort
}

// ServiceDeps is the constructor envelope for Service.
// Log is mandatory (composition root supplies a real logger).
type ServiceDeps struct {
	Log *zap.Logger
}

// NewService constructs a Service from a ServiceDeps envelope.
func NewService(deps ServiceDeps) *Service {
	return &Service{log: deps.Log}
}
