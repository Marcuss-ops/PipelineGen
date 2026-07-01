package images

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	pathutil "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/files"
	textutil "github.com/Marcuss-ops/PipelineGen/pkg/textutil"
	"go.uber.org/zap"
)

// ── Ingest ─────────────────────────────────────────────────────────────

func (s *ImageStorageService) downloadAndIngest(ctx context.Context, slug, imgURL, style, source, query, description string, tags []string) (*asset.ImageAsset, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", imgURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("bad status code: %d", resp.StatusCode)
	}
	return s.IngestImage(ctx, slug, style, "", resp.Body, filepath.Base(imgURL), imgURL, description, tags, false, false)
}

// IngestImage ingests image data into the canonical media_assets pipeline.
func (s *ImageStorageService) IngestImage(ctx context.Context, slug, style, genID string, data io.Reader, filename, sourceURL, description string, tags []string, skipDrive, skipMetadata bool) (*asset.ImageAsset, error) {
	ingestCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 60*time.Second)
	defer cancel()

	content, err := io.ReadAll(data)
	if err != nil {
		return nil, err
	}

	hasher := sha256.New()
	hasher.Write(content)
	legacyHash := hex.EncodeToString(hasher.Sum(nil))

	if s.repo == nil {
		s.log.Warn("IngestImage: repo is nil, returning mock asset")
		return &asset.ImageAsset{
			SlugID:      slug,
			Description: description,
			SourceURL:   sourceURL,
			Hash:        legacyHash,
			Status:      "ready",
			Origin:      asset.ImageOriginRetrieved,
			Provider:    asset.ProviderUnknown,
		}, nil
	}

	if existing, err := s.repo.GetImageByHash(ingestCtx, legacyHash); err == nil && existing != nil {
		existingStyle := pathutil.ExtractStyleFromPath(existing.PathRel)
		if style == "" || existingStyle == style {
			filePath := filepath.Join(s.imagesDir, existing.PathRel)
			if _, statErr := os.Stat(filePath); statErr == nil {
				s.log.Info("IngestImage: hash dedup hit, returning existing",
					zap.String("hash", legacyHash),
					zap.String("style", existingStyle),
				)
				return existing, nil
			}
		}
		s.log.Info("IngestImage: hash dedup skipped (style mismatch or stale)",
			zap.String("hash", legacyHash),
			zap.String("requested_style", style),
			zap.String("existing_style", existingStyle),
		)
	}

	s.log.Info("IngestImage: ingesting image",
		zap.String("slug", slug),
		zap.String("style", style),
		zap.String("gen_id", genID),
		zap.String("hash", legacyHash),
		zap.Bool("skip_drive", skipDrive),
	)

	return s.ingestDirect(ingestCtx, slug, style, genID, content, filename, sourceURL, description, tags, legacyHash, skipDrive, skipMetadata)
}

func (s *ImageStorageService) ingestDirect(ctx context.Context, slug, style, genID string, content []byte, filename, source, description string, tags []string, hash string, skipDrive, skipMetadata bool) (*asset.ImageAsset, error) {
	promptSubject, promptTags := extractSubjectAndTags(description)
	if slug == "" || slug == "unknown" {
		slug = textutil.Slugify(promptSubject)
	}
	if len(tags) == 0 {
		tags = promptTags
	}

	subject, err := s.repo.GetSubjectBySlugOrAlias(ctx, slug)
	if err != nil || subject == nil {
		subject = &asset.Subject{Slug: slug, DisplayName: slug}
		if _, err := s.repo.CreateSubject(ctx, subject); err != nil {
			return nil, fmt.Errorf("create subject %q: %w", slug, err)
		}
	}

	ext := filepath.Ext(filename)
	if ext == "" {
		ext = ".jpg"
	}

	req := drive.AssetDestinationRequest{
		Source:       drive.SourceType(source),
		MediaType:    drive.MediaTypeImage,
		Subject:      slug,
		Hash:         hash,
		Ext:          ext,
		Style:        style,
		GenerationID: genID,
	}
	if aiDriveRoot := s.aiImageDriveRootForSource(source, style); aiDriveRoot != "" {
		req.DriveRootOverride = aiDriveRoot
	}

	dest, err := s.mediaStore.ResolveDest(req)
	if err != nil {
		return nil, fmt.Errorf("resolve destination: %w", err)
	}

	relPath := dest.RelativePath
	fullPath := dest.LocalPath
	if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
		return nil, fmt.Errorf("create directory: %w", err)
	}

	if err := os.WriteFile(fullPath, content, 0644); err != nil {
		s.log.Error("ingestDirect: failed to write file", zap.String("path", fullPath), zap.Error(err))
		return nil, fmt.Errorf("failed to write image file: %w", err)
	}
	s.log.Info("ingestDirect: file saved", zap.String("path", fullPath), zap.Int("bytes", len(content)))

	generator := source
	if strings.HasPrefix(source, "http://") || strings.HasPrefix(source, "https://") {
		if strings.Contains(source, "wikipedia.org") {
			generator = "wikipedia"
		} else if strings.Contains(source, "duckduckgo") {
			generator = "duckduckgo"
		} else {
			generator = "web-download"
		}
	}

	imgWidth, imgHeight := decodeImageDimensions(content)

	metaResult, taggerErr := s.meta.tagImageMetadata(ctx, description, style, generator, hash, fullPath, imgWidth, imgHeight)
	if taggerErr != nil {
		s.log.Error("ingestDirect: tagImageMetadata validation or execution failed, deleting local file and aborting", zap.Error(taggerErr))
		_ = os.Remove(fullPath)
		return nil, taggerErr
	}

	var driveFileID string
	if s.mediaStore != nil && !skipDrive {
		fileID, _, err := s.mediaStore.UploadToDrive(ctx, req, fullPath)
		if err != nil {
			s.log.Warn("Drive upload failed", zap.Error(err))
		} else {
			driveFileID = fileID
			s.log.Info("Drive upload successful", zap.String("file_id", fileID))
			if !skipMetadata && metaResult != nil {
				s.meta.uploadImageMetadata(ctx, req, metaResult)
			}
		}
	}

	var metaJSON []byte
	if taggerErr == nil && metaResult != nil && metaResult.Payload != nil {
		payloadCopy := *metaResult.Payload
		payloadCopy.AssetID = hash
		metaJSON, _ = json.Marshal(payloadCopy)
		tags = uniqueAppend(tags, payloadCopy.Tags...)
	} else {
		metaMap := map[string]any{
			"prompt":    description,
			"style":     style,
			"generator": generator,
		}
		metaJSON, _ = json.Marshal(metaMap)
	}

	imgAsset := &asset.ImageAsset{
		SubjectID:    slug,
		Hash:         hash,
		PathRel:      relPath,
		SourceURL:    source,
		Description:  description,
		DriveFileID:  driveFileID,
		Width:        imgWidth,
		Height:       imgHeight,
		SizeBytes:    int64(len(content)),
		Status:       "ready",
		MetadataJSON: string(metaJSON),
		Tags:         tags,
		Origin:       classifyImageOrigin(source, generator),
		Provider:     classifyImageProvider(source, generator),
	}

	if _, err := s.repo.AddImage(ctx, imgAsset); err != nil {
		errMsg := strings.ToLower(err.Error())
		if strings.Contains(errMsg, "unique") || strings.Contains(errMsg, "constraint") {
			s.log.Info("ingestDirect: hash exists in DB, returning existing asset",
				zap.String("hash", hash),
				zap.String("path", relPath),
				zap.String("style", style),
			)
			existing, getErr := s.repo.GetImageByHash(ctx, hash)
			if getErr == nil && existing != nil {
				return existing, nil
			}
			return imgAsset, nil
		}
		return nil, fmt.Errorf("failed to add image to repository: %w", err)
	}

	return imgAsset, nil
}

// ── Origin / Provider classification (Step 2, July 2026) ──────────────

// classifyImageOrigin determines the ImageOrigin from the source string.
//
// Mapping:
//   - google-slides, google-flow, flux, nvidia → generated
//   - wikipedia, duckduckgo, searxng, drive → retrieved
//   - upload → uploaded
//   - default → retrieved (safe fallback for unknown sources)
func classifyImageOrigin(source, generator string) asset.ImageOrigin {
	lower := strings.ToLower(source)

	// AI generation providers → generated.
	if isAIImageSource(source) || isAIImageSource(generator) {
		return asset.ImageOriginGenerated
	}

	// Manual upload → uploaded.
	if lower == "upload" {
		return asset.ImageOriginUploaded
	}

	// Web retrieval providers → retrieved.
	if strings.Contains(lower, "wikipedia") ||
		strings.Contains(lower, "duckduckgo") ||
		strings.Contains(lower, "searxng") ||
		strings.Contains(lower, "drive") {
		return asset.ImageOriginRetrieved
	}

	// Fallback: URL sources (http/https) are retrieved.
	if strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") {
		return asset.ImageOriginRetrieved
	}

	return asset.ImageOriginRetrieved
}

// classifyImageProvider maps the source string to the canonical ImageProvider.
func classifyImageProvider(source, generator string) asset.ImageProvider {
	lower := strings.ToLower(source)

	switch {
	case strings.Contains(lower, "wikipedia"):
		return asset.ProviderWikipedia
	case strings.Contains(lower, "duckduckgo"):
		return asset.ProviderDuckDuckGo
	case strings.Contains(lower, "searxng"):
		return asset.ProviderSearXNG
	case lower == "drive":
		return asset.ProviderDrive
	case strings.Contains(lower, "google-slides") || strings.Contains(lower, "google-flow"):
		return asset.ProviderGoogleSlides
	case strings.Contains(lower, "flux"):
		return asset.ProviderFlux
	case strings.Contains(lower, "nvidia"):
		return asset.ProviderNvidia
	case lower == "upload":
		return asset.ProviderUpload
	default:
		// Try the generator as fallback.
		genLower := strings.ToLower(generator)
		switch {
		case strings.Contains(genLower, "wikipedia"):
			return asset.ProviderWikipedia
		case strings.Contains(genLower, "duckduckgo"):
			return asset.ProviderDuckDuckGo
		case strings.Contains(genLower, "google-slides") || strings.Contains(genLower, "google-flow"):
			return asset.ProviderGoogleSlides
		case strings.Contains(genLower, "flux"):
			return asset.ProviderFlux
		case strings.Contains(genLower, "nvidia"):
			return asset.ProviderNvidia
		}
	}

	return asset.ProviderUnknown
}
