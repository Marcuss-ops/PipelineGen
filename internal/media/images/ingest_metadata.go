package images

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"go.uber.org/zap"
	"github.com/Marcuss-ops/PipelineGen/internal/media/models"
	"github.com/Marcuss-ops/PipelineGen/internal/media/semantic"
	"github.com/Marcuss-ops/PipelineGen/internal/media/storage"
)

type contextKey string

const (
	SourceTypeKey = contextKey("source_type")
	RetrieverKey  = contextKey("retriever")
	PageURLKey    = contextKey("page_url")
	ImageURLKey   = contextKey("image_url")
	LicenseKey    = contextKey("license")
	AuthorKey     = contextKey("author")
)

var ccLicenseRegex = regexp.MustCompile(`(?i)(cc-by|creative\s*commons|public\s*domain|pd|gfdl|copyright\s*free|creative-commons)`)

// tagImageMetadata calls metaWriter.Write() ONCE to produce semantic metadata.
// Returns the WriteResult which can be used for both Drive upload and DB record.
func (s *Service) tagImageMetadata(ctx context.Context, prompt, style, generator, hash, localPath string, width, height int) (*semantic.WriteResult, error) {
	if s.metaWriter == nil {
		return nil, nil
	}

	// Clean prompt of technical prefixes
	cleanPrompt := prompt
	if strings.Contains(prompt, "for prompt: ") {
		parts := strings.SplitN(prompt, "for prompt: ", 2)
		if len(parts) == 2 {
			cleanPrompt = parts[1]
		}
	}

	sourceType := "generated"
	if val, ok := ctx.Value(SourceTypeKey).(string); ok {
		sourceType = val
	} else {
		if generator == "wikipedia" || generator == "duckduckgo" || generator == "searxng" || generator == "web-download" {
			sourceType = "retrieved"
		}
	}

	retriever := ""
	if val, ok := ctx.Value(RetrieverKey).(string); ok {
		retriever = val
	} else if sourceType == "retrieved" {
		retriever = generator
	}

	pageURL := ""
	if val, ok := ctx.Value(PageURLKey).(string); ok {
		pageURL = val
	}

	imageURL := ""
	if val, ok := ctx.Value(ImageURLKey).(string); ok {
		imageURL = val
	}

	license := ""
	if val, ok := ctx.Value(LicenseKey).(string); ok {
		license = val
	}

	author := ""
	if val, ok := ctx.Value(AuthorKey).(string); ok {
		author = val
	}

	if sourceType == "retrieved" {
		// Validazione image_url: deve essere un URL diretto a un'immagine
		lowerImgURL := strings.ToLower(imageURL)
		validExts := []string{".jpg", ".jpeg", ".png", ".gif", ".webp", ".avif", ".tiff", ".bmp", ".svg"}
		isValidImgURL := false
		for _, ext := range validExts {
			if strings.Contains(lowerImgURL, ext) {
				isValidImgURL = true
				break
			}
		}
		// Also accept URLs without extension if they contain image-related path segments
		if !isValidImgURL && (strings.Contains(lowerImgURL, "/image") || strings.Contains(lowerImgURL, "/photo") || strings.Contains(lowerImgURL, "/upload")) {
			isValidImgURL = true
		}

		if !isValidImgURL {
			return nil, fmt.Errorf("invalid image_url: does not point to a direct image file (URL: %s)", imageURL)
		} else {
			// Validazione licenza
			if license == "" || !ccLicenseRegex.MatchString(license) {
				s.log.Warn("tagImageMetadata: license unrecognized or empty, falling back to 'None'", zap.String("license", license))
				license = "None"
			}
		}
	}

	req := semantic.WriteRequest{
		AssetID:    hash,
		AssetType:  "image",
		MediaType:  "image",
		Source:     generator,
		SourceType: sourceType,
		Generator:  generator,
		Retriever:  retriever,
		PageURL:    pageURL,
		ImageURL:   imageURL,
		License:    license,
		Author:     author,
		Style:      style,
		Prompt:     cleanPrompt,
		LocalPath:  localPath,
		TempDir:    s.tempDir,
		Extensions: semantic.BuildImageExtension(width, height, "", "", 0),
	}

	// If source is wikipedia/searxng we set the retriever URL
	if sourceType == "retrieved" {
		req.Generator = ""
	}

	result, err := s.metaWriter.Write(ctx, req)
	if err != nil {
		return nil, err
	}
	return result, nil
}

// uploadImageMetadata writes a metadata.json file in the same Drive folder as the image.
// Uses a pre-computed semantic.WriteResult — does NOT call the tagger again.
// This avoids duplicating the Python subprocess call that tagImageMetadata already made.
func (s *Service) uploadImageMetadata(ctx context.Context, req storage.AssetDestinationRequest, result *semantic.WriteResult) {
	if result == nil || result.LocalPath == "" {
		s.log.Warn("uploadImageMetadata: nil result or empty local path")
		return
	}
	if s.mediaStore == nil {
		s.log.Warn("uploadImageMetadata: media store not configured")
		return
	}

	// Use the same Drive folder as the image, named "metadata.json"
	metaReq := req
	metaReq.Ext = ".json"
	metaReq.Hash = "metadata"

	if _, _, err := s.mediaStore.UploadToDrive(ctx, metaReq, result.LocalPath); err != nil {
		s.log.Warn("uploadImageMetadata: failed to upload metadata.json", zap.Error(err))
		return
	}
	s.log.Info("uploadImageMetadata: metadata.json uploaded",
		zap.String("prompt", result.Payload.PromptOriginal),
		zap.String("style", strings.Join(result.Payload.Style, ", ")),
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
