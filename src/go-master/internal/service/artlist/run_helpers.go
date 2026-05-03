package artlist

import (
	"crypto/sha256"
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
	// Build canonical request for deduplication
	canonical := map[string]any{
		"term":           strings.ToLower(strings.TrimSpace(term)),
		"root_folder_id": strings.TrimSpace(rootFolderID),
		"strategy":       strings.ToLower(strings.TrimSpace(strategy)),
		"dry_run":        dryRun,
	}
	raw, err := json.Marshal(canonical)
	if err != nil {
		// Fallback to simple key if JSON fails
		return fmt.Sprintf("%s|%s|%s|%v", strings.ToLower(strings.TrimSpace(term)), strings.TrimSpace(rootFolderID), strings.ToLower(strings.TrimSpace(strategy)), dryRun)
	}
	hash := sha256.Sum256(raw)
	return fmt.Sprintf("%x", hash)
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


