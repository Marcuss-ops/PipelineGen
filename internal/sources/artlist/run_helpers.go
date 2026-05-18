package artlist

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"velox/go-master/internal/storage/drive"
	"velox/go-master/internal/media/models"
)

// RunDefaults holds default values for request normalization.
type RunDefaults struct {
	DefaultRootFolderID string
	DefaultLimit        int
	MaxLimit            int
}

// NormalizeSearchTerm trims the term and keeps at most the first two words.
// This keeps Artlist searches focused on the strongest query tokens.
func NormalizeSearchTerm(term string) string {
	term = strings.TrimSpace(term)
	if term == "" {
		return ""
	}

	parts := strings.Fields(term)
	if len(parts) > 2 {
		parts = parts[:2]
	}
	return strings.Join(parts, " ")
}

// NormalizeRunTagRequest normalizes a RunTagRequest using the provided defaults.
// This is the SINGLE normalization function that should be used everywhere:
// - Before dedup key generation
// - Before job enqueue
// - Before job execution
// - At the start of pipeline RunTag
func NormalizeRunTagRequest(req RunTagRequest, defaults RunDefaults) RunTagRequest {
	// Normalize term
	req.Term = NormalizeSearchTerm(req.Term)

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

	// Normalize strategy
	req.Strategy = string(models.NormalizeStrategy(req.Strategy, false))

	return req
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

func driveFileIDFromClip(clip *models.MediaAsset) string {
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
