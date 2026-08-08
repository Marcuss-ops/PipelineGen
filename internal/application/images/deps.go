// Package images (application/images) — deps.go holds the canonical
// dependency bag types consumed by NewService. Per PR-IMG-SPLIT-4
// (July 2026), dep types live in their own file, separate from the
// Service struct and constructor.
//
// godlike/06 SSOT: each dep bag is the canonical SOLE owner of its
// typed dependency surface. The composition root
// (internal/app/build_bundles_core.go) wires the concrete adapters
// into these bags.
package images

import (
	assetapp "github.com/Marcuss-ops/PipelineGen/internal/application/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/generation"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/ingest"
	persistence "github.com/Marcuss-ops/PipelineGen/internal/application/assets/persistence"
	"github.com/Marcuss-ops/PipelineGen/internal/application/images/destinations"
	"github.com/Marcuss-ops/PipelineGen/internal/application/images/generated"
	"github.com/Marcuss-ops/PipelineGen/internal/application/images/retrieved"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/ollama"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets/imagesrepo"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"go.uber.org/zap"
)

// ImagesDeps is the top-level dependency bag for the images subsystem.
// Split into four sub-bags by architectural concern: Core, Storage,
// GenAI, and External. Plus two typed-registry references (Generated
// and Retrieved) wired at composition time.
type ImagesDeps struct {
	Core      ImagesCoreDeps
	Storage   ImagesStorageDeps
	GenAI     ImagesGenAIDeps
	External  ImagesExternalDeps
	Retrieval *retrieved.RetrievalProviderRegistry
	Generated *generated.GenerationProviderRegistry
}

// ImagesCoreDeps holds platform-level dependencies (config + logger).
type ImagesCoreDeps struct {
	Cfg *config.Config
	Log *zap.Logger
}

// ImagesStorageDeps holds repository, Drive, and publisher dependencies.
// The images package writes assets atomically via Publisher and
// AssetCommitter; it no longer holds any drive.Store / mediaStore
// surface.
type ImagesStorageDeps struct {
	ImageRepo    *imagesrepo.ImagesRepository
	DriveReader  drive.Reader
	Publisher    delivery.Publisher
	DestResolver destinations.DestinationResolver
}

// ImagesGenAIDeps holds AI-generation dependencies (LLM, metadata, styles, image gen).
type ImagesGenAIDeps struct {
	LLMGen        *ollama.Generator
	MetaWriter    SemanticPort
	StyleRegistry *generation.StyleRegistry
	ImageGen      ImageGenerator
}

// ImagesExternalDeps holds external-service dependencies (ingest, committer, Velox, GA).
//
// Committer is the canonical SINGLE-transaction asset commit surface
// (persistence.AssetCommitter) used by ImageStorageService.ingestDirect
// to atomically write media_assets + asset_locations + typed metadata +
// the asset.index.requested outbox event.
//
// SourceStager is the canonical port for staging remote URLs into
// deterministic local files. downloadAndIngest routes web-image
// downloads through it.
type ImagesExternalDeps struct {
	IngestSvc    *ingest.Service
	Committer    persistence.AssetCommitter
	SourceStager assetapp.SourceStager
	VeloxBaseURL string
	GACfg        GoogleAccountingConfig
	RemoteFetch  RemoteFetchPort
}
