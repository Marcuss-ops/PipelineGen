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
	"strings"

	"go.uber.org/zap"
	driveapi "google.golang.org/api/drive/v3"
	"velox/go-master/internal/media/ingest"
	"velox/go-master/internal/media/models"
)
func (s *Service) downloadAndIngest(ctx context.Context, slug, imgURL, source, query, description string, tags []string) (*models.ImageAsset, error) {
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

	return s.IngestImage(ctx, slug, resp.Body, filepath.Base(imgURL), imgURL, description, tags)
}

func (s *Service) IngestImage(ctx context.Context, slug string, data io.Reader, filename, sourceURL, description string, tags []string) (*models.ImageAsset, error) {
	content, err := io.ReadAll(data)
	if err != nil {
		return nil, err
	}

	// Legacy dedup: SHA256 check per immagini già salvate col vecchio path
	hasher := sha256.New()
	hasher.Write(content)
	legacyHash := hex.EncodeToString(hasher.Sum(nil))

	if existing, err := s.repo.GetImageByHash(legacyHash); err == nil && existing != nil {
		s.log.Info("Image with this hash already exists (legacy SHA256)", zap.String("hash", legacyHash))
		return existing, nil
	}

	// If ingest pipeline is available, use it
	if s.ingestSvc != nil {
		return s.ingestViaPipeline(ctx, slug, content, filename, sourceURL, description, tags)
	}

	// Fallback: legacy direct path
	return s.ingestDirect(slug, content, filename, sourceURL, description, tags, legacyHash)
}

func (s *Service) ingestViaPipeline(ctx context.Context, slug string, content []byte, filename, sourceURL, description string, tags []string) (*models.ImageAsset, error) {
	tmpDir, err := os.MkdirTemp(s.tempDir, "image-ingest-*")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	tmpPath := filepath.Join(tmpDir, filename)
	if err := os.WriteFile(tmpPath, content, 0644); err != nil {
		return nil, fmt.Errorf("failed to write temp file: %w", err)
	}

	result, err := s.ingestSvc.Ingest(ctx, &ingest.Request{
		Kind:      string(ingest.KindImage),
		LocalPath: tmpPath,
		Name:      description,
		Group:     slug,
		Source:    "image",
		SourceID:  sourceURL,
		Tags:      tags,
	})
	if err != nil {
		return nil, err
	}

	asset, err := s.repo.GetImageByHash(result.FileHash)
	if err != nil {
		asset, err = s.repo.GetImageByHash(result.ContentHash)
		if err != nil {
			return nil, fmt.Errorf("image not found after ingest: %w", err)
		}
	}
	return asset, nil
}

func (s *Service) ingestDirect(slug string, content []byte, filename, sourceURL, description string, tags []string, hash string) (*models.ImageAsset, error) {
	// 1. Trova Soggetto (o crealo)
	subject, err := s.repo.GetSubjectBySlugOrAlias(slug)
	if err != nil || subject == nil {
		subject = &models.Subject{
			Slug:        slug,
			DisplayName: slug,
		}
		_, err := s.repo.CreateSubject(subject)
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
		return nil, fmt.Errorf("failed to write image file: %w", err)
	}

	// 4. Upload to Drive if configured
	var driveFileID string
	if s.driveSvc != nil && s.driveFolderID != "" {
		s.log.Info("Uploading image to Google Drive", zap.String("filename", filename), zap.String("folder_id", s.driveFolderID))

		driveFile := &driveapi.File{
			Name:    filename,
			Parents: []string{s.driveFolderID},
		}

		res, err := s.driveSvc.Files.Create(driveFile).
			Media(strings.NewReader(string(content))).
			Fields("id").
			Do()

		if err != nil {
			s.log.Error("Drive upload failed", zap.Error(err))
		} else {
			driveFileID = res.Id
			s.log.Info("Drive upload successful", zap.String("file_id", driveFileID))
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

	if _, err := s.repo.AddImage(asset); err != nil {
		// Final safety check for UNIQUE constraint
		if existing, exErr := s.repo.GetImageByHash(hash); exErr == nil && existing != nil {
			return existing, nil
		}
		return nil, fmt.Errorf("failed to add image to repository: %w", err)
	}

	return asset, nil
}
