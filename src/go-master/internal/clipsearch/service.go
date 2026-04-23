// Package clipsearch dynamically searches, downloads, and uploads video clips for keywords.
package clipsearch

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"velox/go-master/internal/artlistdb"
	"velox/go-master/internal/clip"
	"velox/go-master/internal/ml/ollama"
	"velox/go-master/internal/stockdb"
	"velox/go-master/internal/upload/drive"
	"velox/go-master/pkg/logger"

	"go.uber.org/zap"
)

func (s *Service) GetIndexer() *clip.Indexer {
	return s.indexer
}

func (s *Service) SetIndexer(i *clip.Indexer) {
	s.indexer = i
}

func (s *Service) SetArtlistSource(src *clip.ArtlistSource) {
	s.artlistSrc = src
	if s.downloader != nil {
		s.downloader.artlistSrc = src
	}
}

func (s *Service) SetOllamaClient(c *ollama.Client) {
	s.ollama = c
}

func (s *Service) SetUploadFolderID(folderID string) {
	if s.uploader != nil {
		s.uploader.SetUploadFolderID(folderID)
	}
}

func (s *Service) SetPostCycleSync(fn func(context.Context) error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.postCycleSync = fn
}

func New(driveClient *drive.Client, stockDB *stockdb.StockDB, artlistDB *artlistdb.ArtlistDB, downloadDir, ytDlpPath string) *Service {
	processor := NewClipProcessor("ffmpeg", "ffprobe")
	downloader := NewClipDownloader(nil, ytDlpPath, downloadDir)
	uploader := NewDriveUploader(driveClient, "")
	persister := NewClipPersister(stockDB, artlistDB)
	finder := NewClipFinder(stockDB, artlistDB)

	svc := &Service{
		downloader:      downloader,
		uploader:        uploader,
		persister:       persister,
		finder:          finder,
		processor:       processor,
		downloadDir:     downloadDir,
		ytDlpPath:       ytDlpPath,
		ffmpegPath:      "ffmpeg",
		keywordFailures: make(map[string]int),
		keywordBlocked:  make(map[string]time.Time),
		workerSemaphore: make(chan struct{}, maxParallelDownloads),
	}
	downloader.SetAlreadyDownloadedChecker(func(meta *YouTubeClipMetadata) bool {
		return svc.finder != nil && svc.finder.FindDownloadedYouTubeByMeta(meta) != nil
	})
	defaultCheckpoint := filepath.Join(downloadDir, "clipsearch_checkpoints.json")
	if err := svc.SetCheckpointStorePath(defaultCheckpoint); err != nil {
		logger.Warn("Failed to initialize default clipsearch checkpoint store",
			zap.String("path", defaultCheckpoint),
			zap.Error(err),
		)
	}
	return svc
}

func (s *Service) SetCheckpointStorePath(path string) error {
	store, err := OpenClipJobCheckpointStore(path)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.checkpoints = store
	s.mu.Unlock()
	return nil
}

func (s *Service) runPostCycleSync(ctx context.Context, newUploads int) {
	s.mu.Lock()
	syncFn := s.postCycleSync
	s.mu.Unlock()

	if syncFn == nil {
		return
	}
	if err := syncFn(ctx); err != nil {
		logger.Warn("Post-cycle DB sync failed",
			zap.Int("new_uploads", newUploads),
			zap.Error(err),
		)
		return
	}
	logger.Info("Post-cycle DB sync completed",
		zap.Int("new_uploads", newUploads),
	)
}

func (s *Service) isKeywordBlocked(keyword string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	until, ok := s.keywordBlocked[keyword]
	if !ok {
		return false
	}
	if time.Now().After(until) {
		delete(s.keywordBlocked, keyword)
		delete(s.keywordFailures, keyword)
		return false
	}
	return true
}

func (s *Service) recordKeywordFailure(keyword string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.keywordFailures[keyword]++
	if s.keywordFailures[keyword] >= keywordFailThreshold {
		s.keywordBlocked[keyword] = time.Now().Add(keywordBlockDuration)
	}
}

func (s *Service) resetKeywordFailures(keyword string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.keywordFailures, keyword)
	delete(s.keywordBlocked, keyword)
}

func (s *Service) ensureKeywordJobCheckpoint(keyword string) string {
	s.mu.Lock()
	store := s.checkpoints
	s.mu.Unlock()
	if store == nil {
		return ""
	}

	if existing, ok := store.GetLatestByKeyword(keyword); ok && !existing.IsTerminal() {
		existing.Attempts++
		existing.Status = ClipJobStatusQueued
		existing.LastError = ""
		existing.UpdatedAt = time.Now().UTC()
		_ = store.SaveOrUpdate(existing)
		return existing.JobID
	}

	jobID := fmt.Sprintf("clip_%d_%s", time.Now().UTC().UnixNano(), sanitizeFilename(keyword))
	checkpoint := ClipJobCheckpoint{
		JobID:     jobID,
		Keyword:   strings.TrimSpace(keyword),
		Status:    ClipJobStatusQueued,
		Attempts:  1,
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	_ = store.SaveOrUpdate(checkpoint)
	return jobID
}

func (s *Service) markCheckpoint(jobID string, status ClipJobStatus, errMsg string, result *SearchResult) {
	if strings.TrimSpace(jobID) == "" {
		return
	}
	s.mu.Lock()
	store := s.checkpoints
	s.mu.Unlock()
	if store == nil {
		return
	}
	if err := store.Transition(jobID, status, errMsg, result); err != nil {
		logger.Debug("Failed to update clipsearch checkpoint",
			zap.String("job_id", jobID),
			zap.String("status", string(status)),
			zap.Error(err),
		)
	}
	if s.persister != nil {
		keyword := ""
		if checkpoint, ok := store.Get(jobID); ok {
			keyword = checkpoint.Keyword
		}
		mappedStatus := "processing"
		switch status {
		case ClipJobStatusQueued:
			mappedStatus = "queued"
		case ClipJobStatusSearched, ClipJobStatusDownloaded, ClipJobStatusProcessed:
			mappedStatus = "processing"
		case ClipJobStatusUploaded, ClipJobStatusDone:
			mappedStatus = "uploaded"
		case ClipJobStatusFailed:
			mappedStatus = "failed"
		}
		_ = s.persister.SaveJobStatus(keyword, "job_"+jobID, mappedStatus, errMsg)
	}
}
