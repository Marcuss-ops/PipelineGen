package youtubeclip

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
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
}

func NewService(
	cfg *config.Config,
	log *zap.Logger,
	clipsRepo *clips.Repository,
	driveClient *driveapi.Service,
	driveDestination *drivedestination.Service,
) *Service {
	return &Service{
		cfg:              cfg,
		log:              log,
		clipsRepo:        clipsRepo,
		driveClient:      driveClient,
		driveDestination: driveDestination,
	}
}

func (s *Service) Extract(ctx context.Context, req *ExtractRequest) (*ExtractResponse, error) {
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
		resolved, err := s.driveDestination.Resolve(ctx, destReq)
		if err != nil {
			s.log.Warn("failed to resolve drive destination", zap.Error(err))
		} else {
			driveFolderID = resolved.FolderID
			resolvedPath = resolved.FolderPath
		}
	}

	for i, seg := range req.Segments {
		item := ExtractItem{
			Name:  pathutil.Slug(seg.Name),
			Start: strings.TrimSpace(seg.Start),
			End:   strings.TrimSpace(seg.End),
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

		outputTemplate := filepath.Join(outDir, fmt.Sprintf("%03d_%s", i+1, item.Name))
		section := fmt.Sprintf("*%s-%s", item.Start, item.End)

		err := dl.Download(ctx, &downloader.DownloadRequest{
			URL:             resp.SourceURL,
			OutputPath:      outputTemplate,
			Format:          "bv*[ext=mp4]+ba[ext=m4a]/b[ext=mp4]/best",
			MergeFormat:     "mp4",
			NoPlaylist:      true,
			DownloadSections: []string{section},
			ForceKeyframes:  req.ForceKeyframes,
			Timeout:         10 * time.Minute,
		})

		if err != nil {
			item.Status = "failed"
			item.Error = fmt.Sprintf("yt-dlp failed: %v", err)
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

		// Calculate file hash
		var fileHash string
		if hash, err := hashutil.MD5File(localPath); err == nil {
			fileHash = hash
		}

		// Upload to Drive if client is available, folder resolved, and upload_drive is true
		var driveLink string
		if req.UploadDrive && localPath != "" && s.driveClient != nil && driveFolderID != "" {
			uploader := &drive.Uploader{Service: s.driveClient, Log: s.log}
			filename := filepath.Base(localPath)
			result, err := uploader.UploadFile(ctx, localPath, driveFolderID, filename)
			if err != nil {
				s.log.Warn("failed to upload to drive", zap.Error(err))
			} else {
				driveLink = result.WebViewLink
				item.DriveLink = result.WebViewLink
			}
		}

		// Save clip to database
		if req.SaveDB && s.clipsRepo != nil {
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

			metadata := fmt.Sprintf(`{"start":"%s","end":"%s","group":"%s","subfolder":"%s"}`,
				item.Start, item.End,
				getGroupFromDestination(req.Destination),
				getSubfolderFromDestination(req.Destination))

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

func extractVideoID(url string) string {
	if strings.Contains(url, "v=") {
		parts := strings.Split(url, "v=")
		if len(parts) > 1 {
			id := parts[1]
			if idx := strings.Index(id, "&"); idx != -1 {
				id = id[:idx]
			}
			return id
		}
	}
	if strings.Contains(url, "youtu.be/") {
		parts := strings.Split(url, "youtu.be/")
		if len(parts) > 1 {
			id := parts[1]
			if idx := strings.Index(id, "?"); idx != -1 {
				id = id[:idx]
			}
			return id
		}
	}
	return ""
}
