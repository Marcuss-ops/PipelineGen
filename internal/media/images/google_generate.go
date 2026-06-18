package images

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/media/models"
	"github.com/Marcuss-ops/PipelineGen/pkg/pathutil"
	"github.com/Marcuss-ops/PipelineGen/pkg/timeutil"
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
) (*models.ImageAsset, error) {
	cleanPrompt := pickImagePrompt(subject, topic, prompts)
	if cleanPrompt == "" {
		return nil, fmt.Errorf("missing image prompt")
	}

	// Check if this image has already been generated and is in the DB
	if s.repo != nil && s.repo.DB() != nil {
		// Escape LIKE wildcards in prompt to prevent false dedup matches
		escapedPrompt := strings.ReplaceAll(strings.ReplaceAll(cleanPrompt, "%", "\\%"), "_", "\\_")
		descPattern := "%for prompt: " + escapedPrompt
		var img models.ImageAsset
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

	// Apply style from registry if provided
	styledPrompt := cleanPrompt
	if s.styleRegistry != nil && style != "" {
		styledPrompt = s.styleRegistry.ApplyStyle(cleanPrompt, style)
	}

	// Google Slides is the ONLY image backend in this build. NVIDIA fallback
	// was removed because (a) the local sidecar `/v1/infer` is unreachable
	// in this env and (b) no NVIDIA_API_KEY is configured. Errors propagate
	// directly, so callers see the real Google Slides status instead of a
	// synthesized "both providers failed" message — fail-fast on the actual
	// signal.
	asset, err := s.generateGoogleSlidesImage(ctx, cleanPrompt, styledPrompt, subject, style, tags, width, height, skipDrive)
	if err == nil && asset != nil {
		return asset, nil
	}
	return nil, fmt.Errorf("google slides image generation failed: %w", err)
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
