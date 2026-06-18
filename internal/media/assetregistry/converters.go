package assetregistry

import (
	"path/filepath"

	"github.com/Marcuss-ops/PipelineGen/internal/media/models"
	"github.com/Marcuss-ops/PipelineGen/internal/core/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/repository/voiceovers"
)

// VoiceoverRecordToClip converts a voiceover.Record to models.Clip for unified handling.
// This is the canonical converter — do NOT create copies in handlers or services.
func VoiceoverRecordToClip(rec *voiceovers.Record) *asset.MediaAsset {
	if rec == nil {
		return nil
	}
	name := rec.Filename
	if name == "" {
		name = rec.TextPreview
		if len(name) > 50 {
			name = name[:50]
		}
	}
	clip := &asset.MediaAsset{
		ID:           rec.ID,
		Name:         name,
		Filename:     rec.Filename,
		FolderID:     rec.FolderID,
		FolderPath:   rec.FolderPath,
		DriveLink:    rec.DriveLink,
		DriveFileID:  rec.DriveFileID,
		DownloadLink: rec.DownloadLink,
		FileHash:     rec.FileHash,
		LocalPath:    rec.LocalPath,
		Source:       "voiceover",
		MediaType:    "audio",
		SearchTerms:  []string{rec.TextPreview},
		CreatedAt:    rec.CreatedAt,
		UpdatedAt:    rec.UpdatedAt,
	}
	clip.SetMetadataJSON(rec.Metadata)
	return clip
}

// ToCanonical converts a legacy models.MediaAsset to the canonical
// asset.MediaAsset. This is the central converter used during the
// Strangler Fig migration; once all consumers use asset.MediaAsset,
// this function and models.MediaAsset will be deleted.
//
// Field mapping notes:
//   - ExternalURL → SourceURL (+ kept as ExternalURL alias)
//   - Duration (int) → DurationMs (int64)
//   - ThumbURL → ThumbnailURL
//   - LifecycleState string → LifecycleState (typed)
//   - Status / Error → DROP (migrated to asset_processing)
//   - Width / Height / RelativePath → stored in Metadata for round-trip fidelity
func ToCanonical(legacy *models.MediaAsset) *asset.MediaAsset {
	if legacy == nil {
		return nil
	}

	// Clone Metadata so mutations don't leak back.
	meta := cloneMetadata(legacy.Metadata)

	// Preserve fields not in canonical struct via Metadata.
	if legacy.Width > 0 {
		meta["_width"] = legacy.Width
	}
	if legacy.Height > 0 {
		meta["_height"] = legacy.Height
	}
	if legacy.RelativePath != "" {
		meta["_relative_path"] = legacy.RelativePath
	}
	// Keep TagsNorm in Metadata for search compatibility.
	if legacy.TagsNorm != "" {
		meta["_tags_norm"] = legacy.TagsNorm
	}

	return &asset.MediaAsset{
		// ── Identity ──
		ID:       legacy.ID,
		Source:   legacy.Source,
		Name:     legacy.Name,
		Filename: legacy.Filename,

		// ── Classification ──
		MediaType: legacy.MediaType,
		Category:  legacy.Category,
		Group:     legacy.Group,

		// ── URLs ──
		SourceURL:    legacy.ExternalURL,
		ClipPageURL:  legacy.ClipPageURL,
		ThumbnailURL: legacy.ThumbURL,
		ExternalURL:  legacy.ExternalURL,

		// ── Content ──
		DurationMs:  int64(legacy.Duration),
		Tags:        safeStringSlice(legacy.Tags),
		SearchTerms: safeStringSlice(legacy.SearchTerms),
		SearchText:  legacy.SearchText,

		// ── Lifecycle ──
		LifecycleState: asset.LifecycleState(legacy.LifecycleState),
		DeletedAt:      legacy.DeletedAt,
		CreatedAt:      legacy.CreatedAt,
		UpdatedAt:      legacy.UpdatedAt,

		// ── Metadata ──
		Metadata: meta,

		// ── Legacy embedding fields ──
		EmbeddingJSON:       legacy.EmbeddingJSON,
		VisualEmbedding:     legacy.VisualEmbedding,
		TranscriptEmbedding: legacy.TranscriptEmbedding,
		VisualEmbeddingJSON: legacy.VisualEmbeddingJSON,

		// ── Legacy folder fields ──
		FolderID:       legacy.FolderID,
		ParentFolderID: legacy.ParentFolderID,
		FolderPath:     legacy.FolderPath,
		Depth:          legacy.Depth,
		IsFolder:       legacy.IsFolder,

		// ── Legacy usage/quality fields ──
		SceneType:    legacy.SceneType,
		QualityScore: legacy.QualityScore,
		ReuseCount:   legacy.ReuseCount,
		LastUsedAt:   legacy.LastUsedAt,
		UsableFor:    safeStringSlice(legacy.UsableFor),
		AvoidFor:     safeStringSlice(legacy.AvoidFor),
		PHash:        legacy.PHash,
		ChildCount:   legacy.ChildCount,

		// ── Deprecated location fields ──
		DriveFileID:  legacy.DriveFileID,
		DriveLink:    legacy.DriveLink,
		DownloadLink: legacy.DownloadLink,
		LocalPath:    legacy.LocalPath,
		FileHash:     legacy.FileHash,
	}
}

// ToLegacy converts a canonical asset.MediaAsset back to the legacy
// models.MediaAsset. Used during migration when a converted caller
// must pass data to clips.Repository (which still expects the legacy
// type). This function is temporary and will be deleted along with
// models.MediaAsset.
//
// Round-trip invariant: ToLegacy(ToCanonical(m)) ≈ m for all
// non-Status/non-Error fields. Status and Error are intentionally
// zeroed because their canonical home is asset_processing.
func ToLegacy(canonical *asset.MediaAsset) *models.MediaAsset {
	if canonical == nil {
		return nil
	}

	meta := cloneMetadata(canonical.Metadata)

	// Recover fields stored in Metadata.
	width, height := 0, 0
	if v, ok := meta["_width"].(float64); ok {
		width = int(v)
		delete(meta, "_width")
	} else if v, ok := meta["_width"].(int); ok {
		width = v
		delete(meta, "_width")
	}
	if v, ok := meta["_height"].(float64); ok {
		height = int(v)
		delete(meta, "_height")
	} else if v, ok := meta["_height"].(int); ok {
		height = v
		delete(meta, "_height")
	}
	relPath, _ := meta["_relative_path"].(string)
	delete(meta, "_relative_path")
	tagsNorm, _ := meta["_tags_norm"].(string)
	delete(meta, "_tags_norm")

	// Resolve ExternalURL: prefer SourceURL, fall back to ExternalURL alias.
	extURL := canonical.SourceURL
	if extURL == "" {
		extURL = canonical.ExternalURL
	}

	return &models.MediaAsset{
		ID:             canonical.ID,
		Name:           canonical.Name,
		Filename:       canonical.Filename,
		FolderID:       canonical.FolderID,
		ParentFolderID: canonical.ParentFolderID,
		FolderPath:     canonical.FolderPath,
		Depth:          canonical.Depth,
		IsFolder:       canonical.IsFolder,
		Group:          canonical.Group,
		MediaType:      canonical.MediaType,
		DriveLink:      canonical.DriveLink,
		DownloadLink:   canonical.DownloadLink,
		DriveFileID:    canonical.DriveFileID,
		Tags:           safeStringSlice(canonical.Tags),
		Source:         canonical.Source,
		Category:       canonical.Category,
		ExternalURL:    extURL,
		Duration:       int(canonical.DurationMs),
		Metadata:       meta,
		FileHash:       canonical.FileHash,
		LocalPath:      canonical.LocalPath,
		ThumbURL:       canonical.ThumbnailURL,
		Status:         "", // migrated to asset_processing
		Error:          "", // migrated to asset_processing
		SearchTerms:    safeStringSlice(canonical.SearchTerms),
		CreatedAt:      canonical.CreatedAt,
		UpdatedAt:      canonical.UpdatedAt,
		DeletedAt:      canonical.DeletedAt,
		SearchText:     canonical.SearchText,
		Width:          width,
		Height:         height,
		RelativePath:   relPath,
		TagsNorm:       tagsNorm,
		SceneType:      canonical.SceneType,
		QualityScore:   canonical.QualityScore,
		ReuseCount:     canonical.ReuseCount,
		LastUsedAt:     canonical.LastUsedAt,
		UsableFor:      safeStringSlice(canonical.UsableFor),
		AvoidFor:       safeStringSlice(canonical.AvoidFor),
		ChildCount:     canonical.ChildCount,
		PHash:          canonical.PHash,
		VisualEmbeddingJSON: canonical.VisualEmbeddingJSON,
		EmbeddingJSON:       canonical.EmbeddingJSON,
		VisualEmbedding:     canonical.VisualEmbedding,
		TranscriptEmbedding: canonical.TranscriptEmbedding,
		ClipPageURL:         canonical.ClipPageURL,
		LifecycleState:      string(canonical.LifecycleState),
	}
}

// cloneMetadata returns a shallow copy of src, or an empty map if src is nil.
func cloneMetadata(src map[string]any) map[string]any {
	if src == nil {
		return make(map[string]any)
	}
	dst := make(map[string]any, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

// safeStringSlice returns s if non-nil, or an empty slice. This avoids
// nil vs []string{} confusion in JSON round-trips and struct comparison.
func safeStringSlice(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// ImageAssetToClip converts an models.ImageAsset to models.Clip for unified handling.
// Uses SlugID as ID (consistent with asset index) and Hash as FileHash.
// This is the canonical converter — do NOT create copies in handlers or services.
func ImageAssetToClip(assetItem *models.ImageAsset) *asset.MediaAsset {
	if assetItem == nil {
		return nil
	}
	name := assetItem.Description
	if name == "" {
		name = filepath.Base(assetItem.PathRel)
	}
	// Use SlugID as primary ID for consistency with the asset index.
	// Fall back to Hash if SlugID is empty.
	id := assetItem.SlugID
	if id == "" {
		id = assetItem.Hash
	}
	return &asset.MediaAsset{
		ID:          id,
		Name:        name,
		Filename:    filepath.Base(assetItem.PathRel),
		DriveLink:   assetItem.SourceURL,
		DriveFileID: assetItem.DriveFileID,
		FileHash:    assetItem.Hash,
		LocalPath:   assetItem.PathRel,
		Source:      "images",
		MediaType:   "image",
		Tags:        assetItem.Tags,
		SearchTerms: []string{assetItem.Description},
		CreatedAt:   assetItem.CreatedAt,
		UpdatedAt:   assetItem.CreatedAt,
	}
}
