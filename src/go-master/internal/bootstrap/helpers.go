package bootstrap

import (
	"fmt"
	"os"
	"time"

	"go.uber.org/zap"
	"velox/go-master/internal/clip"
	"velox/go-master/internal/downloader"
	"velox/go-master/internal/stockdb"
	"velox/go-master/internal/stockjob"
	"velox/go-master/pkg/config"
	"velox/go-master/pkg/logger"
	"velox/go-master/internal/service/scriptdocs"
)

// initArtlistSource initializes the Artlist DB source from config paths.
func initArtlistSource(cfg *config.Config, log *zap.Logger) *clip.ArtlistSource {
	artlistDBPath := cfg.GetArtlistDBPath()
	if artlistDBPath == "" {
		for _, candidate := range []string{"src/node-scraper/artlist_videos.db", "../src/node-scraper/artlist_videos.db"} {
			if _, err := os.Stat(candidate); err == nil {
				artlistDBPath = candidate
				break
			}
		}
	}
	if artlistDBPath == "" {
		log.Info("Artlist DB path not configured")
		return nil
	}
	if _, err := os.Stat(artlistDBPath); err != nil {
		log.Warn("Artlist DB not found", zap.String("path", artlistDBPath))
		return nil
	}
	artlistSrc := clip.NewArtlistSource(artlistDBPath)
	if err := artlistSrc.Connect(); err != nil {
		log.Warn("Failed to connect to Artlist DB", zap.Error(err))
		return nil
	}
	log.Info("Artlist source connected", zap.String("path", artlistDBPath))
	return artlistSrc
}

// convertConfigToStockFolders converts config StockFolderEntry map to scriptdocs StockFolder map.
func convertConfigToStockFolders(entries map[string]config.StockFolderEntry) map[string]scriptdocs.StockFolder {
	result := make(map[string]scriptdocs.StockFolder, len(entries))
	for k, e := range entries {
		result[k] = scriptdocs.StockFolder{ID: e.ID, Name: e.Name, URL: e.URL}
	}
	return result
}

// mainClipDB adapts StockDB to the ClipDatabase interface required by stockjob.Scheduler.
type mainClipDB struct {
	db *stockdb.StockDB
}

func (m *mainClipDB) ClipExists(platform downloader.Platform, videoID string) (bool, error) {
	entries, err := m.db.GetAllClips()
	if err != nil {
		return false, err
	}
	for _, entry := range entries {
		if entry.ClipID == videoID {
			return true, nil
		}
	}
	return false, nil
}

func (m *mainClipDB) AddClip(clip *stockjob.ClipRecord) error {
	entry := stockdb.StockClipEntry{
		ClipID:   clip.VideoID,
		FolderID: clip.DriveFolder,
		Filename: clip.Title + ".mp4",
		Source:   string(clip.Platform),
		Tags:     clip.Tags,
		Duration: int(clip.Duration.Seconds()),
	}
	if err := m.db.UpsertClip(entry); err != nil {
		return fmt.Errorf("failed to persist clip to StockDB: %w", err)
	}
	logger.Info("Clip added to StockDB",
		zap.String("id", clip.VideoID),
		zap.String("platform", string(clip.Platform)),
		zap.String("title", clip.Title),
	)
	return nil
}

func (m *mainClipDB) UpdateClip(clip *stockjob.ClipRecord) error {
	entry := stockdb.StockClipEntry{
		ClipID:   clip.VideoID,
		FolderID: clip.DriveFolder,
		Filename: clip.Title + ".mp4",
		Source:   string(clip.Platform),
		Tags:     clip.Tags,
		Duration: int(clip.Duration.Seconds()),
	}
	if err := m.db.UpsertClip(entry); err != nil {
		return fmt.Errorf("failed to update clip in StockDB: %w", err)
	}
	logger.Info("Clip updated in StockDB", zap.String("id", clip.VideoID))
	return nil
}

func (m *mainClipDB) GetClip(platform downloader.Platform, videoID string) (*stockjob.ClipRecord, error) {
	entries, err := m.db.GetAllClips()
	if err != nil {
		return nil, err
	}
	for _, c := range entries {
		if c.ClipID == videoID {
			return &stockjob.ClipRecord{
				ID:       c.ClipID,
				Platform: downloader.Platform(c.Source),
				VideoID:  c.ClipID,
				Title:    c.Filename,
				Tags:     c.Tags,
				Duration: time.Duration(c.Duration) * time.Second,
			}, nil
		}
	}
	return nil, nil
}

func (m *mainClipDB) ListMissingClipsWithMetadata(limit int) ([]stockjob.ClipRecord, error) {
	clips, err := m.db.GetAllClips()
	if err != nil {
		return nil, err
	}
	var missing []stockjob.ClipRecord
	for _, c := range clips {
		if len(c.Tags) == 0 || c.Duration == 0 {
			missing = append(missing, stockjob.ClipRecord{
				ID:       c.ClipID,
				Platform: downloader.Platform(c.Source),
				VideoID:  c.ClipID,
				Title:    c.Filename,
				Tags:     c.Tags,
				Duration: time.Duration(c.Duration) * time.Second,
			})
			if len(missing) >= limit {
				break
			}
		}
	}
	return missing, nil
}
