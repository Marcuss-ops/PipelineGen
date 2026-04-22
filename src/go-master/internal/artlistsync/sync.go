// Package artlistsync provides Drive-to-ArtlistIndex synchronization.
package artlistsync

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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
	statePath     string
	changeToken   string
	lastSync      time.Time
	mu            sync.Mutex
	running       bool
}

const fullRescanInterval = 24 * time.Hour

func NewArtlistSync(driveClient *drive.Client, artlistRootID, outputPath string, statePath ...string) *ArtlistSync {
	return &ArtlistSync{
		driveClient:   driveClient,
		artlistRootID: artlistRootID,
		outputPath:    outputPath,
		statePath:     firstOrEmpty(statePath),
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

	if s.statePath != "" {
		loaded, err := s.loadState()
		if err != nil {
			logger.Warn("Failed to load Artlist sync state", zap.String("path", s.statePath), zap.Error(err))
		} else {
			s.changeToken = loaded.ChangeToken
		}
	}

	forceFullRescan := s.shouldForceFullRescan()
	if s.changeToken != "" {
		hasChanges, newToken, err := s.hasDriveChanges(ctx, s.changeToken)
		if err != nil {
			logger.Warn("Artlist change check failed, falling back to full scan", zap.Error(err))
		} else if !hasChanges && !forceFullRescan {
			if newToken != "" {
				s.changeToken = newToken
				_ = s.saveState(false)
			}
			s.lastSync = time.Now()
			logger.Info("Artlist Drive-to-Index sync skipped, no Drive changes detected",
				zap.String("artlist_root_id", s.artlistRootID),
				zap.Duration("duration", time.Since(start)),
			)
			return nil
		} else if newToken != "" {
			s.changeToken = newToken
		}
	}

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
	if s.statePath != "" {
		if token, err := s.driveClient.GetStartPageToken(ctx); err == nil && token != "" {
			s.changeToken = token
		}
		if err := s.saveState(true); err != nil {
			logger.Warn("Failed to persist Artlist sync state", zap.String("path", s.statePath), zap.Error(err))
		}
	}

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

type artlistSyncState struct {
	ChangeToken    string    `json:"change_token"`
	LastSyncAt     time.Time `json:"last_sync_at"`
	LastFullScanAt time.Time `json:"last_full_scan_at"`
}

func (s *ArtlistSync) hasDriveChanges(ctx context.Context, pageToken string) (bool, string, error) {
	nextToken := pageToken
	for {
		changes, err := s.driveClient.ListChanges(ctx, nextToken, 100)
		if err != nil {
			return false, "", err
		}
		if len(changes.Changes) > 0 {
			token := changes.NewStartPageToken
			if token == "" {
				token = nextToken
			}
			return true, token, nil
		}
		if changes.NextPageToken == "" {
			token := changes.NewStartPageToken
			if token == "" {
				token = nextToken
			}
			return false, token, nil
		}
		nextToken = changes.NextPageToken
	}
}

func (s *ArtlistSync) loadState() (artlistSyncState, error) {
	if s.statePath == "" {
		return artlistSyncState{}, nil
	}
	data, err := os.ReadFile(s.statePath)
	if err != nil {
		return artlistSyncState{}, err
	}
	var state artlistSyncState
	if err := json.Unmarshal(data, &state); err != nil {
		return artlistSyncState{}, err
	}
	return state, nil
}

func (s *ArtlistSync) saveState(fullScan bool) error {
	if s.statePath == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(s.statePath), 0755); err != nil {
		return err
	}
	state := artlistSyncState{
		ChangeToken: s.changeToken,
		LastSyncAt:  s.lastSync,
	}
	if fullScan {
		state.LastFullScanAt = s.lastSync
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.statePath, data, 0644)
}

func (s *ArtlistSync) shouldForceFullRescan() bool {
	if s.statePath == "" {
		return true
	}
	state, err := s.loadState()
	if err != nil {
		return true
	}
	if state.LastFullScanAt.IsZero() {
		return true
	}
	return time.Since(state.LastFullScanAt) >= fullRescanInterval
}

func normalizeTerm(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	// Also allow underscore as space
	name = strings.ReplaceAll(name, "_", " ")
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

func firstOrEmpty(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return values[0]
}
