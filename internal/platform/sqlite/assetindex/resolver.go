package assetindex

import (
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/assets/imagesregistry"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/assets/imagesrepo"
)

// Resolver provides a unified way to resolve assets across all databases.
// It queries asset_index first (fast), then falls back to specific DBs if needed.
type Resolver struct {
	svc           *Service
	clipsRepos    map[string]*imagesregistry.ClipsRepository // source -> repo (youtube, stock, artlist)
	imageRepo     *imagesrepo.ImagesRepository
	voiceoverRepo *imagesregistry.VoiceoversRepository
	log           *zap.Logger
}

// ResolverConfig holds the configuration for the AssetResolver
type ResolverConfig struct {
	ClipsRepos    map[string]*imagesregistry.ClipsRepository
	ImageRepo     *imagesrepo.ImagesRepository
	VoiceoverRepo *imagesregistry.VoiceoversRepository
}

// NewResolver creates a new AssetResolver
func NewResolver(svc *Service, cfg *ResolverConfig, log *zap.Logger) *Resolver {
	return &Resolver{
		svc:           svc,
		clipsRepos:    cfg.ClipsRepos,
		imageRepo:     cfg.ImageRepo,
		voiceoverRepo: cfg.VoiceoverRepo,
		log:           log.Named("asset_resolver"),
	}
}

// canonicalSource is a local alias normalizer that keeps the
// infrastructure layer free of application-layer imports.
// Mirrors artifacts.CanonicalSource for the subset of sources
// this package knows about.

// clipToAssetRecord converts a models.Clip to an AssetRecord

// voiceoverToAssetRecord converts a imagesregistry.Record to an AssetRecord

// getAssetTypeFromSource returns the asset type based on the source.
// Collapse (June 2026): switch cleaned to use canonical source names;
// returns the canonical name itself (not MediaType) to preserve
// existing callers that expect source-specific identifiers.
