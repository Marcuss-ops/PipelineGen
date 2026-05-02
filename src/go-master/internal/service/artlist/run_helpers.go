package artlist

import (
	"encoding/json"
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
	return strings.ToLower(strings.TrimSpace(term)) + "|" + strings.ToLower(strings.TrimSpace(rootFolderID)) + "|" + strings.ToLower(strings.TrimSpace(strategy)) + "|" + formatBool(dryRun)
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

func formatBool(b bool) string {
	if b {
		return "true"
	}
	return "false"
}
