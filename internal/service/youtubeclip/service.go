package youtubeclip

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.uber.org/zap"
	driveapi "google.golang.org/api/drive/v3"

	"velox/go-master/internal/repository/clips"
	"velox/go-master/internal/service/drivedestination"
	"velox/go-master/internal/service/foldermemory"
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
	folderMemory     *foldermemory.Service
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
		folderMemory:     foldermemory.NewService(log, clipsRepo),
	}
}

func (s *Service) Extract(ctx context.Context, req *ExtractRequest) (*ExtractResponse, error) {
	s.log.Info("YouTube Extract service called", zap.String("url", req.URL))
	
	videoID := extractVideoID(req.URL)
	if videoID == "" {
		videoID = hashutil.MD5String(req.URL)[:12]
	}

	resp := &ExtractResponse{
		OK:        true,
		SourceURL: strings.TrimSpace(req.URL),
		VideoID:   videoID,
		Stats: &ExtractStats{
			Requested: len(req.Segments),
		},
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

	// Create stable folder path using video ID instead of timestamp
	folderSlug := "yt_" + videoID
	if req.Destination != nil && req.Destination.SubfolderName != "" {
		folderSlug = pathutil.Slug(req.Destination.SubfolderName)
	}
	
	outDir := filepath.Join(s.cfg.Storage.DataDir, "youtube-clips", folderSlug)
	if err := os.MkdirAll(outDir, 0755); err != nil {
		resp.OK = false
		resp.Error = err.Error()
		return resp, err
	}
	s.log.Info("using stable folder for video", zap.String("folder", outDir), zap.String("video_id", videoID))

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
		if destReq.SubfolderName == "" {
			destReq.SubfolderName = "yt_" + videoID
			destReq.CreateSubfolder = true
			s.log.Info("auto-assigning video subfolder", zap.String("subfolder", destReq.SubfolderName))
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
	resp.Folder = &FolderInfo{
		ID:               fmt.Sprintf("clipfolder_youtube_%s", videoID),
		LocalFolderPath:  outDir,
		DriveFolderID:    driveFolderID,
		DriveFolderPath:  resolvedPath,
		ManifestTXTPath:  filepath.Join(outDir, "clip_manifest.txt"),
		ManifestJSONPath: filepath.Join(outDir, "clip_manifest.json"),
	}
	resp.DriveFolderID = driveFolderID
	resp.DriveFolderPath = resolvedPath

	// Initialize or load clip folder from DB
	folderID := fmt.Sprintf("clipfolder_youtube_%s", videoID)
	var clipFolder *models.ClipFolder
	if s.clipsRepo != nil {
		existingFolder, err := s.clipsRepo.GetClipFolder(ctx, folderID)
		if err == nil && existingFolder != nil {
			clipFolder = existingFolder
			s.log.Info("loaded existing clip folder", zap.String("folder_id", folderID))

			// Update drive info if it was missing but we have it now
			if clipFolder.FolderID == "" && driveFolderID != "" {
				clipFolder.FolderID = driveFolderID
				clipFolder.FolderPath = resolvedPath
				clipFolder.Group = getGroupFromDestination(req.Destination)
			}
			
			// Update local path if it changed (e.g. user provided a specific subfolder_name)
			if clipFolder.LocalFolderPath != outDir {
				clipFolder.LocalFolderPath = outDir
				clipFolder.ManifestTXTPath = filepath.Join(outDir, "clip_manifest.txt")
				clipFolder.ManifestJSONPath = filepath.Join(outDir, "clip_manifest.json")
			}
		} else {
			clipFolder = &models.ClipFolder{
				ID:              folderID,
				Source:          "youtube",
				SourceURL:       resp.SourceURL,
				VideoID:         videoID,
				FolderID:        driveFolderID,
				FolderPath:      resolvedPath,
				LocalFolderPath: outDir,
				Group:           getGroupFromDestination(req.Destination),
				ManifestTXTPath: filepath.Join(outDir, "clip_manifest.txt"),
				ManifestJSONPath: filepath.Join(outDir, "clip_manifest.json"),
				CreatedAt:       time.Now().UTC(),
				UpdatedAt:       time.Now().UTC(),
			}
			s.log.Info("created new clip folder", zap.String("folder_id", folderID))
		}
	}

	// Load existing manifest if available
	manifest := &models.ClipManifest{
		ID:              folderID,
		FolderID:        driveFolderID,
		FolderPath:      resolvedPath,
		Source:          "youtube",
		SourceURL:       resp.SourceURL,
		VideoID:         videoID,
		LocalFolderPath: outDir,
		Clips:           []models.ClipManifestItem{},
	}
	if clipFolder != nil && clipFolder.ManifestJSONPath != "" {
		loadedManifest, err := s.folderMemory.LoadManifest(clipFolder.ManifestJSONPath)
		if err == nil && loadedManifest != nil {
			manifest = loadedManifest
			s.log.Info("loaded existing manifest", zap.Int("clip_count", len(manifest.Clips)))

			// Restore/Update drive info if missing in file but present in current request
			if manifest.FolderID == "" && driveFolderID != "" {
				manifest.FolderID = driveFolderID
				manifest.FolderPath = resolvedPath
			}
			if manifest.ID == "" {
				manifest.ID = folderID
			}
		}
	}

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
			resp.Stats.Failed++
			resp.OK = false
			continue
		}

		if err := security.SanitizeTimestamp(item.End); err != nil {
			item.Status = "failed"
			item.Error = "invalid end timestamp: " + err.Error()
			resp.Items = append(resp.Items, item)
			resp.Stats.Failed++
			resp.OK = false
			continue
		}

		// Validate start < end and check max duration
		startSec, err := parseTimestamp(item.Start)
		if err != nil {
			item.Status = "failed"
			item.Error = "invalid start timestamp format: " + err.Error()
			resp.Items = append(resp.Items, item)
			resp.Stats.Failed++
			resp.OK = false
			continue
		}
		endSec, err := parseTimestamp(item.End)
		if err != nil {
			item.Status = "failed"
			item.Error = "invalid end timestamp format: " + err.Error()
			resp.Items = append(resp.Items, item)
			resp.Stats.Failed++
			resp.OK = false
			continue
		}
		if startSec >= endSec {
			item.Status = "failed"
			item.Error = fmt.Sprintf("start time (%s) must be before end time (%s)", item.Start, item.End)
			resp.Items = append(resp.Items, item)
			resp.Stats.Failed++
			resp.OK = false
			continue
		}
		duration := endSec - startSec
		if duration > MaxSegmentDuration {
			item.Status = "failed"
			item.Error = fmt.Sprintf("segment duration (%d seconds) exceeds maximum allowed (%d seconds)", duration, MaxSegmentDuration)
			resp.Items = append(resp.Items, item)
			resp.Stats.Failed++
			resp.OK = false
			continue
		}

		// Create stable ID: yt_videoID_startSec_endSec
		clipID := fmt.Sprintf("yt_%s_%d_%d", videoID, startSec, endSec)
		item.ID = clipID

		// Check if clip already exists (deduplication)
		strategy := req.Strategy
		if strategy == "" {
			strategy = "verify"
		}

		saveDB := boolDefault(req.SaveDB, true)
		if s.clipsRepo != nil && saveDB && strategy != "replace" {
			existingClip, clipErr := s.clipsRepo.GetClip(ctx, clipID)
			
			shouldSkip := false
			skipReason := ""

			// 1. Check DB record
			if clipErr == nil && existingClip != nil {
				if strategy == "skip" {
					shouldSkip = true
					skipReason = "existing DB record (skip strategy)"
				} else {
					// verify strategy
					if existingClip.LocalPath != "" {
						if _, statErr := os.Stat(existingClip.LocalPath); statErr == nil {
							shouldSkip = true
							skipReason = "valid local file"
						}
					}
					if !shouldSkip && existingClip.DriveLink != "" {
						shouldSkip = true
						skipReason = "valid drive link"
					}
				}
			}

			// 2. Check manifest
			if !shouldSkip && manifest != nil {
				for _, mItem := range manifest.Clips {
					if mItem.ID == clipID {
						if strategy == "skip" {
							shouldSkip = true
							skipReason = "found in manifest (skip strategy)"
						} else if mItem.Status == "processed" {
							if mItem.LocalPath != "" {
								if _, statErr := os.Stat(mItem.LocalPath); statErr == nil {
									shouldSkip = true
									skipReason = "processed in manifest (local file exists)"
								}
							}
							if !shouldSkip && mItem.DriveLink != "" {
								shouldSkip = true
								skipReason = "processed in manifest (drive link exists)"
							}
						}
						break
					}
				}
			}

			if shouldSkip {
				s.log.Info("clip already exists, skipping processing",
					zap.String("clip_id", clipID),
					zap.String("reason", skipReason),
				)
				if existingClip != nil {
					item.LocalPath = existingClip.LocalPath
					item.DriveLink = existingClip.DriveLink
				}
				item.Status = "skipped_existing"

				// Update manifest even if skipped
				if manifest != nil {
					found := false
					for _, mItem := range manifest.Clips {
						if mItem.ID == clipID {
							found = true
							break
						}
					}
					if !found {
						manifest.Clips = append(manifest.Clips, models.ClipManifestItem{
							ID:              clipID,
							Name:            item.Name,
							Start:           item.Start,
							End:             item.End,
							StartSeconds:    startSec,
							EndSeconds:      endSec,
							DurationSeconds: duration,
							LocalPath:       item.LocalPath,
							DriveLink:       item.DriveLink,
							Status:          item.Status,
							Tags:            fmt.Sprintf("%v", seg.Tags),
						})
					}
				}

				resp.Items = append(resp.Items, item)
				resp.Stats.Skipped++
				continue
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
			resp.Stats.Failed++
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
			resp.Stats.Failed++
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
		uploadDrive := boolDefault(req.UploadDrive, true)
		shouldUpload := uploadDrive && localPath != "" && s.driveClient != nil && driveFolderID != ""
		if shouldUpload {
			uploader := &drive.Uploader{Service: s.driveClient, Log: s.log}
			filename := filepath.Base(localPath)
			result, uploadErr := uploader.UploadFile(ctx, localPath, driveFolderID, filename)
			if uploadErr != nil {
				s.log.Warn("failed to upload to drive", zap.Error(uploadErr))
				// If user requested upload but it failed, mark as upload_failed
				if uploadDrive {
					item.Status = "upload_failed"
					item.Error = fmt.Sprintf("drive upload failed: %v", uploadErr)
					resp.Items = append(resp.Items, item)
					resp.Stats.Failed++
					resp.OK = false
					continue
				}
			} else {
				driveLink = result.WebViewLink
				item.DriveLink = result.WebViewLink
			}
		}

		// Save clip to database
		if saveDB && s.clipsRepo != nil {
			// Build metadata using json.Marshal for proper escaping
			metadataMap := map[string]interface{}{
				"video_id":         videoID,
				"start":            item.Start,
				"end":              item.End,
				"start_seconds":    startSec,
				"end_seconds":      endSec,
				"duration_seconds": duration,
				"folder_slug":      folderSlug,
				"strategy":         strategy,
				"normalized":       shouldNormalize,
				"keep_audio":       req.KeepAudio,
			}
			metadataBytes, _ := json.Marshal(metadataMap)
			metadata := string(metadataBytes)

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
				Duration:    duration,
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

		// Update manifest with this clip
		if manifest != nil {
			newMItem := models.ClipManifestItem{
				ID:              clipID,
				Name:            item.Name,
				Start:           item.Start,
				End:             item.End,
				StartSeconds:    startSec,
				EndSeconds:      endSec,
				DurationSeconds: duration,
				Filename:        filepath.Base(localPath),
				LocalPath:       item.LocalPath,
				DriveLink:       item.DriveLink,
				FileHash:        fileHash,
				Status:          item.Status,
				Tags:            fmt.Sprintf("%v", seg.Tags),
			}
			
			// Replace existing or append new
			found := false
			for j, mItem := range manifest.Clips {
				if mItem.ID == clipID {
					manifest.Clips[j] = newMItem
					found = true
					break
				}
			}
			if !found {
				manifest.Clips = append(manifest.Clips, newMItem)
			}
		}

		resp.Items = append(resp.Items, item)
		resp.Stats.Processed++
	}

	// Update folder manifest (TXT + JSON)
	if clipFolder != nil {
			// Compute manifest stats using foldermemory
		stats := s.folderMemory.ComputeManifestStats(manifest)
		manifest.Stats = stats

		clipFolder.ClipCount = stats.ClipCount
		clipFolder.ProcessedCount = stats.ProcessedCount
		clipFolder.FailedCount = stats.FailedCount
		clipFolder.SkippedCount = stats.SkippedCount
		clipFolder.UpdatedAt = time.Now().UTC()

		// Save manifest JSON
		if manifest != nil {
			if err := s.folderMemory.SaveManifest(clipFolder.ManifestJSONPath, manifest); err != nil {
				s.log.Warn("failed to write manifest JSON", zap.Error(err))
			} else {
				s.log.Info("manifest JSON updated", zap.String("path", clipFolder.ManifestJSONPath))
			}
		}

		// Save manifest TXT (respect WriteSummary flag)
		writeSummary := boolDefault(req.WriteSummary, true)
		if writeSummary && clipFolder.ManifestTXTPath != "" {
			if err := s.folderMemory.UpdateManifestTXT(clipFolder, manifest); err != nil {
				s.log.Warn("failed to write manifest TXT", zap.Error(err))
			} else {
				s.log.Info("manifest TXT updated", zap.String("path", clipFolder.ManifestTXTPath))
			}
		}

		// Upsert clip folder to DB
		if clipFolder != nil {
			if err := s.folderMemory.UpsertClipFolder(ctx, clipFolder); err != nil {
				s.log.Warn("failed to upsert clip folder", zap.Error(err))
			}
		}
	}

	return resp, nil
}

// GetFolder returns a clip folder by ID
func (s *Service) GetFolder(ctx context.Context, folderID string) (*models.ClipFolder, error) {
	if s.clipsRepo == nil {
		return nil, fmt.Errorf("clips repository not available")
	}
	return s.clipsRepo.GetClipFolder(ctx, folderID)
}

// GetFolderByVideoID returns a clip folder by video ID
func (s *Service) GetFolderByVideoID(ctx context.Context, videoID string) (*models.ClipFolder, error) {
	if s.clipsRepo == nil {
		return nil, fmt.Errorf("clips repository not available")
	}
	return s.clipsRepo.GetClipFolderByVideoID(ctx, videoID)
}

// ListFolders returns all clip folders
func (s *Service) ListFolders(ctx context.Context, source string) ([]*models.ClipFolder, error) {
	if s.clipsRepo == nil {
		return nil, fmt.Errorf("clips repository not available")
	}
	return s.clipsRepo.ListClipFolders(ctx, source)
}

// SearchFolders searches clip folders by keyword
func (s *Service) SearchFolders(ctx context.Context, keyword string) ([]*models.ClipFolder, error) {
	if s.clipsRepo == nil {
		return nil, fmt.Errorf("clips repository not available")
	}
	return s.clipsRepo.SearchClipFolders(ctx, keyword)
}

// ListFolderClips returns all clips in a folder by folder ID
func (s *Service) ListFolderClips(ctx context.Context, folderID string) ([]*models.Clip, error) {
	if s.clipsRepo == nil {
		return nil, fmt.Errorf("clips repository not available")
	}
	return s.clipsRepo.ListClipsByFolderID(ctx, folderID)
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

// boolDefault returns the value of the bool pointer, or the default value if nil
func boolDefault(v *bool, def bool) bool {
	if v == nil {
		return def
	}
	return *v
}

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

