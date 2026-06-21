package images

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	textutil "github.com/Marcuss-ops/PipelineGen/pkg/textutil"
	"go.uber.org/zap"
)

// UploadToStyleDrive carica un'immagine su Drive in una subfolder per stile.
// Crea la struttura: {driveRoot}/{style}/{subject}/
func (s *Service) UploadToStyleDrive(ctx context.Context, asset *asset.ImageAsset, style string) (string, string, error) {
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
		DriveRootOverride: s.cfg.Drive.VideoAIFolder(),
	}
	imagePath := filepath.Join(s.imagesDir, asset.PathRel)

	fileID, webLink, err := s.mediaStore.UploadToDrive(ctx, req, imagePath)
	if err != nil {
		return "", "", fmt.Errorf("style-based Drive upload: %w", err)
	}

	// Recuperiamo la descrizione originale o usiamo un prompt fallback se non c'è.
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
		prompt = asset.SubjectID // Fallback to subject
	}

	// Call unified tagger ONCE and reuse result
	metaResult, metaErr := s.tagImageMetadata(ctx, prompt, style, generator, asset.Hash, imagePath, asset.Width, asset.Height)
	if metaErr == nil && metaResult != nil {
		s.uploadImageMetadata(ctx, req, metaResult)
	}

	s.log.Info("image uploaded to Drive with style",
		zap.String("file_id", fileID),
		zap.String("style", style),
	)
	return webLink, fileID, nil
}
