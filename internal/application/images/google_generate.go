package images

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	pathutil "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/files"
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

	// Real Google Slides click automation flow
	s.log.Info("Google Slides: starting Playwright click automation", zap.String("prompt", cleanPrompt))

	tempOut := filepath.Join(s.tempDir, fmt.Sprintf("slides_temp_%d.png", time.Now().Unix()))
	// Ensure temp directory exists
	if err := os.MkdirAll(s.tempDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create temp dir: %w", err)
	}
	defer os.Remove(tempOut)

	scriptPath := filepath.Join(s.scriptsDir, "bridges", "generate_slide_click.py")
	if _, err := os.Stat(scriptPath); os.IsNotExist(err) {
		scriptPath = "scripts/generate_slide_click.py" // fallback
	}

	profileDir := "data/google_slides_profile"
	if s.cfg != nil {
		profileDir = filepath.Join(s.cfg.Storage.DataDir, "google_slides_profile")
	}
	if abs, err := filepath.Abs(profileDir); err == nil {
		profileDir = abs
	}

	cmd := exec.CommandContext(ctx, "python3", scriptPath, "--prompt", cleanPrompt, "--output", tempOut, "--profile-dir", profileDir)
	output, err := cmd.CombinedOutput()
	if err != nil {
		s.log.Error("Google Slides click automation script failed", zap.Error(err), zap.String("output", string(output)))
		return nil, fmt.Errorf("google slides automation failed: %w (output: %s)", err, string(output))
	}

	imgFile, err := os.Open(tempOut)
	if err != nil {
		return nil, fmt.Errorf("failed to open generated slide image: %w", err)
	}
	defer imgFile.Close()

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
		imgFile,
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
