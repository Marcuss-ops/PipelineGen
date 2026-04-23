package clipsearch

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"velox/go-master/internal/clip"
)

type ClipDownloader struct {
	artlistSrc               *clip.ArtlistSource
	ytDlpPath                string
	downloadDir              string
	ytMaxAgeDays             int
	alreadyDownloadedChecker func(*YouTubeClipMetadata) bool
}

func NewClipDownloader(artlistSrc *clip.ArtlistSource, ytDlpPath, downloadDir string) *ClipDownloader {
	maxAgeDays := 30
	if parsed, err := strconv.Atoi(strings.TrimSpace(os.Getenv("VELOX_YOUTUBE_MAX_AGE_DAYS"))); err == nil && parsed > 0 {
		maxAgeDays = parsed
	}
	return &ClipDownloader{
		artlistSrc:   artlistSrc,
		ytDlpPath:    ytDlpPath,
		downloadDir:  downloadDir,
		ytMaxAgeDays: maxAgeDays,
	}
}

func (d *ClipDownloader) SetAlreadyDownloadedChecker(fn func(*YouTubeClipMetadata) bool) {
	d.alreadyDownloadedChecker = fn
}

func (d *ClipDownloader) DownloadClip(ctx context.Context, keyword string) (string, error) {
	filePath, _, err := d.DownloadClipWithMetadata(ctx, keyword)
	return filePath, err
}

func (d *ClipDownloader) DownloadClipWithMetadata(ctx context.Context, keyword string) (string, *YouTubeClipMetadata, error) {
	if d.ytDlpPath == "" {
		return "", nil, fmt.Errorf("yt-dlp not configured")
	}

	outputDir := filepath.Join(d.downloadDir, "dynamic_clips")
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return "", nil, err
	}

	authArgs := ytDLPAuthArgsFromEnv()
	candidates, bestErr := d.selectRankedYouTubeCandidates(ctx, keyword, authArgs)
	allAlreadyDownloaded := true
	var lastMeta *YouTubeClipMetadata
	for _, cand := range candidates {
		if cand == nil {
			continue
		}
		lastMeta = cand
		if d.alreadyDownloadedChecker != nil && d.alreadyDownloadedChecker(cand) {
			continue
		}
		allAlreadyDownloaded = false
		filePath, err := d.downloadYouTubeVideoByURL(ctx, keyword, outputDir, cand.VideoURL, cand.VideoID, authArgs)
		if err != nil {
			continue
		}
		transcript, transcriptPath, segments := d.fetchYouTubeTranscript(ctx, outputDir, cand.VideoURL, cand.VideoID, authArgs)
		cand.Transcript = strings.TrimSpace(transcript)
		cand.TranscriptVTT = strings.TrimSpace(transcriptPath)
		cand.TranscriptSegments = segments
		return filePath, cand, nil
	}
	if len(candidates) > 0 && allAlreadyDownloaded {
		if lastMeta != nil {
			return "", lastMeta, ErrYouTubeAlreadyDownloaded
		}
		return "", nil, ErrYouTubeAlreadyDownloaded
	}

	outputPattern := filepath.Join(outputDir, fmt.Sprintf("dynamic_%s_%%(id)s.%%(ext)s", sanitizeFilename(keyword)))
	var lastErr error
	for _, args := range buildYTDLPSearchArgVariants(keyword, outputPattern, authArgs) {
		cmd := exec.CommandContext(ctx, d.ytDlpPath, args...)
		var stderr bytes.Buffer
		cmd.Stdout = nil
		cmd.Stderr = &stderr
		runErr := cmd.Run()
		if runErr != nil {
			lastErr = fmt.Errorf("yt-dlp args failed: %w (%s)", runErr, strings.TrimSpace(stderr.String()))
		}

		files, err := filepath.Glob(filepath.Join(outputDir, fmt.Sprintf("dynamic_%s_*", sanitizeFilename(keyword))))
		if err == nil && len(files) > 0 {
			// Some yt-dlp flows can return non-zero even when a file was produced.
			fallbackMeta := &YouTubeClipMetadata{
				VideoURL:    "",
				SearchQuery: keyword,
				Relevance:   0,
			}
			return pickVideoCandidate(files), fallbackMeta, nil
		}
		if runErr == nil {
			lastErr = fmt.Errorf("yt-dlp completed but produced no files")
		}
	}
	if bestErr != nil {
		lastErr = fmt.Errorf("%w; fallback err: %v", bestErr, lastErr)
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("yt-dlp search exhausted without result")
	}
	return "", nil, lastErr
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

func (s *Service) downloadFromArtlist(ctx context.Context, keyword string) (string, clip.IndexedClip, error) {
	return s.downloader.DownloadFromArtlist(ctx, keyword)
}

func (s *Service) downloadClip(ctx context.Context, keyword string) (string, *YouTubeClipMetadata, error) {
	return s.downloader.DownloadClipWithMetadata(ctx, keyword)
}
