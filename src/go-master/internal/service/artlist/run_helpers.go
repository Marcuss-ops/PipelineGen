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

// RunDefaults holds default values for request normalization.
type RunDefaults struct {
	DefaultRootFolderID string
	DefaultLimit        int
	MaxLimit            int
}

// NormalizeRunTagRequest normalizes a RunTagRequest using the provided defaults.
// This is the SINGLE normalization function that should be used everywhere:
// - Before dedup key generation
// - Before job enqueue
// - Before job execution
// - At the start of pipeline RunTag
func NormalizeRunTagRequest(req RunTagRequest, defaults RunDefaults) RunTagRequest {
	// Normalize term
	req.Term = strings.TrimSpace(req.Term)

	// Normalize limit
	if req.Limit <= 0 {
		if defaults.DefaultLimit > 0 {
			req.Limit = defaults.DefaultLimit
		} else {
			req.Limit = 1
		}
	}
	if defaults.MaxLimit > 0 && req.Limit > defaults.MaxLimit {
		req.Limit = defaults.MaxLimit
	}

	// Normalize root folder ID
	req.RootFolderID = strings.TrimSpace(req.RootFolderID)
	if req.RootFolderID == "" && defaults.DefaultRootFolderID != "" {
		req.RootFolderID = defaults.DefaultRootFolderID
	}

	// Handle deprecated ForceReupload once, then forget it
	if req.ForceReupload {
		if req.Strategy == "" {
			req.Strategy = "replace"
		}
		req.ForceReupload = false
	}

	// Normalize strategy
	req.Strategy = string(pipeline.NormalizeStrategy(req.Strategy, false))

	return req
}

// Deprecated: Use NormalizeRunTagRequest instead.
func normalizeRunRequest(req *RunTagRequest) *RunTagRequest {
	if req == nil {
		return &RunTagRequest{}
	}
	copyReq := *req
	normalized := NormalizeRunTagRequest(copyReq, RunDefaults{
		MaxLimit: 500,
	})
	return &normalized
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
