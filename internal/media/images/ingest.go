package images

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"

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

	return s.IngestImage(ctx, slug, style, resp.Body, filepath.Base(imgURL), imgURL, description, tags, false)
}

func (s *Service) IngestImage(ctx context.Context, slug, style string, data io.Reader, filename, sourceURL, description string, tags []string, skipDrive bool) (*models.ImageAsset, error) {
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
		zap.String("hash", legacyHash),
		zap.Bool("skip_drive", skipDrive),
	)

	return s.ingestDirect(ctx, slug, style, content, filename, sourceURL, description, tags, legacyHash, skipDrive)
}

func (s *Service) ingestDirect(ctx context.Context, slug, style string, content []byte, filename, sourceURL, description string, tags []string, hash string, skipDrive bool) (*models.ImageAsset, error) {
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
	relPath := filepath.Join(slug, hash+ext)
	fullPath := filepath.Join(s.imagesDir, relPath)
	os.MkdirAll(filepath.Dir(fullPath), 0755)

	// 3. Salva il file fisico
	if err := os.WriteFile(fullPath, content, 0644); err != nil {
		s.log.Error("ingestDirect: failed to write file", zap.String("path", fullPath), zap.Error(err))
		return nil, fmt.Errorf("failed to write image file: %w", err)
	}
	s.log.Info("ingestDirect: file saved", zap.String("path", fullPath), zap.Int("bytes", len(content)))

	// 4. Upload to Drive if configured (skip if disabled by fullimages pipeline)
	var driveFileID string
	if s.mediaStore != nil && !skipDrive {
		fileID, _, err := s.mediaStore.UploadToDrive(ctx, storage.AssetDestinationRequest{
			Source:    storage.SourceImage,
			MediaType: storage.MediaTypeImage,
			Subject:   slug, // Prompt slug
			Hash:      hash,
			Ext:       ext,
			Style:     style, // Chosen style
		}, fullPath)
		if err != nil {
			s.log.Warn("Drive upload failed", zap.Error(err))
		} else {
			driveFileID = fileID
			s.log.Info("Drive upload successful", zap.String("file_id", fileID))
		}
	}

	// 5. Crea record DB
	asset := &models.ImageAsset{
		SubjectID:    slug,
		Hash:         hash,
		PathRel:      relPath,
		SourceURL:    sourceURL,
		Description:  description,
		DriveFileID:  driveFileID,
		Status:       "ready",
		MetadataJSON: "{}",
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
