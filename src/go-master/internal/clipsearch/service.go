// Package clipsearch dynamically searches, downloads, and uploads video clips for keywords.
package clipsearch

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"velox/go-master/internal/artlistdb"
	"velox/go-master/internal/clip"
	"velox/go-master/internal/ml/ollama"
	"velox/go-master/internal/stockdb"
	"velox/go-master/internal/upload/drive"
	"velox/go-master/pkg/logger"

	"go.uber.org/zap"
)

type Service struct {
	downloader *ClipDownloader
	uploader   *DriveUploader
	persister  *ClipPersister
	finder     *ClipFinder
	processor  *ClipProcessor

	artlistSrc *clip.ArtlistSource
	indexer    *clip.Indexer
	ollama     *ollama.Client

	ytDlpPath  string
	ffmpegPath string

	downloadDir string

	postCycleSync func(context.Context) error
	mu            sync.Mutex

	keywordFailures map[string]int
	keywordBlocked  map[string]time.Time
	checkpoints     *ClipJobCheckpointStore
	uploadMu        sync.Mutex

	// Concurrency control
	workerSemaphore chan struct{}
}

const (
	defaultPerKeywordTimeout = 90 * time.Second // Increased for parallel load
	keywordFailThreshold     = 3
	keywordBlockDuration     = 10 * time.Minute
	maxParallelDownloads     = 5
)

type SearchOptions struct {
	ForceFresh         bool
	MaxClipsPerKeyword int
}

type SearchResult struct {
	Keyword           string   `json:"keyword"`
	ClipID            string   `json:"clip_id"`
	Filename          string   `json:"filename"`
	Source            string   `json:"source,omitempty"`
	DriveURL          string   `json:"drive_url"`
	DriveID           string   `json:"drive_id"`
	Folder            string   `json:"folder"`
	FolderID          string   `json:"folder_id,omitempty"`
	Description       string   `json:"description,omitempty"`
	Tags              []string `json:"tags,omitempty"`
	StartSec          float64  `json:"start_sec,omitempty"`
	EndSec            float64  `json:"end_sec,omitempty"`
	Score             float64  `json:"score,omitempty"`
	TranscriptSnippet string   `json:"transcript_snippet,omitempty"`
	ThumbnailURL      string   `json:"thumbnail_url,omitempty"`
	TextDriveURL      string   `json:"text_drive_url,omitempty"`
	TextDriveID       string   `json:"text_drive_id,omitempty"`
}

type DriveUploadResult struct {
	DriveID    string
	Filename   string
	DriveURL   string
	FolderID   string
	FolderName string
	FolderPath string
	TextFileID string
	TextURL    string
	TextName   string
}

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

func (s *Service) RankedYouTubeCandidates(ctx context.Context, keyword string) ([]*YouTubeClipMetadata, error) {
	if s.downloader == nil {
		return nil, fmt.Errorf("clip downloader not available")
	}
	return s.downloader.selectRankedYouTubeCandidates(ctx, keyword, ytDLPAuthArgsFromEnv())
}

func (s *Service) DownloadYouTubeCandidate(ctx context.Context, keyword string, candidate *YouTubeClipMetadata) (string, *YouTubeClipMetadata, error) {
	if s.downloader == nil {
		return "", nil, fmt.Errorf("clip downloader not available")
	}
	if candidate == nil {
		return "", nil, fmt.Errorf("candidate cannot be nil")
	}
	if strings.TrimSpace(candidate.VideoURL) == "" {
		return "", nil, fmt.Errorf("candidate url cannot be empty")
	}
	outputDir := filepath.Join(s.downloadDir, "jit_stock")
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return "", nil, err
	}
	rawPath, err := s.downloader.downloadYouTubeVideoByURL(ctx, keyword, outputDir, candidate.VideoURL, candidate.VideoID, ytDLPAuthArgsFromEnv())
	if err != nil {
		return "", nil, err
	}
	meta := *candidate
	transcript, transcriptPath, segments := s.downloader.fetchYouTubeTranscript(ctx, outputDir, candidate.VideoURL, candidate.VideoID, ytDLPAuthArgsFromEnv())
	meta.Transcript = transcript
	meta.TranscriptVTT = transcriptPath
	meta.TranscriptSegments = segments
	return rawPath, &meta, nil
}

func (s *Service) ProcessDownloadedYouTubeMomentsToFolder(ctx context.Context, keyword, rawPath string, baseMeta *YouTubeClipMetadata, folderID string) ([]SearchResult, int, error) {
	if s.downloader == nil || s.uploader == nil {
		return nil, 0, fmt.Errorf("clipsearch service not fully initialized")
	}
	s.uploadMu.Lock()
	defer s.uploadMu.Unlock()

	prevFolder := s.uploader.uploadFolderID
	s.uploader.SetUploadFolderID(folderID)
	defer s.uploader.SetUploadFolderID(prevFolder)

	return s.processYouTubeMomentsFromDownloaded(ctx, keyword, rawPath, baseMeta)
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

func (s *Service) SearchClips(ctx context.Context, keywords []string) ([]SearchResult, error) {
	return s.SearchClipsWithOptions(ctx, keywords, SearchOptions{})
}

func (s *Service) SearchClipsWithOptions(ctx context.Context, keywords []string, opts SearchOptions) ([]SearchResult, error) {
	normalizedKeywords := normalizeKeywords(keywords)
	results, newUploads := s.processKeywords(ctx, normalizedKeywords, opts)

	if newUploads > 0 {
		s.runPostCycleSync(ctx, newUploads)
	}

	return results, nil
}

func (s *Service) processKeywords(ctx context.Context, keywords []string, opts SearchOptions) ([]SearchResult, int) {
	if opts.MaxClipsPerKeyword > 1 {
		return s.processKeywordsMulti(ctx, keywords, opts)
	}

	newUploads := int32(0)
	g, gCtx := errgroup.WithContext(ctx)

	var mu sync.Mutex
	finalResults := make([]SearchResult, 0, len(keywords))

	for _, kw := range keywords {
		kw := kw // capture
		g.Go(func() error {
			// Acquire semaphore
			select {
			case s.workerSemaphore <- struct{}{}:
				defer func() { <-s.workerSemaphore }()
			case <-gCtx.Done():
				return gCtx.Err()
			}

			jobID := s.ensureKeywordJobCheckpoint(kw)
			res, uploaded, found := s.processKeyword(gCtx, kw, opts, jobID)
			if found {
				mu.Lock()
				finalResults = append(finalResults, res)
				if uploaded {
					newUploads++
				}
				mu.Unlock()
			}
			return nil
		})
	}

	_ = g.Wait()
	return finalResults, int(newUploads)
}

func (s *Service) processKeyword(ctx context.Context, kw string, opts SearchOptions, jobID string) (SearchResult, bool, bool) {
	s.markCheckpoint(jobID, ClipJobStatusSearched, "", nil)

	if !opts.ForceFresh {
		existing, err := s.finder.FindClipInDB(kw)
		if err == nil && existing != nil {
			logger.Info("Found clip in DB cache",
				zap.String("keyword", kw),
				zap.String("clip_id", existing.ClipID),
			)
			s.markCheckpoint(jobID, ClipJobStatusDone, "", existing)
			return *existing, false, true
		}
	}

	if shouldPreferYouTubeKeyword(kw) {
		if result, uploaded, found := s.processYTDLPKeyword(ctx, kw, jobID); found {
			return result, uploaded, true
		}
		if result, uploaded, found := s.processArtlistKeyword(ctx, kw, jobID); found {
			return result, uploaded, true
		}
	} else {
		if result, uploaded, found := s.processArtlistKeyword(ctx, kw, jobID); found {
			return result, uploaded, true
		}
		if result, uploaded, found := s.processYTDLPKeyword(ctx, kw, jobID); found {
			return result, uploaded, true
		}
	}
	return SearchResult{}, false, false
}

func (s *Service) processArtlistKeyword(ctx context.Context, kw string, jobID string) (SearchResult, bool, bool) {
	artlistPath, artlistClip, err := s.downloadFromArtlist(ctx, kw)
	if err != nil {
		logger.Warn("Artlist direct download failed, falling back to yt-dlp search",
			zap.String("keyword", kw),
			zap.Error(err),
		)
		return SearchResult{}, false, false
	}
	s.markCheckpoint(jobID, ClipJobStatusDownloaded, "", nil)
	defer os.Remove(artlistPath)

	// Deduplicate by source clip identity + keyword: skip new upload when already present.
	if existing := s.finder.FindDownloadedArtlistBySource(kw, artlistClip); existing != nil {
		logger.Info("Skipping upload for already downloaded Artlist source clip",
			zap.String("keyword", kw),
			zap.String("clip_id", artlistClip.ID),
			zap.String("drive_id", existing.DriveID),
		)
		return *existing, false, true
	}

	normalizedPath, normErr := s.processor.NormalizeClipToSevenSeconds1080p(ctx, artlistPath, artlistClip.Duration)
	if normErr != nil {
		logger.Warn("Artlist clip normalization failed, falling back to yt-dlp search",
			zap.String("keyword", kw),
			zap.String("clip_id", artlistClip.ID),
			zap.Error(normErr),
		)
		return SearchResult{}, false, false
	}
	s.markCheckpoint(jobID, ClipJobStatusProcessed, "", nil)
	defer os.Remove(normalizedPath)

	visualHash, hashErr := s.processor.ComputeVisualHash(ctx, normalizedPath)
	if hashErr != nil {
		logger.Warn("Failed to compute clip visual hash",
			zap.String("keyword", kw),
			zap.String("clip_id", artlistClip.ID),
			zap.Error(hashErr),
		)
	}

	if existing := s.finder.FindDownloadedArtlistByVisualAndTitle(kw, visualHash, artlistClip.Name); existing != nil {
		logger.Info("Skipping upload for visually duplicated Artlist clip",
			zap.String("keyword", kw),
			zap.String("clip_id", artlistClip.ID),
			zap.String("drive_id", existing.DriveID),
		)
		s.markCheckpoint(jobID, ClipJobStatusDone, "", existing)
		return *existing, false, true
	}

	driveResult, upErr := s.uploader.UploadToDrive(ctx, normalizedPath, kw)
	if upErr != nil {
		logger.Warn("Artlist clip upload failed, falling back to yt-dlp search",
			zap.String("keyword", kw),
			zap.String("clip_id", artlistClip.ID),
			zap.Error(upErr),
		)
		return SearchResult{}, false, false
	}
	res := searchResultFromDrive(kw, driveResult)
	s.uploadClipSidecarText(ctx, kw, driveResult, buildArtlistClipSidecarText(kw, artlistClip))
	res.TextDriveID = driveResult.TextFileID
	res.TextDriveURL = driveResult.TextURL
	s.markCheckpoint(jobID, ClipJobStatusUploaded, "", &res)

	s.persister.PersistClipMetadata(kw, driveResult, normalizedPath, &artlistClip, visualHash, nil)

	logger.Info("Dynamic Artlist clip processed and registered",
		zap.String("keyword", kw),
		zap.String("clip_id", artlistClip.ID),
		zap.String("drive_url", driveResult.DriveURL),
	)
	return res, true, true
}

func (s *Service) processYTDLPKeyword(ctx context.Context, kw string, jobID string) (SearchResult, bool, bool) {
	downloadedPath, ytMeta, err := s.downloadClip(ctx, kw)
	if err != nil {
		if errors.Is(err, ErrYouTubeAlreadyDownloaded) {
			if existing := s.finder.FindDownloadedYouTubeByMeta(ytMeta); existing != nil {
				logger.Info("Skipping download for already downloaded YouTube interview hash",
					zap.String("keyword", kw),
					zap.String("video_id", strings.TrimSpace(ytMeta.VideoID)),
					zap.String("drive_id", existing.DriveID),
				)
				s.markCheckpoint(jobID, ClipJobStatusDone, "", existing)
				return *existing, false, true
			}
		}
		logger.Warn("Failed to download clip for keyword",
			zap.String("keyword", kw),
			zap.Error(err),
		)
		return SearchResult{}, false, false
	}
	s.markCheckpoint(jobID, ClipJobStatusDownloaded, "", nil)
	defer os.Remove(downloadedPath)

	s.markCheckpoint(jobID, ClipJobStatusProcessed, "", nil)
	results, uploads, procErr := s.processYouTubeMomentsFromDownloaded(ctx, kw, downloadedPath, ytMeta)
	if procErr != nil || len(results) == 0 {
		logger.Warn("Failed to process yt-dlp moments",
			zap.String("keyword", kw),
			zap.Error(procErr),
		)
		return SearchResult{}, false, false
	}
	s.markCheckpoint(jobID, ClipJobStatusUploaded, "", &results[0])
	return results[0], uploads > 0, true
}

func (s *Service) downloadFromArtlist(ctx context.Context, keyword string) (string, clip.IndexedClip, error) {
	return s.downloader.DownloadFromArtlist(ctx, keyword)
}

func (s *Service) downloadClip(ctx context.Context, keyword string) (string, *YouTubeClipMetadata, error) {
	return s.downloader.DownloadClipWithMetadata(ctx, keyword)
}

func searchResultFromDrive(kw string, driveResult *DriveUploadResult) SearchResult {
	folder := driveResult.FolderPath
	if folder == "" {
		folder = "Stock/Artlist/" + kw
	}
	return SearchResult{
		Keyword:      kw,
		ClipID:       driveResult.DriveID,
		Filename:     driveResult.Filename,
		DriveURL:     driveResult.DriveURL,
		DriveID:      driveResult.DriveID,
		Folder:       folder,
		FolderID:     driveResult.FolderID,
		TextDriveID:  driveResult.TextFileID,
		TextDriveURL: driveResult.TextURL,
	}
}

func (s *Service) uploadClipSidecarText(ctx context.Context, keyword string, driveResult *DriveUploadResult, content string) {
	// Default behavior: avoid per-clip txt explosion in Drive.
	// Enable only if explicitly requested.
	if strings.ToLower(strings.TrimSpace(os.Getenv("VELOX_ENABLE_PER_CLIP_TXT"))) != "true" {
		return
	}
	if s.uploader == nil || driveResult == nil || strings.TrimSpace(content) == "" {
		return
	}
	res, err := s.uploader.UploadTextSidecar(ctx, driveResult.FolderID, driveResult.Filename, keyword, content)
	if err != nil {
		logger.Warn("Failed to upload clip sidecar text",
			zap.String("keyword", keyword),
			zap.String("drive_id", driveResult.DriveID),
			zap.Error(err),
		)
		return
	}
	driveResult.TextFileID = res.DriveID
	driveResult.TextURL = res.DriveURL
	driveResult.TextName = res.Filename
}

func buildArtlistClipSidecarText(keyword string, c clip.IndexedClip) string {
	var b strings.Builder
	b.WriteString("keyword: " + strings.TrimSpace(keyword) + "\n")
	b.WriteString("source: artlist\n")
	if strings.TrimSpace(c.ID) != "" {
		b.WriteString("clip_id: " + strings.TrimSpace(c.ID) + "\n")
	}
	if strings.TrimSpace(c.Name) != "" {
		b.WriteString("title: " + strings.TrimSpace(c.Name) + "\n")
	}
	if strings.TrimSpace(c.DownloadLink) != "" {
		b.WriteString("source_url: " + strings.TrimSpace(c.DownloadLink) + "\n")
	} else if strings.TrimSpace(c.DriveLink) != "" {
		b.WriteString("source_url: " + strings.TrimSpace(c.DriveLink) + "\n")
	}
	if len(c.Tags) > 0 {
		b.WriteString("tags: " + strings.Join(c.Tags, ", ") + "\n")
	}
	b.WriteString("\ntranscript:\n")
	b.WriteString("Not available for Artlist source in current pipeline.\n")
	return b.String()
}

func buildYouTubeClipSidecarText(keyword string, m *YouTubeClipMetadata) string {
	var b strings.Builder
	b.WriteString("keyword: " + strings.TrimSpace(keyword) + "\n")
	b.WriteString("source: youtube\n")
	if m == nil {
		b.WriteString("note: metadata unavailable (fallback download path)\n")
		return b.String()
	}
	if strings.TrimSpace(m.VideoID) != "" {
		b.WriteString("video_id: " + strings.TrimSpace(m.VideoID) + "\n")
	}
	if strings.TrimSpace(m.VideoURL) != "" {
		b.WriteString("video_url: " + strings.TrimSpace(m.VideoURL) + "\n")
	}
	if strings.TrimSpace(m.Title) != "" {
		b.WriteString("title: " + strings.TrimSpace(m.Title) + "\n")
	}
	if strings.TrimSpace(m.Channel) != "" {
		b.WriteString("channel: " + strings.TrimSpace(m.Channel) + "\n")
	}
	if strings.TrimSpace(m.Uploader) != "" {
		b.WriteString("uploader: " + strings.TrimSpace(m.Uploader) + "\n")
	}
	if m.ViewCount > 0 {
		b.WriteString(fmt.Sprintf("views: %d\n", m.ViewCount))
	}
	if m.DurationSec > 0 {
		b.WriteString(fmt.Sprintf("duration_sec: %.1f\n", m.DurationSec))
	}
	if strings.TrimSpace(m.UploadDate) != "" {
		b.WriteString("upload_date: " + strings.TrimSpace(m.UploadDate) + "\n")
	}
	if strings.TrimSpace(m.SearchQuery) != "" {
		b.WriteString("search_query: " + strings.TrimSpace(m.SearchQuery) + "\n")
	}
	if m.Relevance != 0 {
		b.WriteString(fmt.Sprintf("relevance_score: %d\n", m.Relevance))
	}
	if m.SelectedMoment != nil {
		b.WriteString(fmt.Sprintf("selected_moment_start_sec: %.1f\n", m.SelectedMoment.StartSec))
		b.WriteString(fmt.Sprintf("selected_moment_end_sec: %.1f\n", m.SelectedMoment.EndSec))
		if strings.TrimSpace(m.SelectedMoment.Reason) != "" {
			b.WriteString("selected_moment_reason: " + strings.TrimSpace(m.SelectedMoment.Reason) + "\n")
		}
		if strings.TrimSpace(m.SelectedMoment.Source) != "" {
			b.WriteString("selected_moment_source: " + strings.TrimSpace(m.SelectedMoment.Source) + "\n")
		}
	}
	if hash := buildYouTubeInterviewHash(m); hash != "" {
		b.WriteString("interview_hash: " + hash + "\n")
	}
	if strings.TrimSpace(m.Description) != "" {
		b.WriteString("\ndescription:\n")
		b.WriteString(strings.TrimSpace(m.Description) + "\n")
	}
	b.WriteString("\ntranscript:\n")
	if strings.TrimSpace(m.Transcript) != "" {
		b.WriteString(strings.TrimSpace(m.Transcript) + "\n")
	} else {
		b.WriteString("Subtitles/transcript not available from source.\n")
	}
	return b.String()
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
