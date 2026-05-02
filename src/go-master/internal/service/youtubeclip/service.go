package youtubeclip

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"go.uber.org/zap"
	driveapi "google.golang.org/api/drive/v3"

	"velox/go-master/internal/repository/clips"
	"velox/go-master/internal/service/drivedestination"
	"velox/go-master/internal/upload/drive"
	"velox/go-master/pkg/config"
	"velox/go-master/pkg/hashutil"
	"velox/go-master/pkg/media/downloader"
	"velox/go-master/pkg/media/ffmpeg"
	"velox/go-master/pkg/pathutil"
	"velox/go-master/pkg/security"
	"velox/go-master/pkg/models"
)

type Service struct {
	cfg              *config.Config
	log              *zap.Logger
	clipsRepo        *clips.Repository
	driveClient      *driveapi.Service
	driveDestination *drivedestination.Service
	ffmpeg           *ffmpeg.Processor
}

func NewService(
	cfg *config.Config,
	log *zap.Logger,
	clipsRepo *clips.Repository,
	driveClient *driveapi.Service,
	driveDestination *drivedestination.Service,
	ffmpegProc *ffmpeg.Processor,
) *Service {
	return &Service{
		cfg:              cfg,
		log:              log,
		clipsRepo:        clipsRepo,
		driveClient:      driveClient,
		driveDestination: driveDestination,
		ffmpeg:           ffmpegProc,
	}
}

func (s *Service) Extract(ctx context.Context, req *ExtractRequest) (*ExtractResponse, error) {
	s.log.Info("YouTube Extract service called", zap.String("url", req.URL))
	resp := &ExtractResponse{
		OK:        true,
		SourceURL: strings.TrimSpace(req.URL),
	}

	if resp.SourceURL == "" {
		resp.OK = false
		resp.Error = "url is required"
		return resp, fmt.Errorf("url is required")
	}

	if err := security.ValidateDownloadURL(resp.SourceURL); err != nil {
		resp.OK = false
		resp.Error = err.Error()
		return resp, err
	}

	if len(req.Segments) == 0 {
		resp.OK = false
		resp.Error = "segments are required"
		return resp, fmt.Errorf("segments are required")
	}

	if len(req.Segments) > 20 {
		resp.OK = false
		resp.Error = "too many segments, max 20"
		return resp, fmt.Errorf("too many segments")
	}

	dl := downloader.NewYTDLP(s.cfg)

	outDir := filepath.Join(s.cfg.Storage.DataDir, "youtube-clips", time.Now().UTC().Format("20060102_150405"))
	if err := os.MkdirAll(outDir, 0755); err != nil {
		resp.OK = false
		resp.Error = err.Error()
		return resp, err
	}

	// Resolve Drive destination if drivedestination service is available
	var driveFolderID string
	var resolvedPath string
	if s.driveDestination != nil && req.Destination != nil {
		destReq := &drivedestination.Request{
			Group:           req.Destination.Group,
			FolderID:        req.Destination.FolderID,
			FolderPath:      req.Destination.FolderPath,
			SubfolderName:   req.Destination.SubfolderName,
			CreateSubfolder: req.Destination.CreateSubfolder,
		}

		// If no subfolder provided, automatically create one based on the video ID
		// to avoid "floating clips" in the main group folder.
		if destReq.SubfolderName == "" {
			videoID := extractVideoID(resp.SourceURL)
			if videoID != "" {
				destReq.SubfolderName = "yt_" + videoID
				destReq.CreateSubfolder = true
				s.log.Info("auto-assigning video subfolder", zap.String("subfolder", destReq.SubfolderName))
			}
		}

		resolved, err := s.driveDestination.Resolve(ctx, destReq)
		if err != nil {
			s.log.Warn("failed to resolve drive destination", zap.Error(err))
		} else {
			driveFolderID = resolved.FolderID
			resolvedPath = resolved.FolderPath
		}
	}
	// Set folder info on response
	resp.DriveFolderID = driveFolderID
	resp.DriveFolderPath = resolvedPath

	for i, seg := range req.Segments {
		item := ExtractItem{
			Name:           pathutil.Slug(seg.Name),
			Start:          strings.TrimSpace(seg.Start),
			End:            strings.TrimSpace(seg.End),
			DriveFolderID:  driveFolderID,
			DriveFolderPath: resolvedPath,
		}

		if item.Name == "" {
			item.Name = fmt.Sprintf("segment_%03d", i+1)
		}

		if err := security.SanitizeTimestamp(item.Start); err != nil {
			item.Status = "failed"
			item.Error = "invalid start timestamp: " + err.Error()
			resp.Items = append(resp.Items, item)
			resp.OK = false
			continue
		}

		if err := security.SanitizeTimestamp(item.End); err != nil {
			item.Status = "failed"
			item.Error = "invalid end timestamp: " + err.Error()
			resp.Items = append(resp.Items, item)
			resp.OK = false
			continue
		}

		// Validate start < end and check max duration
		startSec, err := parseTimestamp(item.Start)
		if err != nil {
			item.Status = "failed"
			item.Error = "invalid start timestamp format: " + err.Error()
			resp.Items = append(resp.Items, item)
			resp.OK = false
			continue
		}
		endSec, err := parseTimestamp(item.End)
		if err != nil {
			item.Status = "failed"
			item.Error = "invalid end timestamp format: " + err.Error()
			resp.Items = append(resp.Items, item)
			resp.OK = false
			continue
		}
		if startSec >= endSec {
			item.Status = "failed"
			item.Error = fmt.Sprintf("start time (%s) must be before end time (%s)", item.Start, item.End)
			resp.Items = append(resp.Items, item)
			resp.OK = false
			continue
		}
		duration := endSec - startSec
		if duration > MaxSegmentDuration {
			item.Status = "failed"
			item.Error = fmt.Sprintf("segment duration (%d seconds) exceeds maximum allowed (%d seconds)", duration, MaxSegmentDuration)
			resp.Items = append(resp.Items, item)
			resp.OK = false
			continue
		}

		// Extract video ID for stable clip ID
		videoID := extractVideoID(resp.SourceURL)

		// Create stable ID: yt_videoID_start_end (sanitized)
		safeStart := strings.ReplaceAll(item.Start, ":", "")
		safeEnd := strings.ReplaceAll(item.End, ":", "")
		clipID := fmt.Sprintf("yt_%s_%s_%s", videoID, safeStart, safeEnd)

		// If we can't get video ID, fallback to timestamp
		if videoID == "" {
			clipID = fmt.Sprintf("yt_%s_%03d", time.Now().UTC().Format("20060102_150405"), i+1)
		}

		// Check if clip already exists (deduplication)
		if s.clipsRepo != nil && req.SaveDB {
			existingClip, clipErr := s.clipsRepo.GetClip(ctx, clipID)
			if clipErr == nil && existingClip != nil {
				// Check if existing clip has valid local file
				if existingClip.LocalPath != "" {
					if _, statErr := os.Stat(existingClip.LocalPath); statErr == nil {
						s.log.Info("clip already exists with valid local file, skipping processing",
							zap.String("clip_id", clipID),
							zap.String("local_path", existingClip.LocalPath),
						)
						item.LocalPath = existingClip.LocalPath
						item.Status = "skipped_existing"
						item.DriveLink = existingClip.DriveLink

						// Still add to response items
						resp.Items = append(resp.Items, item)
						continue
					}
				}
			}
		}

		outputTemplate := filepath.Join(outDir, fmt.Sprintf("%03d_%s", i+1, item.Name))
		section := fmt.Sprintf("*%s-%s", item.Start, item.End)

		var dlErr error
		dlErr = dl.Download(ctx, &downloader.DownloadRequest{
			URL:             resp.SourceURL,
			OutputPath:      outputTemplate,
			Format:          "bv*[height<=1080][ext=mp4]+ba[ext=m4a]/b[height<=1080][ext=mp4]/best[height<=1080]",
			MergeFormat:     "mp4",
			NoPlaylist:      true,
			DownloadSections: []string{section},
			ForceKeyframes:  req.ForceKeyframes,
			Timeout:         10 * time.Minute,
		})

		if dlErr != nil {
			item.Status = "failed"
			item.Error = fmt.Sprintf("yt-dlp failed: %v", dlErr)
			resp.Items = append(resp.Items, item)
			resp.OK = false
			continue
		}

		localPath := findFirstOutput(outDir, fmt.Sprintf("%03d_%s", i+1, item.Name))
		item.LocalPath = localPath
		item.Status = "processed"

		if localPath == "" {
			item.Status = "failed"
			item.Error = "output file not found after yt-dlp"
			resp.Items = append(resp.Items, item)
			resp.OK = false
			continue
		}

		// Normalize video with FFmpeg if processor is available and requested
		shouldNormalize := req.Normalize == nil || *req.Normalize
		if s.ffmpeg != nil && shouldNormalize {
			normalizedPath := localPath + ".normalized.mp4"
			opts := ffmpeg.DefaultNormalizeOptions(s.cfg)
			opts.KeepAudio = req.KeepAudio
			opts.DisableDuration = true // Don't truncate YouTube clips to the global default duration

			s.log.Info("normalizing video",
				zap.String("input", localPath),
				zap.String("output", normalizedPath),
				zap.Int("width", opts.Width),
				zap.Int("height", opts.Height),
				zap.Bool("keep_audio", opts.KeepAudio),
			)
			ffmpegErr := s.ffmpeg.Normalize(ctx, localPath, normalizedPath, opts)
			if ffmpegErr != nil {
				s.log.Warn("failed to normalize video, using original", zap.Error(ffmpegErr))
			} else {
				// Replace original with normalized version
				if err := os.Remove(localPath); err != nil {
					s.log.Warn("failed to remove original file", zap.Error(err))
				}
				if err := os.Rename(normalizedPath, localPath); err != nil {
					s.log.Warn("failed to rename normalized file", zap.Error(err))
					item.LocalPath = normalizedPath
					localPath = normalizedPath
				}
			}
		}

		// Calculate file hash
		var fileHash string
		if hash, hashErr := hashutil.MD5File(localPath); hashErr == nil {
			fileHash = hash
		}

		// Upload to Drive if client is available, folder resolved, and upload_drive is true
		var driveLink string
		shouldUpload := req.UploadDrive && localPath != "" && s.driveClient != nil && driveFolderID != ""
		if shouldUpload {
			uploader := &drive.Uploader{Service: s.driveClient, Log: s.log}
			filename := filepath.Base(localPath)
			result, uploadErr := uploader.UploadFile(ctx, localPath, driveFolderID, filename)
			if uploadErr != nil {
				s.log.Warn("failed to upload to drive", zap.Error(uploadErr))
			} else {
				driveLink = result.WebViewLink
				item.DriveLink = result.WebViewLink
			}
		}

		// Save clip to database
		if req.SaveDB && s.clipsRepo != nil {
			// Build metadata using json.Marshal for proper escaping
			metadataMap := map[string]string{
				"start":     item.Start,
				"end":       item.End,
				"group":     getGroupFromDestination(req.Destination),
				"subfolder": getSubfolderFromDestination(req.Destination),
			}
			metadataBytes, err := json.Marshal(metadataMap)
			metadata := string(metadataBytes)
			if err != nil {
				s.log.Warn("failed to marshal metadata", zap.Error(err))
				metadata = fmt.Sprintf(`{"start":"%s","end":"%s","group":"%s","subfolder":"%s"}`,
					item.Start, item.End,
					getGroupFromDestination(req.Destination),
					getSubfolderFromDestination(req.Destination))
			}

			// Use resolved path or fallback to request path
			folderPath := resolvedPath
			if folderPath == "" && req.Destination != nil {
				folderPath = req.Destination.FolderPath
			}

			clip := &models.Clip{
				ID:          clipID,
				Name:        item.Name,
				Filename:    filepath.Base(localPath),
				FolderID:    driveFolderID,
				FolderPath:  folderPath,
				Group:       getGroupFromDestination(req.Destination),
				MediaType:   "youtube_clip",
				DriveLink:   driveLink,
				Tags:        seg.Tags,
				Source:      "youtube",
				Category:    "manual_extract",
				ExternalURL: resp.SourceURL,
				Duration:    0,
				Metadata:    metadata,
				FileHash:    fileHash,
				LocalPath:   localPath,
				CreatedAt:   time.Now().UTC(),
				UpdatedAt:   time.Now().UTC(),
			}
			if err := s.clipsRepo.UpsertClip(ctx, clip); err != nil {
				s.log.Warn("failed to save clip to db", zap.Error(err))
			}
		}

		resp.Items = append(resp.Items, item)
	}

	// Generate or update summary TXT file
	summaryPath := filepath.Join(s.cfg.Storage.DataDir, "riepilogo_clip.txt")
	s.log.Info("updating summary file", zap.String("path", summaryPath), zap.Int("items", len(resp.Items)))
	if err := s.updateSummaryFile(ctx, resp, summaryPath); err != nil {
		s.log.Warn("failed to update summary file", zap.Error(err))
	} else {
		s.log.Info("summary file updated successfully", zap.String("path", summaryPath))
	}

	return resp, nil
}

// getGroupFromDestination extracts group name from destination request
func getGroupFromDestination(dest *DestinationRequest) string {
	if dest == nil {
		return ""
	}
	return dest.Group
}

// getSubfolderFromDestination extracts subfolder name from destination request
func getSubfolderFromDestination(dest *DestinationRequest) string {
	if dest == nil {
		return ""
	}
	return dest.SubfolderName
}

func findFirstOutput(dir, prefix string) string {
	matches, _ := filepath.Glob(filepath.Join(dir, prefix+".*"))
	if len(matches) == 0 {
		return ""
	}
	return matches[0]
}

// MaxSegmentDuration is the maximum allowed duration for a single clip segment (120 seconds)
const MaxSegmentDuration = 120

// parseTimestamp parses a timestamp string (e.g., "10:31", "1:23:45", "45") to seconds
func parseTimestamp(ts string) (int, error) {
	ts = strings.TrimSpace(ts)
	if ts == "" {
		return 0, fmt.Errorf("empty timestamp")
	}

	parts := strings.Split(ts, ":")
	if len(parts) == 1 {
		var seconds int
		_, err := fmt.Sscanf(ts, "%d", &seconds)
		if err != nil {
			return 0, fmt.Errorf("invalid timestamp format: %s", ts)
		}
		return seconds, nil
	}

	var totalSeconds int
	if len(parts) == 2 {
		var minutes, seconds int
		_, err := fmt.Sscanf(parts[0]+":"+parts[1], "%d:%d", &minutes, &seconds)
		if err != nil {
			return 0, fmt.Errorf("invalid timestamp format: %s", ts)
		}
		totalSeconds = minutes*60 + seconds
	} else if len(parts) == 3 {
		var hours, minutes, seconds int
		_, err := fmt.Sscanf(parts[0]+":"+parts[1]+":"+parts[2], "%d:%d:%d", &hours, &minutes, &seconds)
		if err != nil {
			return 0, fmt.Errorf("invalid timestamp format: %s", ts)
		}
		totalSeconds = hours*3600 + minutes*60 + seconds
	} else {
		return 0, fmt.Errorf("invalid timestamp format: %s", ts)
	}

	return totalSeconds, nil
}

func extractVideoID(inputURL string) string {
	parsed, err := url.Parse(inputURL)
	if err != nil {
		return ""
	}

	// Handle youtu.be short links
	if parsed.Hostname() == "youtu.be" {
		path := strings.TrimPrefix(parsed.Path, "/")
		if path != "" {
			return path
		}
	}

	// Handle youtube.com URLs
	if strings.Contains(parsed.Hostname(), "youtube.com") {
		// Standard watch URLs: youtube.com/watch?v=ID
		if parsed.Path == "/watch" {
			return parsed.Query().Get("v")
		}
		// Shorts URLs: youtube.com/shorts/ID
		if strings.HasPrefix(parsed.Path, "/shorts/") {
			id := strings.TrimPrefix(parsed.Path, "/shorts/")
			if idx := strings.Index(id, "?"); idx != -1 {
				id = id[:idx]
			}
			return id
		}
		// Embed URLs: youtube.com/embed/ID
		if strings.HasPrefix(parsed.Path, "/embed/") {
			id := strings.TrimPrefix(parsed.Path, "/embed/")
			if idx := strings.Index(id, "?"); idx != -1 {
				id = id[:idx]
			}
			return id
		}
		// Live URLs: youtube.com/live/ID
		if strings.HasPrefix(parsed.Path, "/live/") {
			id := strings.TrimPrefix(parsed.Path, "/live/")
			if idx := strings.Index(id, "?"); idx != -1 {
				id = id[:idx]
			}
			return id
		}
	}

	return ""
}

// updateSummaryFile creates or updates the summary TXT file with clip information
func (s *Service) updateSummaryFile(ctx context.Context, resp *ExtractResponse, filePath string) error {
	// Read existing file if it exists
	existingContent := ""
	fileExists := false
	if data, err := os.ReadFile(filePath); err == nil {
		existingContent = string(data)
		fileExists = true
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("failed to read summary file: %w", err)
	}

	var sb strings.Builder

	if !fileExists {
		// New file: add header
		sb.WriteString("📋 RIEPILOGO CLIP\n")
		sb.WriteString(strings.Repeat("=", 80) + "\n\n")
	}

	// Count existing clips to determine starting number for new clips
	existingClipCount := 0
	if fileExists {
		lines := strings.Split(existingContent, "\n")
		for _, line := range lines {
			if isClipLine(line) {
				existingClipCount++
			}
		}
	}

	// Build content: existing content + new clips
	if fileExists {
		sb.WriteString(existingContent)
		// Ensure trailing newline before appending
		if !strings.HasSuffix(existingContent, "\n") {
			sb.WriteString("\n")
		}
		sb.WriteString("\n")
	}

	// Add new clips from current response
	newClipCount := 0
	for _, item := range resp.Items {
		if item.Status == "failed" || item.Status == "skipped_existing" {
			continue
		}
		existingClipCount++
		newClipCount++

		sb.WriteString(fmt.Sprintf("%d. [%s]\n", existingClipCount, item.Name))
		if item.DriveLink != "" {
			sb.WriteString(fmt.Sprintf("   🔗 Link: %s\n", item.DriveLink))
		}
		if item.LocalPath != "" {
			sb.WriteString(fmt.Sprintf("   📁 File: %s\n", filepath.Base(item.LocalPath)))
		}
		sb.WriteString(fmt.Sprintf("   📊 Stato: %s\n", item.Status))
		if item.DriveFolderPath != "" {
			sb.WriteString(fmt.Sprintf("   📂 Cartella: %s\n", item.DriveFolderPath))
		}
		if item.DriveFolderID != "" {
			sb.WriteString(fmt.Sprintf("   🆔 Folder ID: %s\n", item.DriveFolderID))
		}
		sb.WriteString("\n")
	}

	// Only write if we have new clips
	if newClipCount == 0 {
		return nil
	}

	// Update total count in header
	content := sb.String()
	totalClips := countClipsInContent(content)

	if fileExists {
		// Replace the old total line
		lines := strings.Split(content, "\n")
		for i, line := range lines {
			if strings.Contains(line, "Totale clip:") {
				lines[i] = fmt.Sprintf("📊 Totale clip: %d", totalClips)
				break
			}
		}
		content = strings.Join(lines, "\n")
	} else {
		// Insert total line after the separator line
		lines := strings.Split(content, "\n")
		for i, line := range lines {
			if strings.Contains(line, "===") {
				newLines := append(lines[:i+1],
					append([]string{"", fmt.Sprintf("📊 Totale clip: %d", totalClips), ""},
						lines[i+1:]...)...)
				content = strings.Join(newLines, "\n")
				break
			}
		}
	}

	return os.WriteFile(filePath, []byte(content), 0644)
}

// isClipLine checks if a line is a clip entry (starts with a number followed by ". [")
func isClipLine(line string) bool {
	matched, _ := regexp.MatchString(`^\d+\.\s*\[`, line)
	return matched
}

// countClipsInContent counts the number of clip entries in the content
func countClipsInContent(content string) int {
	count := 0
	lines := strings.Split(content, "\n")
	for _, line := range lines {
		if isClipLine(line) {
			count++
		}
	}
	return count
}
