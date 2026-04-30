package artlist

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

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
	copyReq.Strategy = normalizeRunStrategy(&copyReq)
	return &copyReq
}

func normalizeRunStrategy(req *RunTagRequest) string {
	if req == nil {
		return "verify"
	}

	strategy := strings.ToLower(strings.TrimSpace(req.Strategy))
	switch strategy {
	case "skip", "verify", "replace":
		return strategy
	}
	if req.ForceReupload {
		return "replace"
	}
	return "verify"
}

func runDedupKey(term, rootFolderID, strategy string, dryRun bool) string {
	return strings.ToLower(strings.TrimSpace(term)) + "|" + strings.ToLower(strings.TrimSpace(rootFolderID)) + "|" + strings.ToLower(strings.TrimSpace(strategy)) + "|" + fmt.Sprintf("%t", dryRun)
}

func sanitizeDriveFolderName(term string) string {
	term = strings.ToLower(strings.TrimSpace(term))
	var b strings.Builder
	lastUnderscore := false
	for _, r := range term {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastUnderscore = false
		case r == ' ' || r == '-' || r == '_' || r == '.':
			if b.Len() > 0 && !lastUnderscore {
				b.WriteByte('_')
				lastUnderscore = true
			}
		default:
			if r < 128 {
				if b.Len() > 0 && !lastUnderscore {
					b.WriteByte('_')
					lastUnderscore = true
				}
			}
		}
	}
	out := strings.Trim(b.String(), "_")
	if out == "" {
		return "artlist"
	}
	return out
}

func boolToInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func nullString(v string) interface{} {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	return v
}

func isUniqueConstraintErr(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(strings.ToLower(err.Error()), "unique constraint failed")
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
	if id := driveFileIDFromLink(clip.DownloadLink); id != "" {
		return id
	}
	return driveFileIDFromLink(clip.DriveLink)
}

func driveFileIDFromLink(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}

	if u, err := url.Parse(raw); err == nil {
		if id := strings.TrimSpace(u.Query().Get("id")); id != "" {
			return id
		}
		path := strings.Trim(u.Path, "/")
		parts := strings.Split(path, "/")
		for i := 0; i < len(parts); i++ {
			if parts[i] == "d" && i+1 < len(parts) {
				return parts[i+1]
			}
		}
	}

	if idx := strings.Index(raw, "id="); idx >= 0 {
		id := raw[idx+3:]
		if cut := strings.IndexAny(id, "&?#"); cut >= 0 {
			id = id[:cut]
		}
		return id
	}

	return ""
}

func extractDriveMD5FromMetadata(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return ""
	}
	if v, ok := payload["drive_md5_checksum"].(string); ok {
		return strings.TrimSpace(v)
	}
	if v, ok := payload["md5_checksum"].(string); ok {
		return strings.TrimSpace(v)
	}
	return ""
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
