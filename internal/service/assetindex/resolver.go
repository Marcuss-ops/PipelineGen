package assetindex

import (
	"context"
	"strings"

	"go.uber.org/zap"

	"velox/go-master/internal/repository/clips"
	"velox/go-master/internal/repository/images"
	"velox/go-master/internal/repository/voiceovers"
	"velox/go-master/pkg/models"
)

// Resolver provides a unified way to resolve assets across all databases.
// It queries asset_index first (fast), then falls back to specific DBs if needed.
type Resolver struct {
	svc           *Service
	clipsRepos    map[string]*clips.Repository // source -> repo (youtube, stock, artlist)
	imageRepo     *images.Repository
	voiceoverRepo *voiceovers.Repository
	log           *zap.Logger
}

// ResolverConfig holds the configuration for the AssetResolver
type ResolverConfig struct {
	ClipsRepos    map[string]*clips.Repository
	ImageRepo     *images.Repository
	VoiceoverRepo *voiceovers.Repository
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

// resolveFromDB queries the specific repository for an asset
func (r *Resolver) resolveFromDB(ctx context.Context, source, sourceID string) (*AssetRecord, error) {
	switch source {
	case "youtube", "youtube_clip", "clip":
		return r.resolveClipFromDB(ctx, "youtube", sourceID)
	case "stock":
		return r.resolveClipFromDB(ctx, "stock", sourceID)
	case "artlist":
		return r.resolveClipFromDB(ctx, "artlist", sourceID)
	case "image", "images":
		return r.resolveImageFromDB(ctx, sourceID)
	case "voiceover", "audio":
		return r.resolveVoiceoverFromDB(ctx, sourceID)
	default:
		r.log.Warn("unsupported source type", zap.String("source", source))
		return nil, nil
	}
}

// resolveClipFromDB retrieves a clip from the appropriate clips repository
func (r *Resolver) resolveClipFromDB(ctx context.Context, source, id string) (*AssetRecord, error) {
	repo, ok := r.clipsRepos[source]
	if !ok {
		r.log.Warn("no repository for source", zap.String("source", source))
		return nil, nil
	}

	clip, err := repo.GetClip(ctx, id)
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

	// Note: images.Repository needs a Get method - check if available
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

	// Convert voiceovers.Record to AssetRecord
	return voiceoverToAssetRecord(rec), nil
}

// clipToAssetRecord converts a models.Clip to an AssetRecord
func clipToAssetRecord(source string, clip *models.MediaAsset) *AssetRecord {
	rec := &AssetRecord{
		AssetID:   source + "_" + clip.ID,
		AssetType: getAssetTypeFromSource(source),
		Source:    source,
		SourceID:  clip.ID,
		GroupName: clip.Group,
		LocalPath: clip.LocalPath,
		DriveLink: clip.DriveLink,
		FileHash:  clip.FileHash,
		Status:    clip.Status,
	}

	if len(clip.Metadata) > 0 {
		rec.Metadata = clip.MetadataJSON()
	}

	return rec
}

// voiceoverToAssetRecord converts a voiceovers.Record to an AssetRecord
func voiceoverToAssetRecord(rec *voiceovers.Record) *AssetRecord {
	return &AssetRecord{
		AssetID:   "voiceover_" + rec.ID,
		AssetType: "voiceover",
		Source:    "voiceover",
		SourceID:  rec.ID,
		LocalPath: rec.LocalPath,
		DriveLink: rec.DriveLink,
		FileHash:  rec.FileHash,
		Status:    rec.Status,
		Metadata:  rec.Metadata,
	}
}

// getAssetTypeFromSource returns the asset type based on the source
func getAssetTypeFromSource(source string) string {
	switch source {
	case "youtube", "youtube_clip", "clip":
		return "clip"
	case "stock":
		return "stock"
	case "artlist":
		return "artlist"
	default:
		return source
	}
}
