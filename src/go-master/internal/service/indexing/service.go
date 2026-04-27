package indexing

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"
	"velox/go-master/internal/repository/clips"
	"velox/go-master/pkg/models"
)

// Service handles indexing of video assets
type Service struct {
	repo *clips.Repository
	log  *zap.Logger
}

// NewService creates a new indexing service
func NewService(repo *clips.Repository, log *zap.Logger) *Service {
	return &Service{
		repo: repo,
		log:  log,
	}
}

// IndexDirectory scans a directory and updates the database with found clips
func (s *Service) IndexDirectory(ctx context.Context, rootDir string) error {
	s.log.Info("Starting directory indexing", zap.String("path", rootDir))

	err := filepath.WalkDir(rootDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}

		// Supported video formats
		ext := strings.ToLower(filepath.Ext(path))
		if ext != ".mp4" && ext != ".mkv" && ext != ".mov" && ext != ".avi" {
			return nil
		}

		relPath, _ := filepath.Rel(rootDir, path)
		folderPath := filepath.Dir(relPath)
		if folderPath == "." {
			folderPath = "General"
		}

		filename := d.Name()
		name := strings.TrimSuffix(filename, filepath.Ext(filename))

		// Check if clip already exists by folder_path + filename to avoid duplicates
		existingClip, err := s.repo.GetClipByFolderAndFilename(ctx, folderPath, filename)
		if err != nil {
			s.log.Warn("Failed to check existing clip", zap.String("name", name), zap.Error(err))
		}

		clipID := uuid.New().String()
		if existingClip != nil {
			clipID = existingClip.ID // Reuse existing ID to prevent duplicates
			s.log.Debug("Reusing existing clip ID", zap.String("name", name), zap.String("id", clipID))
		}

		clip := &models.Clip{
			ID:           clipID,
			Name:         name,
			Filename:     filename,
			FolderID:     folderPath,
			FolderPath:   folderPath,
			Group:        folderPath,
			MediaType:    "clip",
			DownloadLink: "file://" + path,
			Tags:         strings.Split(strings.ToLower(name), " "),
		}

		if err := s.repo.UpsertClip(ctx, clip); err != nil {
			s.log.Error("Failed to upsert clip", zap.String("name", name), zap.Error(err))
			return nil // Continue indexing other clips
		}

		return nil
	})

	if err != nil {
		return fmt.Errorf("indexing failed: %w", err)
	}

	s.log.Info("Indexing completed")
	return nil
}

// StartCron starts periodic indexing
func (s *Service) StartCron(ctx context.Context, rootDir string, interval time.Duration) {
	ticker := time.NewTicker(interval)
	go func() {
		for {
			select {
			case <-ticker.C:
				if err := s.IndexDirectory(ctx, rootDir); err != nil {
					s.log.Error("Periodic indexing failed", zap.Error(err))
				}
			case <-ctx.Done():
				ticker.Stop()
				return
			}
		}
	}()
}
