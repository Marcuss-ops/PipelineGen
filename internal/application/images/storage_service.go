package images

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/semantic"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outbox"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	pathutil "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/files"
	audio "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/media/ffmpeg"
	textutil "github.com/Marcuss-ops/PipelineGen/pkg/textutil"
	"go.uber.org/zap"
	driveapi "google.golang.org/api/drive/v3"
	"golang.org/x/sync/singleflight"
)

// ImageStorageService handles image storage, retrieval, Drive operations,
// web search, and media asset registration. It delegates metadata operations
// to MetadataService.
type ImageStorageService struct {
	repo          *assets.ImagesRepository
	stockRepo     *assets.ClipsRepository
	mediaStore    *drive.Store
	driveSvc      *driveapi.Service
	cfg           *config.Config
	imagesDir     string
	tempDir       string
	driveFolderID string
	client        *http.Client
	dispatcher    *outbox.Dispatcher
	dedup         singleflight.Group
	nvidiaSem     chan struct{}
	log           *zap.Logger
	gaServerURL   string
	gaDownloadDir string
	vidsProjectID string
	meta          *MetadataService
}

// ── Search & Download ─────────────────────────────────────────────────

// SearchAndDownload searches for an image locally and via web APIs.
func (s *ImageStorageService) SearchAndDownload(ctx context.Context, subjectSlug, displayName, query, lang string, tags []string) (*asset.ImageAsset, error) {
	slug := textutil.Slugify(subjectSlug)
	if slug == "" {
		slug = textutil.Slugify(query)
	}
	if lang == "" {
		lang = "it"
	}

	qLower := strings.ToLower(query)
	if qLower == "name" || qLower == "titolo" || len(query) < 2 {
		return nil, fmt.Errorf("invalid query term: %s", query)
	}

	subject, err := s.repo.GetSubjectBySlugOrAlias(ctx, slug)
	if err == nil && subject != nil {
		if images, err := s.repo.ListImagesBySubject(ctx, subject.Slug); err == nil && len(images) > 0 {
			s.log.Info("Images found in local database", zap.String("subject", subject.Slug), zap.Int("count", len(images)))
			if len(images) > 1 {
				source := rand.New(rand.NewSource(time.Now().UnixNano()))
				randomIndex := source.Intn(len(images))
				s.log.Info("Picking random image from database", zap.Int("index", randomIndex), zap.Int("total", len(images)))
				return &images[randomIndex], nil
			}
			return &images[0], nil
		}
	}

	if subject == nil {
		subject = &asset.Subject{Slug: slug, DisplayName: displayName}
		_, err := s.repo.CreateSubject(ctx, subject)
		if err != nil {
			s.log.Warn("Ingest: subject might already exist", zap.String("slug", slug))
		}
	}

	key := "search:" + slug + ":" + lang
	result, err, _ := s.dedup.Do(key, func() (interface{}, error) {
		return s.searchAndDownloadInner(ctx, slug, displayName, query, lang, tags, subject)
	})
	if err != nil {
		return nil, err
	}
	if asset, ok := result.(*asset.ImageAsset); ok {
		return asset, nil
	}
	return nil, fmt.Errorf("singleflight: unexpected result type")
}

func (s *ImageStorageService) searchAndDownloadInner(ctx context.Context, slug, displayName, query, lang string, tags []string, subject *asset.Subject) (*asset.ImageAsset, error) {
	s.log.Info("Disambiguating with Wikidata", zap.String("query", query), zap.String("lang", lang))
	wikiTitle, qid, _ := s.searchWikidata(query, lang)
	finalQuery := query
	if wikiTitle != "" {
		finalQuery = wikiTitle
		s.log.Info("Wikidata disambiguation successful", zap.String("original", query), zap.String("resolved", finalQuery), zap.String("qid", qid))
	} else {
		s.log.Warn("Wikidata disambiguation found nothing", zap.String("query", query))
	}

	s.log.Info("Searching for image on Wikipedia", zap.String("query", finalQuery), zap.String("lang", lang))
	imgURL, wikiTitle := s.searchWikipedia(finalQuery, lang)
	source := "wikipedia"
	wikiURL := ""
	if wikiTitle != "" {
		wikiURL = fmt.Sprintf("https://%s.wikipedia.org/wiki/%s", lang, strings.ReplaceAll(wikiTitle, " ", "_"))
	}

	if imgURL == "" {
		s.log.Info("Wikipedia failed, trying SearXNG for images", zap.String("query", query))
		imgURL = s.searchSearXNGImages(ctx, query)
		if imgURL != "" {
			source = "searxng"
		}
	}
	if imgURL == "" {
		s.log.Info("SearXNG failed or skipped, falling back to DuckDuckGo (wide)", zap.String("query", query))
		imgURL = s.searchDDGWide(query)
		source = "duckduckgo"
	}
	if imgURL == "" {
		return nil, fmt.Errorf("no image found for query: %s", query)
	}

	s.log.Info("Downloading image", zap.String("url", imgURL), zap.String("source", source))
	description := fmt.Sprintf("Image for %s found via %s", displayName, source)

	provCtx := context.WithValue(ctx, SourceTypeKey, "retrieved")
	provCtx = context.WithValue(provCtx, RetrieverKey, source)
	provCtx = context.WithValue(provCtx, ImageURLKey, imgURL)
	if wikiURL != "" {
		provCtx = context.WithValue(provCtx, PageURLKey, wikiURL)
	} else {
		provCtx = context.WithValue(provCtx, PageURLKey, imgURL)
	}
	if source == "wikipedia" {
		provCtx = context.WithValue(provCtx, LicenseKey, "CC-BY-SA-4.0")
		provCtx = context.WithValue(provCtx, AuthorKey, "Wikipedia Contributors")
	} else {
		provCtx = context.WithValue(provCtx, LicenseKey, "Unknown")
		provCtx = context.WithValue(provCtx, AuthorKey, "Unknown")
	}

	asset, err := s.downloadAndIngest(provCtx, slug, imgURL, slug, source, finalQuery, description, tags)
	if err == nil && asset != nil {
		meta := make(map[string]any)
		if asset.MetadataJSON != "" && asset.MetadataJSON != "{}" {
			_ = json.Unmarshal([]byte(asset.MetadataJSON), &meta)
		}
		meta["source_image_url"] = imgURL
		if wikiURL != "" {
			meta["source_page_url"] = wikiURL
		}
		meta["source_name"] = source
		meta["source_query"] = finalQuery
		metaJSON, _ := json.Marshal(meta)
		_ = s.repo.UpdateImageMetadata(ctx, asset.Hash, string(metaJSON))
		asset.MetadataJSON = string(metaJSON)
	}
	return asset, err
}

// ── Web Search ─────────────────────────────────────────────────────────

// SearchWebImage searches for a real image matching the prompt via DuckDuckGo.
func (s *ImageStorageService) SearchWebImage(ctx context.Context, prompt, slug string, tags []string) (*asset.ImageAsset, error) {
	if slug == "" {
		slug = textutil.Slugify(prompt)
	}
	s.log.Info("Searching web image", zap.String("prompt", prompt), zap.String("slug", slug))

	imgURL := s.searchDDGWide(prompt)
	if imgURL == "" {
		return nil, fmt.Errorf("no image found on DuckDuckGo for: %s", prompt)
	}
	s.log.Info("Found image URL on DuckDuckGo", zap.String("url", imgURL))

	req, err := http.NewRequestWithContext(ctx, "GET", imgURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create download request: %w", err)
	}
	req.Header.Set("User-Agent", userAgent)

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to download image: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download failed with status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 20*1024*1024))
	if err != nil {
		return nil, fmt.Errorf("failed to read image body: %w", err)
	}
	if len(body) == 0 {
		return nil, fmt.Errorf("downloaded image is empty")
	}

	s.log.Info("Image downloaded", zap.Int("size_bytes", len(body)), zap.String("url", imgURL))

	filename := extractFilename(imgURL, prompt)
	description := fmt.Sprintf("Web image for: %s", prompt)

	asset, err := s.IngestImage(ctx, slug, "", "", strings.NewReader(string(body)), filename, imgURL, description, tags, false, false)
	if err != nil {
		return nil, fmt.Errorf("ingest image: %w", err)
	}

	meta := make(map[string]any)
	if asset.MetadataJSON != "" && asset.MetadataJSON != "{}" {
		_ = json.Unmarshal([]byte(asset.MetadataJSON), &meta)
	}
	meta["source_image_url"] = imgURL
	meta["source_name"] = "duckduckgo"
	meta["source_query"] = prompt
	metaJSON, _ := json.Marshal(meta)
	asset.MetadataJSON = string(metaJSON)

	s.log.Info("Web image ingested successfully",
		zap.String("slug", slug),
		zap.String("hash", asset.Hash),
		zap.String("path", asset.PathRel),
	)
	return asset, nil
}

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
		Source:    drive.SourceType(source),
		MediaType: drive.MediaTypeImage,
		Subject:   slug,
		Hash:      hash,
		Ext:       ext,
		Style:     style,
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

	asset := &asset.ImageAsset{
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
	}

	if _, err := s.repo.AddImage(ctx, asset); err != nil {
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
			return asset, nil
		}
		return nil, fmt.Errorf("failed to add image to repository: %w", err)
	}

	_ = description // searchText placeholder
	return asset, nil
}

// ── Drive Upload ───────────────────────────────────────────────────────

// UploadToStyleDrive uploads an image to Drive in a style-based subfolder.
func (s *ImageStorageService) UploadToStyleDrive(ctx context.Context, asset *asset.ImageAsset, style string) (string, string, error) {
	if s.mediaStore == nil {
		return "", "", fmt.Errorf("media store not configured")
	}
	if style == "" {
		return "", "", fmt.Errorf("style is required")
	}

	req := drive.AssetDestinationRequest{
		Source:            drive.SourceImage,
		MediaType:         drive.MediaTypeImage,
		Style:             style,
		Subject:           asset.SubjectID,
		Hash:              asset.Hash,
		Ext:               filepath.Ext(asset.PathRel),
		DriveRootOverride: s.cfg.Drive.ImagesFolder(),
	}
	imagePath := filepath.Join(s.imagesDir, asset.PathRel)

	fileID, webLink, err := s.mediaStore.UploadToDrive(ctx, req, imagePath)
	if err != nil {
		return "", "", fmt.Errorf("style-based Drive upload: %w", err)
	}

	prompt := asset.Description
	generator := "nvidia"
	if asset.SourceURL == "google-vids" || asset.SourceURL == "google-slides" || textutil.ContainsCI(prompt, "google vids") || textutil.ContainsCI(prompt, "google slides") {
		generator = "google-slides"
	} else if asset.MetadataJSON != "" && asset.MetadataJSON != "{}" {
		var meta map[string]any
		if err := json.Unmarshal([]byte(asset.MetadataJSON), &meta); err == nil {
			if genVal, ok := meta["generator"].(string); ok && genVal != "" {
				generator = genVal
			}
		}
	}

	if strings.HasPrefix(prompt, "AI generated image") {
		parts := strings.SplitN(prompt, "for prompt: ", 2)
		if len(parts) == 2 {
			prompt = parts[1]
		}
	}
	if prompt == "" {
		prompt = asset.SubjectID
	}

	metaResult, metaErr := s.meta.tagImageMetadata(ctx, prompt, style, generator, asset.Hash, imagePath, asset.Width, asset.Height)
	if metaErr == nil && metaResult != nil {
		s.meta.uploadImageMetadata(ctx, req, metaResult)
	}

	s.log.Info("image uploaded to Drive with style", zap.String("file_id", fileID), zap.String("style", style))
	return webLink, fileID, nil
}

// FormatDriveLink returns a Google Drive web-view link for the given file ID.
func (s *ImageStorageService) FormatDriveLink(id string) string {
	if id == "" {
		return ""
	}
	return "https://drive.google.com/file/d/" + id
}

// ── Video Asset Registration ───────────────────────────────────────────

// RegisterVideoAsset uploads a video to Drive and creates a record in media_assets.
func (s *ImageStorageService) RegisterVideoAsset(ctx context.Context, filePath, description, source, style string, durationSec int, existingDriveFileID, existingDriveLink string) error {
	if s.stockRepo == nil {
		return fmt.Errorf("stock repo not configured")
	}
	if _, err := os.Stat(filePath); err != nil {
		return fmt.Errorf("video file not found: %w", err)
	}

	id := fmt.Sprintf("vid_%x_%d", sha256Hash(filePath), time.Now().Unix())
	subject := textutil.Slugify(description)
	name := description
	if len(name) > 80 {
		name = name[:80]
	}
	if style != "" {
		name = fmt.Sprintf("[%s] %s", style, name)
	}

	uploaded := false
	var folderID string
	var semanticMeta *semantic.Payload
	var driveFileID, driveLink string
	if existingDriveFileID != "" {
		driveFileID = existingDriveFileID
		driveLink = existingDriveLink
	} else if s.mediaStore != nil {
		req := drive.AssetDestinationRequest{
			Source:    drive.SourceImage,
			MediaType: drive.MediaTypeImageVideo,
			Subject:   subject,
			Hash:      id,
			Ext:       ".mp4",
			Style:     style,
		}
		folderID, _ = s.mediaStore.EnsureDriveFolder(ctx, req)
		fid, wl, err := s.mediaStore.UploadToDrive(ctx, req, filePath)
		if err != nil {
			s.log.Warn("RegisterVideoAsset: Drive upload failed (non fatale)", zap.Error(err))
		} else {
			driveFileID = fid
			driveLink = wl
			uploaded = true
			s.log.Info("RegisterVideoAsset: Drive upload successful", zap.String("file_id", fid))
			semanticMeta = s.uploadVideoMetadata(ctx, req, description, style, source, fid, wl, durationSec, id, filePath, folderID)
		}
	}

	clip := &asset.Asset{
		ID:        id,
		Name:      name,
		Source:    asset.Source(source),
		MediaType: asset.MediaType("video"),
		CreatedAt: time.Now(),
	}
	clip.SetDriveFileID(driveFileID)
	clip.SetDriveLink(driveLink)
	clip.SetMetadataString("prompt", description)
	clip.SetMetadataString("style", style)
	clip.SetMetadataString("generator", source)

	if semanticMeta != nil {
		clip.SearchText = semanticMeta.SearchText
		clip.Tags = uniqueAppend(clip.Tags, semanticMeta.Tags...)
		if style != "" {
			clip.Group = style
		} else if len(semanticMeta.Subjects) > 0 {
			clip.Group = semanticMeta.Subjects[0]
		}
	} else if style != "" {
		clip.Group = style
	}

	if s.dispatcher != nil {
		contentHash := sha256Hash(filePath + id)
		if err := s.dispatcher.EnqueueAndIndex(ctx, clip, contentHash); err != nil {
			return fmt.Errorf("dispatcher.EnqueueAndIndex video %s: %w", id, err)
		}
		s.log.Debug("RegisterVideoAsset: saved via dispatcher", zap.String("id", id))
	} else if err := s.stockRepo.Upsert(ctx, clip); err != nil {
		return err
	}

	if uploaded && s.mediaStore != nil {
		s.registerAudioClip(ctx, filePath, description, style, source, durationSec, id, subject)
	}

	if uploaded && filePath != "" {
		if err := os.Remove(filePath); err != nil {
			s.log.Warn("RegisterVideoAsset: failed to remove local file", zap.String("path", filePath), zap.Error(err))
		} else {
			s.log.Info("RegisterVideoAsset: local file removed after Drive upload", zap.String("path", filePath))
		}
	}

	return nil
}

func (s *ImageStorageService) uploadVideoMetadata(ctx context.Context, req drive.AssetDestinationRequest, prompt, style, generator, fileID, driveLink string, durationSec int, hash, localPath, folderID string) *SemanticMetadataPayload {
	if s.meta == nil || s.meta.metaWriter == nil {
		s.log.Warn("uploadVideoMetadata: metadata writer not configured")
		return nil
	}

	result, err := s.meta.metaWriter.Write(ctx, semantic.WriteRequest{
		AssetID:    hash,
		AssetType:  "video",
		MediaType:  "video",
		Source:     "generated",
		Generator:  generator,
		Style:      style,
		Prompt:     prompt,
		LocalPath:  localPath,
		TempDir:    s.tempDir,
		Extensions: semantic.BuildVideoExtension(durationSec, 0, "", false),
		Assets: []map[string]any{
			{"file_id": fileID, "drive_link": driveLink, "duration_sec": durationSec, "hash": hash},
		},
	})
	if err != nil {
		s.log.Warn("uploadVideoMetadata: metadata writer failed", zap.Error(err))
		return nil
	}

	metaReq := req
	metaReq.Hash = "metadata"
	metaReq.Ext = ".json"
	if _, _, err := s.mediaStore.UploadToDrive(ctx, metaReq, result.LocalPath); err != nil {
		s.log.Warn("uploadVideoMetadata: failed to upload metadata.json", zap.Error(err))
		return result.Payload
	}
	s.log.Info("uploadVideoMetadata: metadata.json uploaded",
		zap.String("asset_type", result.Payload.AssetType),
		zap.String("style", style),
		zap.String("search_text", result.Payload.SearchText),
	)
	return result.Payload
}

func (s *ImageStorageService) registerAudioClip(ctx context.Context, videoPath, description, style, source string, durationSec int, videoID, subject string) {
	if s.meta == nil || s.meta.metaWriter == nil {
		s.log.Warn("registerAudioClip: metadata writer not configured")
		return
	}

	audioPath := filepath.Join(s.tempDir, videoID+"_audio.mp3")
	if err := audio.ExtractClip(ctx, "", videoPath, audioPath, 3); err != nil {
		s.log.Warn("registerAudioClip: audio extraction failed", zap.String("video_id", videoID), zap.Error(err))
		return
	}
	defer os.Remove(audioPath)

	req := drive.AssetDestinationRequest{
		Source:    drive.SourceSoundEffect,
		MediaType: drive.MediaTypeSoundEffect,
		Subject:   subject,
		Hash:      videoID + "_audio",
		Ext:       ".mp3",
		Style:     style,
	}

	folderID, err := s.mediaStore.EnsureDriveFolder(ctx, req)
	if err != nil {
		s.log.Warn("registerAudioClip: EnsureDriveFolder failed", zap.Error(err))
		return
	}

	fileID, webLink, err := s.mediaStore.UploadToDrive(ctx, req, audioPath)
	if err != nil {
		s.log.Warn("registerAudioClip: Drive upload failed", zap.Error(err))
		return
	}

	result, err := s.meta.metaWriter.Write(ctx, semantic.WriteRequest{
		AssetID:    videoID + "_audio",
		AssetType:  "sound_effect",
		MediaType:  "audio",
		Source:     source,
		Generator:  source,
		Style:      style,
		Prompt:     description,
		TempDir:    s.tempDir,
		Extensions: semantic.BuildAudioExtension(3, 0, 0, true, videoID),
	})

	var searchText string
	var tags []string
	if err == nil && result != nil && result.Payload != nil {
		searchText = result.Payload.SearchText
		tags = result.Payload.Tags
		audioReq := req
		audioReq.Hash = "metadata"
		audioReq.Ext = ".json"
		if _, _, err := s.mediaStore.UploadToDrive(ctx, audioReq, result.LocalPath); err != nil {
			s.log.Warn("registerAudioClip: metadata upload failed", zap.Error(err))
		}
	} else {
		s.log.Warn("registerAudioClip: metadata writer failed", zap.Error(err))
	}

	clip := &asset.Asset{
		ID:         videoID + "_audio",
		Name:       description + " (audio)",
		Source:     asset.Source(source),
		MediaType:  asset.MediaType("sound_effect"),
		Duration:   3000 * time.Millisecond,
		CreatedAt:  time.Now(),
		SearchText: searchText,
		Tags:       tags,
	}
	clip.SetLocalPath(audioPath)
	clip.SetDriveFileID(fileID)
	clip.SetDriveLink(webLink)
	clip.SetFolderID(folderID)
	if style != "" {
		clip.Group = style
	}

	if s.dispatcher != nil {
		contentHash := sha256Hash(audioPath)
		if err := s.dispatcher.EnqueueAndIndex(ctx, clip, contentHash); err != nil {
			s.log.Warn("registerAudioClip: dispatcher upsert failed", zap.Error(err))
			return
		}
		s.log.Debug("registerAudioClip: saved via dispatcher", zap.String("id", clip.ID))
	} else if err := s.stockRepo.Upsert(ctx, clip); err != nil {
		s.log.Warn("registerAudioClip: DB upsert failed", zap.Error(err))
		return
	}
	s.log.Info("registerAudioClip: audio extracted, uploaded, and registered",
		zap.String("video_id", videoID),
		zap.String("audio_id", clip.ID),
		zap.String("drive_link", webLink),
		zap.Int("tags_count", len(tags)),
	)
}

// ── Drive Sync ─────────────────────────────────────────────────────────

// SyncFromDrive syncs image assets from Google Drive to the local DB.
func (s *ImageStorageService) SyncFromDrive(ctx context.Context) error {
	if s.driveSvc == nil || s.driveFolderID == "" {
		return fmt.Errorf("drive service or folder ID not configured")
	}
	s.log.Info("Starting images sync from Drive", zap.String("folder_id", s.driveFolderID))
	return s.syncFolderRecursive(ctx, s.driveFolderID, "")
}

func (s *ImageStorageService) syncFolderRecursive(ctx context.Context, folderID, folderPath string) error {
	uploader := &drive.Uploader{Service: s.driveSvc}
	files, err := uploader.ListFiles(ctx, folderID)
	if err != nil {
		return err
	}
	for _, file := range files {
		if file.MimeType == "application/vnd.google-apps.folder" {
			newPath := filepath.Join(folderPath, file.Name)
			if err := s.syncFolderRecursive(ctx, file.ID, newPath); err != nil {
				s.log.Warn("failed to sync subfolder", zap.String("id", file.ID), zap.Error(err))
			}
			continue
		}
		lowerName := strings.ToLower(file.Name)
		if !strings.HasSuffix(lowerName, ".jpg") && !strings.HasSuffix(lowerName, ".jpeg") &&
			!strings.HasSuffix(lowerName, ".png") && !strings.HasSuffix(lowerName, ".webp") {
			continue
		}
		existing, err := s.repo.GetByDriveFileID(ctx, file.ID)
		if err == nil && existing != nil {
			continue
		}
		asset := &asset.ImageAsset{
			SubjectID:    textutil.Slugify(file.Name),
			Hash:         "drive_" + file.ID,
			PathRel:      "",
			SourceURL:    file.WebViewLink,
			Description:  "Synced from Drive: " + file.Name,
			DriveFileID:  file.ID,
			Status:       "ready",
			MetadataJSON: "{}",
		}
		if _, err := s.repo.AddImage(ctx, asset); err != nil {
			s.log.Warn("failed to add synced image", zap.String("name", file.Name), zap.Error(err))
		}
	}
	return nil
}

// ── Web Search Helpers ─────────────────────────────────────────────────

func (s *ImageStorageService) searchDDGWide(query string) string {
	vqdURL := fmt.Sprintf("https://duckduckgo.com/?q=%s&iax=images&ia=images", url.QueryEscape(query))
	req, _ := http.NewRequest("GET", vqdURL, nil)
	req.Header.Set("User-Agent", userAgent)
	resp, err := s.client.Do(req)
	if err != nil {
		return ""
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	vqd := extractVQD(string(body))
	if vqd == "" {
		return ""
	}
	for attempt := 0; attempt < 5; attempt++ {
		apiURL := fmt.Sprintf("https://duckduckgo.com/i.js?l=en-us&o=json&q=%s&vqd=%s&f=,,,&p=%d",
			url.QueryEscape(query), vqd, attempt)
		req, _ = http.NewRequest("GET", apiURL, nil)
		req.Header.Set("User-Agent", userAgent)
		resp, err = s.client.Do(req)
		if err != nil {
			if attempt == 4 {
				return ""
			}
			time.Sleep(200 * time.Millisecond)
			continue
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		var payload struct {
			Results []struct {
				Image     string `json:"image"`
				Width     int    `json:"width"`
				Height    int    `json:"height"`
				Thumbnail string `json:"thumbnail"`
			} `json:"results"`
		}
		if err := json.Unmarshal(body, &payload); err != nil || len(payload.Results) == 0 {
			continue
		}
		best := pickBestImage(payload.Results)
		if best != "" {
			return best
		}
	}
	return ""
}

func (s *ImageStorageService) searchSearXNGImages(ctx context.Context, query string) string {
	if s.cfg == nil || s.cfg.External.SearxngURL == "" {
		s.log.Info("SearXNG not configured, skipping image search")
		return ""
	}
	probeCtx, probeCancel := context.WithTimeout(ctx, 3*time.Second)
	defer probeCancel()
	probeURL := strings.TrimRight(s.cfg.External.SearxngURL, "/") + "/healthz"
	probeReq, _ := http.NewRequestWithContext(probeCtx, "GET", probeURL, nil)
	probeResp, probeErr := s.client.Do(probeReq)
	if probeErr != nil {
		s.log.Warn("SearXNG unreachable, skipping SearXNG search", zap.Error(probeErr))
		return ""
	}
	probeResp.Body.Close()

	searchCtx, searchCancel := context.WithTimeout(ctx, 5*time.Second)
	defer searchCancel()
	s.log.Info("Searching SearXNG for images", zap.String("query", query))
	params := url.Values{}
	params.Set("q", query)
	params.Set("format", "json")
	params.Set("categories", "images")
	reqURL := fmt.Sprintf("%s/search?%s", strings.TrimRight(s.cfg.External.SearxngURL, "/"), params.Encode())
	req, err := http.NewRequestWithContext(searchCtx, "GET", reqURL, nil)
	if err != nil {
		s.log.Error("Failed to create SearXNG request", zap.Error(err))
		return ""
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := s.client.Do(req)
	if err != nil {
		s.log.Error("SearXNG request failed", zap.Error(err))
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		s.log.Warn("SearXNG returned non-200 status", zap.Int("status", resp.StatusCode))
		return ""
	}
	var data struct {
		Results []struct {
			URL          string `json:"url"`
			ImgSrc       string `json:"img_src"`
			Thumbnail    string `json:"thumbnail"`
			ThumbnailSrc string `json:"thumbnail_src"`
			Width        int    `json:"width,omitempty"`
			Height       int    `json:"height,omitempty"`
		} `json:"results"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		s.log.Error("Failed to decode SearXNG response", zap.Error(err))
		return ""
	}
	if len(data.Results) == 0 {
		s.log.Warn("SearXNG returned 0 image results")
		return ""
	}
	best := ""
	bestScore := 0
	for _, r := range data.Results {
		img := r.ImgSrc
		if img == "" {
			img = r.Thumbnail
		}
		if img == "" {
			img = r.ThumbnailSrc
		}
		if img == "" || !strings.HasPrefix(img, "http") {
			continue
		}
		score := 10
		if r.Width >= 1080 {
			score = 100
		} else if r.Width >= 720 {
			score = 70
		} else if r.Width >= 480 {
			score = 40
		}
		if score > bestScore {
			bestScore = score
			best = img
		}
	}
	return best
}

func (s *ImageStorageService) searchWikidata(query, lang string) (string, string, string) {
	apiURL := fmt.Sprintf("https://www.wikidata.org/w/api.php?action=wbsearchentities&search=%s&language=%s&format=json&limit=10", url.QueryEscape(query), lang)
	req, _ := http.NewRequest("GET", apiURL, nil)
	req.Header.Set("User-Agent", userAgent)
	resp, err := s.client.Do(req)
	if err != nil {
		return "", "", ""
	}
	defer resp.Body.Close()
	var payload struct {
		Search []struct {
			ID          string `json:"id"`
			Label       string `json:"label"`
			Description string `json:"description"`
		} `json:"search"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil || len(payload.Search) == 0 {
		return "", "", ""
	}
	bestLabel, bestID, bestDescription := selectBestWikidataHit(query, payload.Search)
	if bestID == "" {
		return "", "", ""
	}
	return bestLabel, bestID, bestDescription
}

func (s *ImageStorageService) searchWikipedia(query, lang string) (string, string) {
	if imgURL, wikiTitle := s.wikipediaThumbnailByExactTitle(query, lang); imgURL != "" {
		return imgURL, wikiTitle
	}
	searchQueries := []string{strings.TrimSpace(query)}
	if !looksLikeProperName(query) && !textutil.ContainsCI(query, "pizza") && !textutil.ContainsCI(query, "italia") {
		searchQueries = append(searchQueries, strings.TrimSpace(query+" "+lang))
	}
	bestTitle := ""
	for _, searchQuery := range searchQueries {
		if searchQuery == "" {
			continue
		}
		searchURL := fmt.Sprintf("https://%s.wikipedia.org/w/api.php?action=query&list=search&srsearch=%s&format=json&srlimit=5", lang, url.QueryEscape(searchQuery))
		req, _ := http.NewRequest("GET", searchURL, nil)
		req.Header.Set("User-Agent", userAgent)
		resp, err := s.client.Do(req)
		if err != nil {
			s.log.Error("Wikipedia search request failed", zap.Error(err))
			continue
		}
		var searchPayload struct {
			Query struct {
				Search []struct {
					Title string `json:"title"`
				} `json:"search"`
			} `json:"query"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&searchPayload); err != nil {
			resp.Body.Close()
			s.log.Error("Failed to decode Wikipedia search response", zap.Error(err))
			continue
		}
		resp.Body.Close()
		bestTitle = selectBestWikiTitle(query, searchPayload.Query.Search)
		if bestTitle != "" {
			s.log.Info("Wikipedia best match found", zap.String("title", bestTitle), zap.String("query", searchQuery))
			break
		}
	}
	if bestTitle == "" {
		s.log.Warn("Wikipedia search returned no results", zap.String("query", query))
		return "", ""
	}
	apiURL := fmt.Sprintf("https://%s.wikipedia.org/w/api.php?action=query&prop=pageimages&titles=%s&piprop=original|thumbnail&pithumbsize=1000&format=json&redirects=1", lang, url.QueryEscape(bestTitle))
	req2, _ := http.NewRequest("GET", apiURL, nil)
	req2.Header.Set("User-Agent", userAgent)
	resp2, err := s.client.Do(req2)
	if err != nil {
		return "", ""
	}
	defer resp2.Body.Close()
	var payload2 struct {
		Query struct {
			Pages map[string]struct {
				Original struct {
					Source string `json:"source"`
				} `json:"original"`
				Thumbnail struct {
					Source string `json:"source"`
				} `json:"thumbnail"`
			} `json:"pages"`
		} `json:"query"`
	}
	if err := json.NewDecoder(resp2.Body).Decode(&payload2); err != nil {
		return "", ""
	}
	for _, page := range payload2.Query.Pages {
		if page.Original.Source != "" {
			return page.Original.Source, bestTitle
		}
		if page.Thumbnail.Source != "" {
			return page.Thumbnail.Source, bestTitle
		}
	}
	return "", ""
}

func (s *ImageStorageService) wikipediaThumbnailByExactTitle(title, lang string) (string, string) {
	apiURL := fmt.Sprintf("https://%s.wikipedia.org/w/api.php?action=query&prop=pageimages&titles=%s&piprop=original|thumbnail&pithumbsize=1000&format=json&redirects=1", lang, url.QueryEscape(title))
	req, _ := http.NewRequest("GET", apiURL, nil)
	req.Header.Set("User-Agent", userAgent)
	resp, err := s.client.Do(req)
	if err != nil {
		return "", ""
	}
	defer resp.Body.Close()
	var payload struct {
		Query struct {
			Pages map[string]struct {
				Title    string `json:"title"`
				Original struct {
					Source string `json:"source"`
				} `json:"original"`
				Thumbnail struct {
					Source string `json:"source"`
				} `json:"thumbnail"`
			} `json:"pages"`
		} `json:"query"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", ""
	}
	for _, page := range payload.Query.Pages {
		if page.Original.Source != "" {
			return page.Original.Source, page.Title
		}
		if page.Thumbnail.Source != "" {
			return page.Thumbnail.Source, page.Title
		}
	}
	return "", ""
}

// ── Helpers ────────────────────────────────────────────────────────────

func (s *ImageStorageService) aiImageDriveRootForSource(source, style string) string {
	if s == nil || s.cfg == nil {
		return ""
	}
	if !isAIImageSource(source) {
		return ""
	}
	styleFolders := map[string]string{
		"medieval": "1yfCnjvpZ3ZuFs7W0pRFNGzapRLGIykPi",
		"whiteboard": "1Znu_g8pUOXkXHG-1XkLMOcYN69umrlae",
		"anime": "1e1pW8ZaQYTwDV0po6tIxx_vUql_6CD_v",
		"cinematic": "1t6bhe8kquPqk7ypYzbobHqUq-HGjVdZw",
		"sketch": "1QrC74aZ8It43pQa5l5G6BNWcc18ksIo2",
		"watercolor": "1tzvn5PkOwZk3DPjjr8sIXKr9LKeM--rB",
		"cyberpunk": "1x8xcUFtIj7hkGF6CsPJCM822ooJL9kMu",
		"realistic": "1b5iP5aHekJUL1FB9ZC-WGkWxoDULyU9X",
		"heritage": "1l_cdMqhKrstV94V7Ym7wemJTUZjjWLq_",
		"kawaii": "1K5IcI3sC5qLID0M1ulSoUC355S_3lUNh",
		"professional-doc": "1g2Ef3yQCDWZ78YqnOnwhKmIghGJvPOPa",
		"cartoon": "1ab_YSfuKpj4CCh9twk3st5zv9fvMwS8B",
		"retro-print": "1141lRohkIiXp8NjGQlGj4bLLaQw6nCDb",
		"papercraft": "1yWlji7wololy_q3l8GAcmmF8goxJmOih",
		"gothic": "1CNNcNWY4YXyat9eqUsmsUEGeMmTXJY3t",
		"oil-painting": "1mI07oRaeabhGSmjdyKOICl5vSK6uSO7i",
		"3d-render": "1MWZy1rDXQKoAr0HRVMc7BdGAvqCaSe1y",
	}
	if folderID, ok := styleFolders[strings.ToLower(style)]; ok {
		return folderID
	}
	return s.cfg.Drive.ImagesFolder()
}
