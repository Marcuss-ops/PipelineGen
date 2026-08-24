package images

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/delivery"
	"go.uber.org/zap"
)

// MetadataService handles semantic metadata tagging and upload to images.
type MetadataService struct {
	metaWriter SemanticPort
	publisher  delivery.Publisher
	tempDir    string
	log        *zap.Logger
}

// publishMetadata uploads metadata JSON via delivery.Publisher.Publish.
func (m *MetadataService) publishMetadata(ctx context.Context, style, subject, filePath string) error {
	if m == nil {
		return fmt.Errorf("MetadataService.publishMetadata: nil receiver")
	}
	if m.publisher == nil {
		return fmt.Errorf("MetadataService.publishMetadata: publisher not configured (P0-2 godlike/07: nil publisher fail-closed)")
	}
	_, err := m.publisher.Publish(ctx, delivery.PublishRequest{
		Destination:    delivery.DestinationImage,
		LocalPath:      filePath,
		Filename:       filepath.Base(filePath),
		Style:          style,
		Subject:        subject,
		Group:          subject,
		ConflictPolicy: delivery.ConflictOverwrite,
	})
	return err
}

var ccLicenseRegex = regexp.MustCompile(`(?i)(cc-by|creative\s*commons|public\s*domain|pd|gfdl|copyright\s*free|creative-commons)`)

// tagImageMetadata calls metaWriter.Write() ONCE to produce semantic metadata.
func (m *MetadataService) tagImageMetadata(ctx context.Context, prompt, style, generator, hash, localPath string, width, height int) (*SemanticWriteResult, error) {
	if m.metaWriter == nil {
		return nil, nil
	}

	cleanPrompt := prompt
	if strings.Contains(prompt, "for prompt: ") {
		parts := strings.SplitN(prompt, "for prompt: ", 2)
		if len(parts) == 2 {
			cleanPrompt = parts[1]
		}
	}

	// Use the canonical image origin classification. The context key
	// still allows callers to override (e.g. ingestion paths that already
	// know the source type), but the default must never be "generated"
	// for retrieved images because that produces metadata_json.origin
	// = "generated" and diverges from asset.Origin.
	sourceType := string(asset.ClassifyImageOrigin(generator, ""))
	if val, ok := ctx.Value(SourceTypeKey).(string); ok && val != "" {
		sourceType = val
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
		lowerImgURL := strings.ToLower(imageURL)
		validExts := []string{".jpg", ".jpeg", ".png", ".gif", ".webp", ".avif", ".tiff", ".bmp", ".svg"}
		isValidImgURL := false
		for _, ext := range validExts {
			if strings.Contains(lowerImgURL, ext) {
				isValidImgURL = true
				break
			}
		}
		if !isValidImgURL && (strings.Contains(lowerImgURL, "/image") || strings.Contains(lowerImgURL, "/photo") || strings.Contains(lowerImgURL, "/upload")) {
			isValidImgURL = true
		}
		if !isValidImgURL {
			return nil, fmt.Errorf("invalid image_url: does not point to a direct image file (URL: %s)", imageURL)
		} else {
			if license == "" || !ccLicenseRegex.MatchString(license) {
				m.log.Warn("tagImageMetadata: license unrecognized or empty, falling back to 'None'", zap.String("license", license))
				license = "None"
			}
		}
	}

	req := SemanticWriteRequest{
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
		TempDir:    m.tempDir,
		Extensions: buildImageSemanticExtension(width, height),
	}

	if sourceType == "retrieved" {
		req.Generator = ""
	}

	result, err := m.metaWriter.Write(ctx, req)
	if err != nil {
		return nil, err
	}
	return result, nil
}

// uploadImageMetadata writes a metadata.json file in the same Drive folder as the image.
func (m *MetadataService) uploadImageMetadata(ctx context.Context, style, subject string, result *SemanticWriteResult) {
	if result == nil || result.LocalPath == "" {
		m.log.Warn("uploadImageMetadata: nil result or empty local path")
		return
	}
	if m.publisher == nil {
		m.log.Warn("uploadImageMetadata: publisher not configured")
		return
	}

	if _, err := m.publisher.Publish(ctx, delivery.PublishRequest{
		Destination:    delivery.DestinationImage,
		LocalPath:      result.LocalPath,
		Filename:       filepath.Base(result.LocalPath),
		Style:          style,
		Subject:        subject,
		Group:          subject,
		ConflictPolicy: delivery.ConflictOverwrite,
	}); err != nil {
		m.log.Warn("uploadImageMetadata: failed to upload metadata.json", zap.Error(err))
		return
	}
	m.log.Info("uploadImageMetadata: metadata.json uploaded",
		zap.String("prompt", result.Payload.PromptOriginal),
		zap.String("style", strings.Join(result.Payload.Style, ", ")),
		zap.Int("tags", len(result.Payload.Tags)),
	)
}

// UploadBatchMetadata writes a single metadata.json for a group of assets.
func (m *MetadataService) UploadBatchMetadata(ctx context.Context, genID, slug, style, prompt, generator string, assets []*asset.ImageAsset) {
	m.log.Info("UploadBatchMetadata: starting", zap.String("gen_id", genID), zap.Int("assets", len(assets)))
	if m.metaWriter == nil {
		m.log.Warn("UploadBatchMetadata: metadata writer not configured")
		return
	}
	if m.publisher == nil {
		m.log.Warn("UploadBatchMetadata: publisher not configured")
		return
	}
	if genID == "" {
		m.log.Warn("UploadBatchMetadata: genID is empty")
		return
	}

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

	result, err := m.metaWriter.Write(ctx, SemanticWriteRequest{
		AssetID:    genID,
		AssetType:  "image_group",
		MediaType:  "image",
		Source:     generator,
		SourceType: "generated",
		Generator:  generator,
		Style:      style,
		Prompt:     prompt,
		GroupID:    genID,
		Assets:     assetInfos,
		TempDir:    m.tempDir,
		Extensions: nil,
	})
	if err != nil {
		m.log.Warn("UploadBatchMetadata: metadata writer failed", zap.Error(err))
		return
	}

	if err := m.publishMetadata(ctx, style, slug, result.LocalPath); err != nil {
		m.log.Warn("UploadBatchMetadata: failed to upload metadata.json", zap.Error(err))
		return
	}
	m.log.Info("UploadBatchMetadata: metadata.json uploaded for group",
		zap.String("gen_id", genID),
		zap.Int("assets_count", len(assets)),
		zap.Int("tags", len(result.Payload.Tags)),
	)
}
