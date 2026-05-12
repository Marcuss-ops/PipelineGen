package indexing

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"
	"velox/go-master/internal/repository/clips"
	"velox/go-master/pkg/models"
)

// Enricher defines the contract for AI-based metadata enrichment of clips.
type Enricher interface {
	IndexClip(ctx context.Context, clipID string) error
	IndexRunItems(ctx context.Context, items []map[string]interface{}) error
}

// Service handles indexing of video assets
type Service struct {
	repo     *clips.Repository
	enricher Enricher
	log      *zap.Logger
}

// NewService creates a new indexing service
func NewService(repo *clips.Repository, enricher Enricher, log *zap.Logger) *Service {
	return &Service{
		repo:     repo,
		enricher: enricher,
		log:      log,
	}
}

// IndexDirectory scans a directory and updates the database with found clips
func (s *Service) IndexDirectory(ctx context.Context, rootDir string) error {
	s.log.Info("Starting directory indexing", zap.String("path", rootDir))

	// Collect all paths first
	var paths []string
	err := filepath.WalkDir(rootDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		// Supported video formats
		ext := strings.ToLower(filepath.Ext(path))
		if ext == ".mp4" || ext == ".mkv" || ext == ".mov" || ext == ".avi" {
			paths = append(paths, path)
		}
		return nil
	})

	if err != nil {
		return fmt.Errorf("failed to walk directory: %w", err)
	}

	if len(paths) == 0 {
		s.log.Info("No clips found to index")
		return nil
	}

	s.log.Info("Found clips to index", zap.Int("count", len(paths)))

	// Worker pool setup
	numWorkers := 4
	pathChan := make(chan string, len(paths))
	var wg sync.WaitGroup

	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for path := range pathChan {
				s.indexSingleFile(ctx, rootDir, path)
			}
		}()
	}

	for _, path := range paths {
		pathChan <- path
	}
	close(pathChan)
	wg.Wait()

	s.log.Info("Indexing completed", zap.Int("total", len(paths)))
	return nil
}

func (s *Service) indexSingleFile(ctx context.Context, rootDir, path string) {
	relPath, _ := filepath.Rel(rootDir, path)
	folderPath := filepath.Dir(relPath)
	if folderPath == "." {
		folderPath = "General"
	}

	filename := filepath.Base(path)
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
		return
	}

	// Optional: Trigger enrichment if enricher is available
	if s.enricher != nil {
		if err := s.enricher.IndexClip(ctx, clipID); err != nil {
			s.log.Warn("Enrichment failed for clip", zap.String("id", clipID), zap.Error(err))
		}
	}
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
