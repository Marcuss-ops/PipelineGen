package images

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.uber.org/zap"
	"velox/go-master/internal/media/models"
	"velox/go-master/internal/media/storage"
)

// uploadImageMetadata writes a metadata.json file in the same Drive folder as the image.
func (s *Service) uploadImageMetadata(ctx context.Context, req storage.AssetDestinationRequest, prompt, style, generator, fileID, driveLink, hash, localPath string, width, height int) {
	// Call Python tagger for rich metadata
	cleanPrompt := prompt
	if strings.Contains(prompt, "for prompt: ") {
		parts := strings.SplitN(prompt, "for prompt: ", 2)
		if len(parts) == 2 {
			cleanPrompt = parts[1]
		}
	}
	meta, err := s.callSemanticTagger(ctx, cleanPrompt, style, "image", generator)
	if err != nil {
		s.log.Warn("uploadImageMetadata: semantic tagger failed, using fallback", zap.Error(err))
		// Fallback to basic metadata
		fSubject, fTags := extractSubjectAndTags(prompt)
		styleList := []string{}
		if style != "" {
			styleList = append(styleList, style)
		}
		meta = &SemanticMetadataPayload{
			AssetID:             hash,
			AssetType:           "image",
			SemanticTier:        "generated_light",
			Source:              "generated",
			MediaType:           "image",
			Generator:           generator,
			PromptOriginal:      prompt,
			SemanticDescription: prompt,
			Subjects:            []string{fSubject},
			Tags:                fTags,
			Style:               styleList,
			SearchText:          prompt,
			EmbeddingStatus:     "pending",
			CreatedAt:           time.Now().Format(time.RFC3339),
		}
	} else {
		meta.AssetID = hash
		// NEW: LLM Fallback if confidence is low
		if meta.Confidence < 0.6 {
			s.log.Info("uploadImageMetadata: confidence low, calling LLM fallback", zap.Float64("confidence", meta.Confidence))
			meta.SemanticDescription = s.callLLMFallback(ctx, "image", cleanPrompt, style)
		}
	}

	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		s.log.Warn("uploadImageMetadata: failed to marshal metadata", zap.Error(err))
		return
	}

	tmpPath := filepath.Join(s.tempDir, "img_metadata_"+req.Hash+".json")
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		s.log.Warn("uploadImageMetadata: failed to write temp metadata file", zap.Error(err))
		return
	}
	defer os.Remove(tmpPath)

	metaReq := req
	metaReq.Ext = ".json"
	metaReq.Hash = "metadata" // Standard name for metadata inside the folder
	if metaReq.GenerationID == "" && hash != "" {
		metaReq.GenerationID = hash
	}

	if _, _, err := s.mediaStore.UploadToDrive(ctx, metaReq, tmpPath); err != nil {
		s.log.Warn("uploadImageMetadata: failed to upload metadata.json", zap.Error(err))
		return
	}
	s.log.Info("uploadImageMetadata: metadata.json uploaded", zap.String("prompt", prompt), zap.String("style", style))
}

// UploadBatchMetadata writes a single metadata.json for a group of assets.
func (s *Service) UploadBatchMetadata(ctx context.Context, genID, slug, style, prompt, generator string, assets []*models.ImageAsset) {
	s.log.Info("UploadBatchMetadata: starting", zap.String("gen_id", genID), zap.Int("assets", len(assets)))
	if s.mediaStore == nil {
		s.log.Warn("UploadBatchMetadata: mediaStore is nil")
		return
	}
	if genID == "" {
		s.log.Warn("UploadBatchMetadata: genID is empty")
		return
	}

	// 1. Call Python tagger for rich metadata base
	meta, err := s.callSemanticTagger(ctx, prompt, style, "image", generator)
	if err != nil {
		s.log.Warn("UploadBatchMetadata: semantic tagger failed, using fallback", zap.Error(err))
		// Fallback to basic metadata
		fSubject, fTags := extractSubjectAndTags(prompt)
		styleList := []string{}
		if style != "" {
			styleList = append(styleList, style)
		}
		meta = &SemanticMetadataPayload{
			AssetID:             genID,
			AssetType:           "image_group",
			SemanticTier:        "generated_light",
			Source:              "generated",
			MediaType:           "image",
			Generator:           generator,
			PromptOriginal:      prompt,
			SemanticDescription: prompt,
			Subjects:            []string{fSubject},
			Tags:                fTags,
			Style:               styleList,
			SearchText:          prompt,
			EmbeddingStatus:     "pending",
			CreatedAt:           time.Now().Format(time.RFC3339),
		}
	} else {
		meta.AssetID = genID
		meta.AssetType = "image_group"
		// NEW: LLM Fallback if confidence is low
		if meta.Confidence < 0.6 {
			s.log.Info("UploadBatchMetadata: confidence low, calling LLM fallback", zap.Float64("confidence", meta.Confidence))
			meta.SemanticDescription = s.callLLMFallback(ctx, "image", prompt, style)
		}
	}

	// 2. Add individual asset info
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
	meta.Assets = assetInfos

	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		s.log.Warn("UploadBatchMetadata: failed to marshal metadata", zap.Error(err))
		return
	}

	tmpPath := filepath.Join(s.tempDir, "group_metadata_"+genID+".json")
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return
	}
	defer os.Remove(tmpPath)

	req := storage.AssetDestinationRequest{
		Source:       storage.SourceImage,
		MediaType:    storage.MediaTypeImage,
		Subject:      slug,
		GenerationID: genID,
		Style:        style,
		Ext:          ".json",
		Hash:         "metadata",
	}

	if _, _, err := s.mediaStore.UploadToDrive(ctx, req, tmpPath); err != nil {
		s.log.Warn("UploadBatchMetadata: failed to upload metadata.json", zap.Error(err))
		return
	}
	s.log.Info("UploadBatchMetadata: metadata.json uploaded for group", zap.String("gen_id", genID), zap.Int("assets_count", len(assets)))
}
