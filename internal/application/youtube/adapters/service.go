// Package adapters — service.go holds the shared Service struct consumed by
// the extraction, manifest, segment, and intelligence files in this package.
//
// Phase 1b (June 2026): the original mega-package youtube.Service was moved to
// usecase/, but 6 files in adapters/ still reference a local *Service receiver
// for methods like resolveDriveDestination, saveManifest, processSegment, etc.
// This file defines the minimal struct those methods need so they compile
// without creating an adapters → usecase cycle (adapters already imports
// usecase for BuildClipMetadataInput and ExtractionCallbacks — no new cycle).
//
// Phase 1c TODO: fold these orchestration methods into ExtractionService in
// usecase/ so this Service struct can be deleted. The adapters package should
// contain only infrastructure adapters — not orchestration logic.
package adapters

import (
	"go.uber.org/zap"

	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/dto"
	youtubeports "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/ports"
	"github.com/Marcuss-ops/PipelineGen/internal/application/youtube/usecase"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
)

// Service is the shared state container consumed by the adapters-level
// extraction, manifest, segment, and intelligence methods. Every field
// is nil-safe: methods guard against nil receivers before access.
//
// Composition root (internal/app/composition.go) wires this struct once
// when constructing the adapters.Service used by the extraction pipeline.
// Fields are unexported because all accessors are methods in this package.
type Service struct {
	log               *zap.Logger
	cfg               youtubetypes.RuntimeConfig
	clips             youtubeports.ClipStorePort
	monitors          youtubeports.MonitorsStorePort
	folderMemory      youtubeports.FolderMemoryPort
	callbacks         usecase.ExtractionCallbacks
	assetDestResolver asset.Resolver
	cache             youtubeports.CachePort
	segmentsSvc       *usecase.SegmentsService
	videoPipeline     youtubeports.VideoPipelinePort
	ollama            youtubeports.OllamaClientPort
}
