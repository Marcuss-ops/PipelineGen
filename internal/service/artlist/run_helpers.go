package artlist

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"velox/go-master/internal/service/pipeline"
	"velox/go-master/internal/upload/drive"
	"velox/go-master/pkg/models"
)

func normalizeRunRequest(req *RunTagRequest) *RunTagRequest {
	if req == nil {
		return &RunTagRequest{}
	}

	copyReq := *req
	copyReq.Term = strings.TrimSpace(copyReq.Term)
	if copyReq.Limit <= 0 {
		copyReq.Limit = 1
	}
	if copyReq.Limit > 500 {
		copyReq.Limit = 500
	}
	copyReq.RootFolderID = strings.TrimSpace(copyReq.RootFolderID)
	copyReq.Strategy = string(pipeline.NormalizeStrategy(req.Strategy, req.ForceReupload))
	return &copyReq
}

func runDedupKey(term, rootFolderID, strategy string, dryRun bool) string {
	return strings.ToLower(strings.TrimSpace(term)) + "|" + strings.ToLower(strings.TrimSpace(rootFolderID)) + "|" + strings.ToLower(strings.TrimSpace(strategy)) + "|" + fmt.Sprintf("%t", dryRun)
}


func (s *Service) shouldSkipClip(ctx context.Context, strategy string, clip *models.Clip) (bool, error) {
	if clip == nil {
		return false, nil
	}

	switch strategy {
	case "replace":
		return false, nil
	case "skip":
		return strings.TrimSpace(clip.DriveLink) != "", nil
	case "verify":
		if strings.TrimSpace(clip.DriveLink) == "" {
			return false, nil
		}
		if strings.TrimSpace(clip.FileHash) == "" {
			return true, nil
		}
		if md5Checksum := extractDriveMD5FromMetadata(clip.Metadata); md5Checksum != "" && strings.EqualFold(md5Checksum, strings.TrimSpace(clip.FileHash)) {
			return true, nil
		}
		remoteChecksum, err := s.remoteDriveChecksum(ctx, clip)
		if err != nil {
			return true, nil
		}
		if remoteChecksum == "" {
			return true, nil
		}
		return strings.EqualFold(remoteChecksum, strings.TrimSpace(clip.FileHash)), nil
	default:
		return strings.TrimSpace(clip.DriveLink) != "", nil
	}
}

func (s *Service) remoteDriveChecksum(ctx context.Context, clip *models.Clip) (string, error) {
	if s.driveClient == nil {
		return "", fmt.Errorf("drive client not configured")
	}

	fileID := driveFileIDFromClip(clip)
	if fileID == "" {
		return "", fmt.Errorf("drive file id unavailable")
	}

	file, err := s.driveClient.Files.Get(fileID).Fields("id,md5Checksum").Context(ctx).Do()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(file.Md5Checksum), nil
}

func driveFileIDFromClip(clip *models.Clip) string {
	if clip == nil {
		return ""
	}
	if id := drive.FileIDFromLink(clip.DownloadLink); id != "" {
		return id
	}
	return drive.FileIDFromLink(clip.DriveLink)
}

func extractDriveMD5FromMetadata(raw string) string {
	return drive.MD5FromMetadata(raw)
}

func composeArtlistMetadata(existing, fileHash, driveChecksum string) string {
	payload := map[string]any{
		"processor_version":  "artlist-pipeline-v2",
		"processed_at":       time.Now().UTC().Format(time.RFC3339),
		"file_hash":          strings.TrimSpace(fileHash),
		"drive_md5_checksum": strings.TrimSpace(driveChecksum),
	}
	if strings.TrimSpace(existing) != "" {
		payload["legacy_metadata"] = strings.TrimSpace(existing)
	}

	raw, err := json.Marshal(payload)
	if err != nil {
		return strings.TrimSpace(existing)
	}
	return string(raw)
}


