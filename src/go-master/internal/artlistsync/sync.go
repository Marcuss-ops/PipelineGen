// Package artlistsync provides Drive-to-ArtlistIndex synchronization.
package artlistsync

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"velox/go-master/internal/upload/drive"
	"velox/go-master/pkg/logger"

	"go.uber.org/zap"
)

type ArtlistClip struct {
	Term     string `json:"term"`
	Name     string `json:"name"`
	FileID   string `json:"file_id"`
	URL      string `json:"url"`
	Folder   string `json:"folder"`
	FolderID string `json:"folder_id"`
}

type ArtlistIndex struct {
	FolderID string                   `json:"folder_id"`
	Clips    []ArtlistClip            `json:"clips"`
	ByTerm   map[string][]ArtlistClip `json:"-"`
}

type ArtlistSync struct {
	driveClient   *drive.Client
	artlistRootID string
	outputPath    string
	lastSync      time.Time
	mu            sync.Mutex
	running       bool
}

func NewArtlistSync(driveClient *drive.Client, artlistRootID, outputPath string) *ArtlistSync {
	return &ArtlistSync{
		driveClient:   driveClient,
		artlistRootID: artlistRootID,
		outputPath:    outputPath,
	}
}

func (s *ArtlistSync) Sync(ctx context.Context) error {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return fmt.Errorf("sync already running")
	}
	s.running = true
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		s.running = false
		s.mu.Unlock()
	}()

	start := time.Now()
	logger.Info("Starting Artlist Drive-to-Index sync",
		zap.String("artlist_root_id", s.artlistRootID),
	)

	driveFolders, err := s.driveClient.ListFolders(ctx, drive.ListFoldersOptions{
		ParentID: s.artlistRootID,
		MaxDepth: 3,
		MaxItems: 500,
	})
	if err != nil {
		return fmt.Errorf("failed to scan Artlist Drive folders: %w", err)
	}

	var allClips []ArtlistClip

	for _, cat := range driveFolders {
		if strings.HasPrefix(cat.Name, ".") {
			continue
		}

		term := normalizeTerm(cat.Name)

		content, err := s.driveClient.GetFolderContent(ctx, cat.ID)
		if err != nil {
			logger.Warn("Failed to get Artlist folder content",
				zap.String("folder", cat.Name),
				zap.Error(err),
			)
			continue
		}

		for _, file := range content.Files {
			if !isVideoFile(file.Name) {
				continue
			}

			clip := ArtlistClip{
				Term:     term,
				Name:     file.Name,
				FileID:   file.ID,
				URL:      fmt.Sprintf("https://drive.google.com/file/d/%s/view?usp=drivesdk", file.ID),
				Folder:   fmt.Sprintf("Stock/Artlist/%s", cat.Name),
				FolderID: cat.ID,
			}
			allClips = append(allClips, clip)
		}

		for _, sub := range cat.Subfolders {
			subTerm := normalizeTerm(sub.Name)
			if subTerm == "" {
				subTerm = term
			}

			subContent, err := s.driveClient.GetFolderContent(ctx, sub.ID)
			if err != nil {
				continue
			}

			for _, file := range subContent.Files {
				if !isVideoFile(file.Name) {
					continue
				}

				clip := ArtlistClip{
					Term:     subTerm,
					Name:     file.Name,
					FileID:   file.ID,
					URL:      fmt.Sprintf("https://drive.google.com/file/d/%s/view?usp=drivesdk", file.ID),
					Folder:   fmt.Sprintf("Stock/Artlist/%s/%s", cat.Name, sub.Name),
					FolderID: sub.ID,
				}
				allClips = append(allClips, clip)
			}

			for _, sub2 := range sub.Subfolders {
				sub2Term := normalizeTerm(sub2.Name)
				if sub2Term == "" {
					sub2Term = subTerm
				}

				sub2Content, err := s.driveClient.GetFolderContent(ctx, sub2.ID)
				if err != nil {
					continue
				}

				for _, file := range sub2Content.Files {
					if !isVideoFile(file.Name) {
						continue
					}

					clip := ArtlistClip{
						Term:     sub2Term,
						Name:     file.Name,
						FileID:   file.ID,
						URL:      fmt.Sprintf("https://drive.google.com/file/d/%s/view?usp=drivesdk", file.ID),
						Folder:   fmt.Sprintf("Stock/Artlist/%s/%s/%s", cat.Name, sub.Name, sub2.Name),
						FolderID: sub2.ID,
					}
					allClips = append(allClips, clip)
				}
			}
		}
	}

	index := &ArtlistIndex{
		FolderID: s.artlistRootID,
		Clips:    allClips,
	}

	index.ByTerm = make(map[string][]ArtlistClip)
	for _, clip := range allClips {
		index.ByTerm[clip.Term] = append(index.ByTerm[clip.Term], clip)
	}

	data, err := json.MarshalIndent(index, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal index: %w", err)
	}

	if err := os.WriteFile(s.outputPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write index: %w", err)
	}

	s.lastSync = time.Now()

	logger.Info("Artlist Drive-to-Index sync completed",
		zap.Int("clips_synced", len(allClips)),
		zap.Int("terms", len(index.ByTerm)),
		zap.Duration("duration", time.Since(start)),
	)

	return nil
}

func (s *ArtlistSync) GetLastSyncTime() time.Time {
	return s.lastSync
}

func (s *ArtlistSync) IsRunning() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running
}

func normalizeTerm(name string) string {
	name = strings.ToLower(name)
	name = strings.ReplaceAll(name, " ", "")
	return name
}

func isVideoFile(name string) bool {
	lower := strings.ToLower(name)
	return strings.HasSuffix(lower, ".mp4") ||
		strings.HasSuffix(lower, ".mov") ||
		strings.HasSuffix(lower, ".avi") ||
		strings.HasSuffix(lower, ".mkv") ||
		strings.HasSuffix(lower, ".webm")
}
