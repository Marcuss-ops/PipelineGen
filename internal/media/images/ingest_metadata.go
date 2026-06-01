package images

import (
	"context"
	"strings"

	"go.uber.org/zap"
	"velox/go-master/internal/media/models"
	"velox/go-master/internal/media/semantic"
	"velox/go-master/internal/media/storage"
)

// uploadImageMetadata writes a metadata.json file in the same Drive folder as the image.
// Uses the unified semantic.MetadataWriter for tagger invocation + fallback.
func (s *Service) uploadImageMetadata(ctx context.Context, req storage.AssetDestinationRequest, prompt, style, generator, fileID, driveLink, hash, localPath string, width, height int) {
	if s.metaWriter == nil {
		s.log.Warn("uploadImageMetadata: metadata writer not configured")
		return
	}
	if s.mediaStore == nil {
		s.log.Warn("uploadImageMetadata: media store not configured")
		return
	}

	// Clean prompt of technical prefixes
	cleanPrompt := prompt
	if strings.Contains(prompt, "for prompt: ") {
		parts := strings.SplitN(prompt, "for prompt: ", 2)
		if len(parts) == 2 {
			cleanPrompt = parts[1]
		}
	}

	// Use unified MetadataWriter for ALL media types
	result, err := s.metaWriter.Write(ctx, semantic.WriteRequest{
		AssetID:    hash,
		AssetType:  "image",
		MediaType:  "image",
		Source:     "generated",
		Generator:  generator,
		Style:      style,
		Prompt:     cleanPrompt,
		LocalPath:  localPath,
		TempDir:    s.tempDir,
		Extensions: semantic.BuildImageExtension(width, height, "", "", 0),
	})
	if err != nil {
		s.log.Warn("uploadImageMetadata: metadata writer failed", zap.Error(err))
		return
	}

	// Upload metadata.json via Drive
	metaLocalPath := result.LocalPath
	if metaLocalPath == "" {
		s.log.Warn("uploadImageMetadata: metadata writer returned empty local path")
		return
	}

	// Use the same Drive folder as the image, named "metadata.json"
	metaReq := req
	metaReq.Ext = ".json"
	metaReq.Hash = "metadata"

	if _, _, err := s.mediaStore.UploadToDrive(ctx, metaReq, metaLocalPath); err != nil {
		s.log.Warn("uploadImageMetadata: failed to upload metadata.json", zap.Error(err))
		return
	}
	s.log.Info("uploadImageMetadata: metadata.json uploaded",
		zap.String("prompt", prompt),
		zap.String("style", style),
		zap.Int("tags", len(result.Payload.Tags)),
	)
}

// UploadBatchMetadata writes a single metadata.json for a group of assets.
func (s *Service) UploadBatchMetadata(ctx context.Context, genID, slug, style, prompt, generator string, assets []*models.ImageAsset) {
	s.log.Info("UploadBatchMetadata: starting", zap.String("gen_id", genID), zap.Int("assets", len(assets)))
	if s.metaWriter == nil {
		s.log.Warn("UploadBatchMetadata: metadata writer not configured")
		return
	}
	if s.mediaStore == nil {
		s.log.Warn("UploadBatchMetadata: mediaStore is nil")
		return
	}
	if genID == "" {
		s.log.Warn("UploadBatchMetadata: genID is empty")
		return
	}

	// Build asset info list for group metadata
	assetInfos := make([]map[string]any, len(assets))
	for i, a := range assets {
		assetInfos[i] = map[string]any{
			"hash":          a.Hash,
			"path":          a.PathRel,
			"width":         a.Width,
			"height":        a.Height,
			"drive_id":      a.DriveFileID,
			"variant_index": i + 1,
		}
	}

	// Use unified MetadataWriter
	result, err := s.metaWriter.Write(ctx, semantic.WriteRequest{
		AssetID:    genID,
		AssetType:  "image_group",
		MediaType:  "image",
		Source:     "generated",
		Generator:  generator,
		Style:      style,
		Prompt:     prompt,
		GroupID:    genID,
		Assets:     assetInfos,
		TempDir:    s.tempDir,
		Extensions: nil, // group metadata doesn't need type-specific extensions
	})
	if err != nil {
		s.log.Warn("UploadBatchMetadata: metadata writer failed", zap.Error(err))
		return
	}

	// Upload metadata.json via Drive
	req := storage.AssetDestinationRequest{
		Source:       storage.SourceImage,
		MediaType:    storage.MediaTypeImage,
		Subject:      slug,
		GenerationID: genID,
		Style:        style,
		Ext:          ".json",
		Hash:         "metadata",
	}

	if _, _, err := s.mediaStore.UploadToDrive(ctx, req, result.LocalPath); err != nil {
		s.log.Warn("UploadBatchMetadata: failed to upload metadata.json", zap.Error(err))
		return
	}
	s.log.Info("UploadBatchMetadata: metadata.json uploaded for group",
		zap.String("gen_id", genID),
		zap.Int("assets_count", len(assets)),
		zap.Int("tags", len(result.Payload.Tags)),
	)
}
