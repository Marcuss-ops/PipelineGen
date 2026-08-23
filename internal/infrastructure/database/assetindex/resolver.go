package assetindex

import (
	"context"
	"strings"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets/imagesrepo"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// Resolver provides a unified way to resolve assets across all databases.
// It queries asset_index first (fast), then falls back to specific DBs if needed.
type Resolver struct {
	svc           *Service
	clipsRepos    map[string]*assets.ClipsRepository // source -> repo (youtube, stock, artlist)
	imageRepo     *imagesrepo.ImagesRepository
	voiceoverRepo *assets.VoiceoversRepository
	log           *zap.Logger
}

// ResolverConfig holds the configuration for the AssetResolver
type ResolverConfig struct {
	ClipsRepos    map[string]*assets.ClipsRepository
	ImageRepo     *imagesrepo.ImagesRepository
	VoiceoverRepo *assets.VoiceoversRepository
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

// ResolveBySource looks up an asset by source and sourceID.
// It queries asset_index first, then falls back to the specific repository if not found.
func (r *Resolver) ResolveBySource(ctx context.Context, source, sourceID string) (*AssetRecord, error) {
	// Try asset_index first
	rec, err := r.svc.FindBySource(ctx, source, sourceID)
	if err != nil {
		r.log.Warn("failed to query asset_index", zap.Error(err), zap.String("source", source))
	}
	if rec != nil {
		return rec, nil
	}

	// Fall back to specific repository
	return r.resolveFromDB(ctx, source, sourceID)
}

// ResolveByContentHash looks up an asset by content hash.
// This is useful for deduplication across sources.
func (r *Resolver) ResolveByContentHash(ctx context.Context, hash string) (*AssetRecord, error) {
	return r.svc.FindByContentHash(ctx, hash)
}

// SearchByType searches assets in asset_index by type.
// Returns assets from the index only (fast path).
func (r *Resolver) SearchByType(ctx context.Context, assetType string) ([]*AssetRecord, error) {
	// Query asset_index for assets of this type
	// Since we don't have a direct method, we'll use FindReadyByGroup with empty group
	// and filter by type in the result
	records, err := r.svc.FindReadyByGroup(ctx, "", "")
	if err != nil {
		return nil, err
	}

	// Filter by type if specified
	if assetType != "" {
		var filtered []*AssetRecord
		for _, rec := range records {
			if strings.EqualFold(rec.AssetType, assetType) {
				filtered = append(filtered, rec)
			}
		}
		return filtered, nil
	}

	return records, nil
}

// resolveFromDB queries the specific repository for an asset.
// Collapse (June 2026): switch cleaned to use canonical source names
// without importing application-layer packages (infrastructure boundary).
func (r *Resolver) resolveFromDB(ctx context.Context, source, sourceID string) (*AssetRecord, error) {
	canonical := canonicalSource(source)
	switch canonical {
	case "artlist", "clips", "stock", "sound_effect":
		return r.resolveClipFromDB(ctx, canonical, sourceID)
	case "voiceover":
		return r.resolveVoiceoverFromDB(ctx, sourceID)
	case "images":
		return r.resolveImageFromDB(ctx, sourceID)
	default:
		r.log.Warn("unsupported source type", zap.String("source", source))
		return nil, nil
	}
}

// canonicalSource is a local alias normalizer that keeps the
// infrastructure layer free of application-layer imports.
// Mirrors artifacts.CanonicalSource for the subset of sources
// this package knows about.
func canonicalSource(source string) string {
	return asset.DefaultSourceCatalog().Canonical(source)
}

// resolveClipFromDB retrieves a clip from the appropriate clips repository
func (r *Resolver) resolveClipFromDB(ctx context.Context, source, id string) (*AssetRecord, error) {
	repo, ok := r.clipsRepos[source]
	if !ok {
		r.log.Warn("no repository for source", zap.String("source", source))
		return nil, nil
	}

	clip, err := repo.Get(ctx, id)
	if err != nil {
		r.log.Warn("failed to get clip from repo", zap.Error(err), zap.String("source", source))
		return nil, nil
	}
	if clip == nil {
		return nil, nil
	}

	// Convert models.Clip to AssetRecord
	return clipToAssetRecord(source, clip), nil
}

// resolveImageFromDB retrieves an image from the images repository
func (r *Resolver) resolveImageFromDB(ctx context.Context, id string) (*AssetRecord, error) {
	if r.imageRepo == nil {
		return nil, nil
	}

	// Note: imagesrepo.ImagesRepository needs a Get method - check if available
	// For now, return nil as placeholder
	r.log.Warn("image resolution from DB not fully implemented")
	return nil, nil
}

// resolveVoiceoverFromDB retrieves a voiceover from the voiceovers repository
func (r *Resolver) resolveVoiceoverFromDB(ctx context.Context, id string) (*AssetRecord, error) {
	if r.voiceoverRepo == nil {
		return nil, nil
	}

	rec, err := r.voiceoverRepo.GetByID(ctx, id)
	if err != nil {
		r.log.Warn("failed to get voiceover from repo", zap.Error(err))
		return nil, nil
	}
	if rec == nil {
		return nil, nil
	}

	// Convert assets.Record to AssetRecord
	return voiceoverToAssetRecord(rec), nil
}

// clipToAssetRecord converts a models.Clip to an AssetRecord
func clipToAssetRecord(source string, clip *asset.Asset) *AssetRecord {
	rec := &AssetRecord{
		AssetID:       source + "_" + clip.ID,
		AssetType:     getAssetTypeFromSource(source),
		Source:        source,
		SourceID:      clip.ID,
		GroupName:     clip.Group,
		LocalPath:     clip.LocalPath(),
		DriveLink:     clip.DriveLink(),
		LegacyFileMD5: clip.LegacyFileMD5(),
		Status:        "", // status migrated to asset_processing
	}

	if len(clip.Metadata) > 0 {
		rec.Metadata = clip.MetadataJSON()
	}

	return rec
}

// voiceoverToAssetRecord converts a assets.Record to an AssetRecord
func voiceoverToAssetRecord(rec *assets.Record) *AssetRecord {
	return &AssetRecord{
		AssetID:       "voiceover_" + rec.ID,
		AssetType:     "voiceover",
		Source:        "voiceover",
		SourceID:      rec.ID,
		LocalPath:     rec.LocalPath,
		DriveLink:     rec.DriveLink,
		LegacyFileMD5: rec.LegacyFileMD5,
		Status:        rec.Status,
		Metadata:      rec.Metadata,
	}
}

// getAssetTypeFromSource returns the asset type based on the source.
// Collapse (June 2026): switch cleaned to use canonical source names;
// returns the canonical name itself (not MediaType) to preserve
// existing callers that expect source-specific identifiers.
func getAssetTypeFromSource(source string) string {
	switch canonical := canonicalSource(source); canonical {
	case "artlist", "clips", "stock", "sound_effect":
		return canonical
	case "voiceover":
		return "voiceover"
	case "images":
		return "images"
	}
	return source
}
