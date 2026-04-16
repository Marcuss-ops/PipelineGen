package artlistpipeline

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"

	"velox/go-master/internal/artlistdb"
	"velox/go-master/internal/service/scriptdocs"
	"velox/go-master/pkg/logger"

	"go.uber.org/zap"
)

// downloadClipsWithDedup downloads all clips from associations, with deduplication.
func (h *Handler) downloadClipsWithDedup(associations []SentenceAssociation, videoName string, parallel int) []DownloadResult {
	dedup := NewDedupChecker(h.artlistDB)
	downloader := NewDownloader(h.driveClient, h.downloadDir, h.ytDlpPath, h.ffmpegPath)

	var (
		results []DownloadResult
		mu      sync.Mutex
		wg      sync.WaitGroup
		sem     = make(chan struct{}, parallel)
	)

	for _, assoc := range associations {
		for _, clip := range assoc.Clips {
			wg.Add(1)
			sem <- struct{}{}

			go func(assoc SentenceAssociation, clip artlistdb.ArtlistClip) {
				defer wg.Done()
				defer func() { <-sem }()

				// DEDUP 1: Check by ID/URL
				if existing := dedup.CheckExisting(clip.ID, clip.URL); existing != nil {
					mu.Lock()
					results = append(results, DownloadResult{
						SentenceIdx: assoc.SentenceIdx,
						Clip:        *existing,
						DriveFileID: existing.DriveFileID,
						DriveURL:    existing.DriveURL,
						LocalPath:   existing.DownloadPath,
					})
					mu.Unlock()
					return
				}

				// DEDUP 2: Check similar tags
				if similar := dedup.CheckSimilarTags(clip.Tags); similar != nil {
					mu.Lock()
					results = append(results, DownloadResult{
						SentenceIdx: assoc.SentenceIdx,
						Clip:        *similar,
						DriveFileID: similar.DriveFileID,
						DriveURL:    similar.DriveURL,
						LocalPath:   similar.DownloadPath,
					})
					mu.Unlock()
					return
				}

				// Skip if already downloaded locally
				if clip.Downloaded && clip.DownloadPath != "" {
					if _, err := os.Stat(clip.DownloadPath); err == nil {
						mu.Lock()
						results = append(results, DownloadResult{
							SentenceIdx: assoc.SentenceIdx,
							Clip:        clip,
							DriveFileID: clip.DriveFileID,
							DriveURL:    clip.DriveURL,
							LocalPath:   clip.DownloadPath,
						})
						mu.Unlock()
						return
					}
				}

				// Download, convert, upload
				result, err := downloader.ProcessClip(clip, assoc.ArtlistTerm, videoName)
				if err != nil {
					logger.Warn("Failed to download clip",
						zap.String("keyword", assoc.Keyword),
						zap.Error(err))
					mu.Lock()
					results = append(results, DownloadResult{
						SentenceIdx: assoc.SentenceIdx,
						Clip:        clip,
						Err:         err,
					})
					mu.Unlock()
					return
				}

				// Mark as downloaded in DB
				h.artlistDB.MarkClipDownloaded(clip.ID, assoc.ArtlistTerm,
					result.DriveFileID, result.DriveURL, result.LocalPath)
				h.artlistDB.MarkClipUsedInVideo(clip.ID, videoName)

				// Update clip with download info
				clip.Downloaded = true
				clip.DriveFileID = result.DriveFileID
				clip.DriveURL = result.DriveURL
				clip.DownloadPath = result.LocalPath
				clip.LocalPathDrive = fmt.Sprintf("%s/%s", result.DriveFolder, result.Filename)

				mu.Lock()
				results = append(results, DownloadResult{
					SentenceIdx: assoc.SentenceIdx,
					Clip:        clip,
					DriveFileID: result.DriveFileID,
					DriveURL:    result.DriveURL,
					LocalPath:   result.LocalPath,
				})
				mu.Unlock()
			}(assoc, clip)
		}
	}

	wg.Wait()
	return results
}

// downloadSingleClip downloads a single clip with dedup check.
func (h *Handler) downloadSingleClip(clip artlistdb.ArtlistClip, term, videoName string) (*ProcessResult, error) {
	dedup := NewDedupChecker(h.artlistDB)
	downloader := NewDownloader(h.driveClient, h.downloadDir, h.ytDlpPath, h.ffmpegPath)

	// Dedup check
	if existing := dedup.CheckExisting(clip.ID, clip.URL); existing != nil {
		return &ProcessResult{
			DriveFileID: existing.DriveFileID,
			DriveURL:    existing.DriveURL,
			LocalPath:   existing.DownloadPath,
		}, nil
	}

	result, err := downloader.ProcessClip(clip, term, videoName)
	if err != nil {
		return nil, err
	}

	h.artlistDB.MarkClipDownloaded(clip.ID, term, result.DriveFileID, result.DriveURL, result.LocalPath)
	h.artlistDB.MarkClipUsedInVideo(clip.ID, videoName)

	return result, nil
}

// concatenateClips concatenates multiple MP4 files into one.
func (h *Handler) concatenateClips(inputPaths []string, outputName string) (string, error) {
	if len(inputPaths) == 0 {
		return "", fmt.Errorf("no clips to concatenate")
	}

	if len(inputPaths) == 1 {
		outputPath := fmt.Sprintf("%s/%s.mp4", h.outputDir, outputName)
		data, err := os.ReadFile(inputPaths[0])
		if err != nil {
			return "", err
		}
		return outputPath, os.WriteFile(outputPath, data, 0644)
	}

	outputPath := fmt.Sprintf("%s/%s.mp4", h.outputDir, outputName)
	listPath := outputPath + ".txt"

	var listContent strings.Builder
	for _, p := range inputPaths {
		listContent.WriteString(fmt.Sprintf("file '%s'\n", p))
	}
	if err := os.WriteFile(listPath, []byte(listContent.String()), 0644); err != nil {
		return "", err
	}

	cmd := exec.Command(h.ffmpegPath,
		"-y", "-f", "concat", "-safe", "0",
		"-i", listPath, "-c", "copy", outputPath)
	if err := cmd.Run(); err != nil {
		os.Remove(listPath)
		return "", fmt.Errorf("ffmpeg concat failed: %w", err)
	}

	os.Remove(listPath)
	return outputPath, nil
}

// uploadFinalVideo uploads the final video to Drive.
func (h *Handler) uploadFinalVideo(localPath, filename string) (string, string) {
	if h.driveClient == nil {
		return "", ""
	}

	ctx := context.Background()
	driveID, err := h.driveClient.UploadFile(ctx, localPath, "", filename)
	if err != nil {
		logger.Warn("Failed to upload final video", zap.Error(err))
		return "", ""
	}

	return driveID, fmt.Sprintf("https://drive.google.com/file/d/%s/view", driveID)
}

// buildTimestamps builds timestamp mapping for each clip.
func buildTimestamps(results []DownloadResult) []TimestampEntry {
	var timestamps []TimestampEntry
	currentTime := 0.0

	for _, r := range results {
		startTime := currentTime
		duration := 7.0
		endTime := startTime + duration

		timestamps = append(timestamps, TimestampEntry{
			SentenceIdx: r.SentenceIdx,
			StartTime:   fmt.Sprintf("%.1f", startTime),
			EndTime:     fmt.Sprintf("%.1f", endTime),
			ClipID:      r.Clip.ID,
			DriveURL:    r.DriveURL,
		})

		currentTime = endTime
	}

	return timestamps
}

// extractSentences extracts sentences from text using scriptdocs.
func extractSentences(text string) []string {
	return scriptdocs.ExtractSentences(text)
}

// extractPaths extracts local file paths from download results.
func extractPaths(results []DownloadResult) []string {
	var paths []string
	for _, r := range results {
		paths = append(paths, r.LocalPath)
	}
	return paths
}

// sanitizeFilename removes special chars from a string.
func sanitizeFilename(s string) string {
	s = strings.ReplaceAll(s, " ", "_")
	s = strings.ReplaceAll(s, "/", "_")
	s = strings.ReplaceAll(s, "\\", "_")
	s = strings.ReplaceAll(s, ":", "_")
	return strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-' {
			return r
		}
		return -1
	}, s)
}

// capitalize converts the first letter to uppercase, rest lowercase.
func capitalize(s string) string {
	if len(s) == 0 {
		return s
	}
	s = strings.ToLower(s)
	return strings.ToUpper(s[:1]) + s[1:]
}
