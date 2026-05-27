package images

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.uber.org/zap"
	"velox/go-master/internal/media/models"
	"velox/go-master/internal/media/storage"
)
func (s *Service) downloadAndIngest(ctx context.Context, slug, imgURL, style, source, query, description string, tags []string) (*models.ImageAsset, error) {
	req, _ := http.NewRequestWithContext(ctx, "GET", imgURL, nil)
	req.Header.Set("User-Agent", userAgent)

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("bad status code: %d", resp.StatusCode)
	}

	return s.IngestImage(ctx, slug, style, "", resp.Body, filepath.Base(imgURL), imgURL, description, tags, false)
}

func (s *Service) IngestImage(ctx context.Context, slug, style, genID string, data io.Reader, filename, sourceURL, description string, tags []string, skipDrive bool) (*models.ImageAsset, error) {
	content, err := io.ReadAll(data)
	if err != nil {
		return nil, err
	}

	// Legacy dedup: SHA256 check per immagini già salvate col vecchio path
	hasher := sha256.New()
	hasher.Write(content)
	legacyHash := hex.EncodeToString(hasher.Sum(nil))

	if existing, err := s.repo.GetImageByHash(ctx, legacyHash); err == nil && existing != nil {
		// Verify the file actually exists on disk (may have been cleaned up)
		filePath := filepath.Join(s.imagesDir, existing.PathRel)
		if _, statErr := os.Stat(filePath); statErr == nil {
			s.log.Info("IngestImage: hash dedup hit, returning existing", zap.String("hash", legacyHash))
			return existing, nil
		}
		s.log.Warn("IngestImage: hash dedup stale, re-ingesting",
			zap.String("hash", legacyHash),
			zap.String("old_path", filePath),
		)
	}

	s.log.Info("IngestImage: ingesting image",
		zap.String("slug", slug),
		zap.String("style", style),
		zap.String("gen_id", genID),
		zap.String("hash", legacyHash),
		zap.Bool("skip_drive", skipDrive),
	)

	return s.ingestDirect(ctx, slug, style, genID, content, filename, sourceURL, description, tags, legacyHash, skipDrive)
}

func (s *Service) ingestDirect(ctx context.Context, slug, style, genID string, content []byte, filename, sourceURL, description string, tags []string, hash string, skipDrive bool) (*models.ImageAsset, error) {
	// 1. Trova Soggetto (o crealo)
	subject, err := s.repo.GetSubjectBySlugOrAlias(ctx, slug)
	if err != nil || subject == nil {
		subject = &models.Subject{
			Slug:        slug,
			DisplayName: slug,
		}
		_, err := s.repo.CreateSubject(ctx, subject)
		if err != nil {
			s.log.Warn("Ingest: subject might exist", zap.String("slug", slug))
		}
	}

	// 2. Prepara percorsi
	ext := filepath.Ext(filename)
	if ext == "" {
		ext = ".jpg"
	}
	
	// Create request for resolver
	req := storage.AssetDestinationRequest{
		Source:       storage.SourceImage,
		MediaType:    storage.MediaTypeImage,
		Subject:      slug, // Prompt slug
		Hash:         hash,
		Ext:          ext,
		Style:        style, // Chosen style
		GenerationID: genID,
	}

	dest, err := s.mediaStore.ResolveDest(req)
	if err != nil {
		return nil, fmt.Errorf("resolve destination: %w", err)
	}

	relPath := dest.RelativePath
	fullPath := dest.LocalPath
	os.MkdirAll(filepath.Dir(fullPath), 0755)

	// 3. Salva il file fisico
	if err := os.WriteFile(fullPath, content, 0644); err != nil {
		s.log.Error("ingestDirect: failed to write file", zap.String("path", fullPath), zap.Error(err))
		return nil, fmt.Errorf("failed to write image file: %w", err)
	}
	s.log.Info("ingestDirect: file saved", zap.String("path", fullPath), zap.Int("bytes", len(content)))

	// 4. Resolve generator dynamically from sourceURL
	generator := sourceURL
	if strings.HasPrefix(sourceURL, "http://") || strings.HasPrefix(sourceURL, "https://") {
		if strings.Contains(sourceURL, "wikipedia.org") {
			generator = "wikipedia"
		} else if strings.Contains(sourceURL, "duckduckgo") {
			generator = "duckduckgo"
		} else {
			generator = "web-download"
		}
	}

	// 5. Upload to Drive if configured (skip if disabled by fullimages pipeline)
	var driveFileID string
	if s.mediaStore != nil && !skipDrive {
		fileID, link, err := s.mediaStore.UploadToDrive(ctx, req, fullPath)
		if err != nil {
			s.log.Warn("Drive upload failed", zap.Error(err))
		} else {
			driveFileID = fileID
			s.log.Info("Drive upload successful", zap.String("file_id", fileID))
			
			// Estrai dimensioni reali dell'immagine
			imgWidth, imgHeight := decodeImageDimensions(content)

			// Upload metadata.json
			prompt := description // Usiamo description come prompt o info
			s.uploadImageMetadata(ctx, req, prompt, style, generator, fileID, link, hash, fullPath, imgWidth, imgHeight)
		}
	}

	// 6. Estrai dimensioni reali dell'immagine (per DB)
	imgWidth, imgHeight := decodeImageDimensions(content)

	// Prepare metadata for DB
	metaMap := map[string]any{
		"prompt":    description,
		"style":     style,
		"generator": generator,
	}
	if strings.Contains(description, "for prompt: ") {
		parts := strings.SplitN(description, "for prompt: ", 2)
		if len(parts) == 2 {
			metaMap["prompt"] = parts[1]
		}
	}
	metaJSON, _ := json.Marshal(metaMap)

	// 7. Crea record DB con dimensioni reali
	asset := &models.ImageAsset{
		SubjectID:    slug,
		Hash:         hash,
		PathRel:      relPath,
		SourceURL:    sourceURL,
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
		// Final safety check for UNIQUE constraint
		if existing, exErr := s.repo.GetImageByHash(ctx, hash); exErr == nil && existing != nil {
			return existing, nil
		}
		return nil, fmt.Errorf("failed to add image to repository: %w", err)
	}

	return asset, nil
}

// decodeImageDimensions estrae larghezza e altezza da bytes immagine.
// Supporta JPEG, PNG, GIF. Per altri formati (webp, etc.) restituisce 0,0.
func decodeImageDimensions(data []byte) (int, int) {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return 0, 0
	}
	return cfg.Width, cfg.Height
}

// uploadImageMetadata writes a metadata.json file in the same Drive folder as the image.
func (s *Service) uploadImageMetadata(ctx context.Context, req storage.AssetDestinationRequest, prompt, style, generator, fileID, driveLink, hash, localPath string, width, height int) {
	subject, tags := extractSubjectAndTags(prompt)
	
	styleList := []string{}
	if style != "" {
		styleList = append(styleList, style)
	}

	meta := SemanticMetadataPayload{
		AssetID:             req.Hash,
		AssetType:           "image",
		PromptOriginal:      prompt,
		SemanticDescription: prompt, // Basic fallback, would be better to call LLM here
		Subjects:            []string{subject},
		Actions:             []string{}, // Placeholder
		Mood:                []string{}, // Placeholder
		Style:               styleList,
		SearchText:          strings.Join(append([]string{subject, style}, tags...), " "),
		EmbeddingReady:      true,
		Generator:           generator,
		CreatedAt:           time.Now().Format(time.RFC3339),
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
	if _, _, err := s.mediaStore.UploadToDrive(ctx, metaReq, tmpPath); err != nil {
		s.log.Warn("uploadImageMetadata: failed to upload metadata.json", zap.Error(err))
		return
	}
	s.log.Info("uploadImageMetadata: metadata.json uploaded", zap.String("prompt", prompt), zap.String("style", style))
}

