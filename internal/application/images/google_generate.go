package images

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"go.uber.org/zap"
	slides "google.golang.org/api/slides/v1"
	"google.golang.org/api/option"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	pathutil "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/files"
	driveauth "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	"github.com/Marcuss-ops/PipelineGen/pkg/textutil"
	timeutil "github.com/Marcuss-ops/PipelineGen/pkg/timeutil"
)

// GenerateSmartImage generates an AI image exclusively via Google Slides.
// It stores every successfully generated file using the existing ingest pipeline.
func (s *Service) GenerateSmartImage(
	ctx context.Context,
	subject string,
	topic string,
	style string,
	prompts []string,
	tags []string,
	width, height int,
	model string,
	skipDrive bool,
) (*asset.ImageAsset, error) {
	cleanPrompt := pickImagePrompt(subject, topic, prompts)
	if cleanPrompt == "" {
		return nil, fmt.Errorf("missing image prompt")
	}

	// Check if this image has already been generated and is in the DB
	if s.repo != nil && s.repo.DB() != nil {
		// Escape LIKE wildcards in prompt to prevent false dedup matches
		escapedPrompt := strings.ReplaceAll(strings.ReplaceAll(cleanPrompt, "%", "\\%"), "_", "\\_")
		descPattern := "%for prompt: " + escapedPrompt
		var img asset.ImageAsset
		var name, urlVal, tagsJSON, metaJSON, createdAtStr, fileHash, localPath, driveFileID sql.NullString
		err := s.repo.DB().QueryRowContext(ctx, `
			SELECT id, name, url, tags, metadata_json, created_at, file_hash, local_path, drive_file_id
			FROM media_assets
			WHERE media_type = 'image' AND name LIKE ?
			LIMIT 1
		`, descPattern).Scan(&img.SlugID, &name, &urlVal, &tagsJSON, &metaJSON, &createdAtStr, &fileHash, &localPath, &driveFileID)
		if err == nil {
			existingStyle := pathutil.ExtractStyleFromPath(localPath.String)
			if style == "" || existingStyle == style {
				s.log.Info("REUSING already generated image from database",
					zap.String("prompt", cleanPrompt),
					zap.String("style", existingStyle),
				)
				img.Description = name.String
				img.SourceURL = urlVal.String
				img.Hash = fileHash.String
				img.PathRel = localPath.String
				img.DriveFileID = driveFileID.String
				if createdAtStr.Valid {
					img.CreatedAt = timeutil.ParseRFC3339(createdAtStr.String)
				}
				if tagsJSON.Valid && tagsJSON.String != "" {
					_ = json.Unmarshal([]byte(tagsJSON.String), &img.Tags)
				}
				if metaJSON.Valid && metaJSON.String != "" {
					img.MetadataJSON = metaJSON.String
				}
				return &img, nil
			}
			s.log.Info("GenerateSmartImage: cache hit but style mismatch, re-generating",
				zap.String("requested_style", style),
				zap.String("cached_style", existingStyle),
			)
		}
	}

	// Real Google Slides Generation Flow
	if s.cfg == nil {
		return nil, fmt.Errorf("config not available")
	}

	s.log.Info("Google Slides: starting generation", zap.String("prompt", cleanPrompt))

	// Get HTTP client from auth
	httpClient, err := driveauth.NewGoogleHTTPClient(ctx, s.cfg.Paths.CredentialsFile, s.cfg.Paths.TokenFile, "https://www.googleapis.com/auth/drive", "https://www.googleapis.com/auth/presentations")
	if err != nil {
		return nil, fmt.Errorf("failed to init google client: %w", err)
	}

	slidesSvc, err := slides.NewService(ctx, option.WithHTTPClient(httpClient))
	if err != nil {
		return nil, fmt.Errorf("failed to init google slides service: %w", err)
	}

	// 1. Create presentation
	presentation, err := slidesSvc.Presentations.Create(&slides.Presentation{
		Title: "Velox Image Gen Slide",
	}).Do()
	if err != nil {
		return nil, fmt.Errorf("failed to create presentation: %w", err)
	}

	// Cleanup presentation on function exit (so we don't pollute user's Google Drive)
	defer func() {
		if s.driveSvc != nil {
			s.log.Debug("Google Slides: cleaning up presentation", zap.String("id", presentation.PresentationId))
			_ = s.driveSvc.Files.Delete(presentation.PresentationId).Do()
		}
	}()

	if len(presentation.Slides) == 0 {
		return nil, fmt.Errorf("created presentation has no slides")
	}
	slideID := presentation.Slides[0].ObjectId

	// 2. Add text box to slide with the prompt
	requests := []*slides.Request{
		{
			CreateShape: &slides.CreateShapeRequest{
				ObjectId:  "textBoxId",
				ShapeType: "TEXT_BOX",
				ElementProperties: &slides.PageElementProperties{
					PageObjectId: slideID,
					Size: &slides.Size{
						Height: &slides.Dimension{Magnitude: 400, Unit: "PT"},
						Width:  &slides.Dimension{Magnitude: 600, Unit: "PT"},
					},
					Transform: &slides.AffineTransform{
						ScaleX:     1,
						ScaleY:     1,
						TranslateX: 100,
						TranslateY: 100,
						Unit:       "PT",
					},
				},
			},
		},
		{
			InsertText: &slides.InsertTextRequest{
				ObjectId: "textBoxId",
				Text:     cleanPrompt,
			},
		},
	}
	_, err = slidesSvc.Presentations.BatchUpdate(presentation.PresentationId, &slides.BatchUpdatePresentationRequest{
		Requests: requests,
	}).Do()
	if err != nil {
		return nil, fmt.Errorf("failed to add text to slide: %w", err)
	}

	// 3. Get Thumbnail URL of the slide
	thumbnail, err := slidesSvc.Presentations.Pages.GetThumbnail(presentation.PresentationId, slideID).Do()
	if err != nil {
		return nil, fmt.Errorf("failed to get slide thumbnail: %w", err)
	}

	// 4. Download thumbnail image bytes
	req, err := http.NewRequestWithContext(ctx, "GET", thumbnail.ContentUrl, nil)
	if err != nil {
		return nil, err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to download thumbnail: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("thumbnail download returned status %d", resp.StatusCode)
	}

	imgData, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read thumbnail bytes: %w", err)
	}

	slug := textutil.Slugify(subject)
	if slug == "" {
		slug = textutil.Slugify(cleanPrompt)
	}
	if len(slug) > 50 {
		slug = slug[:50]
	}

	filename := fmt.Sprintf("slides_%d.png", time.Now().Unix())
	description := fmt.Sprintf("AI generated image via Google Slides for prompt: %s", cleanPrompt)

	return s.IngestImage(
		ctx,
		slug,
		style,
		"google-slides",
		bytes.NewReader(imgData),
		filename,
		"google-slides",
		description,
		tags,
		skipDrive,
		false,
	)
}

func pickImagePrompt(subject, topic string, prompts []string) string {
	for _, p := range prompts {
		if p = strings.TrimSpace(p); p != "" {
			return p
		}
	}

	subject = strings.TrimSpace(subject)
	topic = strings.TrimSpace(topic)

	switch {
	case subject != "" && topic != "":
		return fmt.Sprintf("%s, %s, cinematic landscape", subject, topic)
	case subject != "":
		return fmt.Sprintf("%s, cinematic landscape", subject)
	case topic != "":
		return fmt.Sprintf("%s, cinematic landscape", topic)
	default:
		return ""
	}
}
