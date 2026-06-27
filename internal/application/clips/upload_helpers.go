package clips

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
)

var driveFolderIDRegex = regexp.MustCompile(`/folders/([a-zA-Z0-9_-]+)`)

// ExtractDriveFolderID extracts the folder ID from a Google Drive URL or
// returns the input unchanged if it's already a raw ID. Used by both
// clip_upload.go (clips package) and the legacy register_from_youtube.go
// (sources package) — exporting it lets both consume the same impl.
func ExtractDriveFolderID(input string) string {
	if input == "" {
		return ""
	}
	if strings.HasPrefix(input, "http://") || strings.HasPrefix(input, "https://") {
		if parsed, err := url.Parse(input); err == nil {
			if matches := driveFolderIDRegex.FindStringSubmatch(parsed.Path); len(matches) > 1 {
				return matches[1]
			}
		}
	}
	return input
}

// CleanFolderName normalizes a folder name for comparison. Exposed as
// CleanFolderName so callers cross-package can use it without an alias.
func CleanFolderName(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, "-", "")
	s = strings.ReplaceAll(s, "_", "")
	s = strings.ReplaceAll(s, " ", "")
	return s
}

// BuildDriveDescription builds a description string for the Drive file.
//
// # PR-A Phase 4 BULK note
//
// This was previously `func buildDriveDescription` lowercase in
// sources/; cross-package consumption forced renaming to uppercase. The
// signature is unchanged.
func BuildDriveDescription(name, reqDescription, metaDescription string, tags []string, category, source, url, videoID string) string {
	var parts []string

	parts = append(parts, fmt.Sprintf("Name: %s", name))

	if category != "" {
		parts = append(parts, fmt.Sprintf("Category: %s", category))
	}
	if source != "" {
		parts = append(parts, fmt.Sprintf("Source: %s", source))
	}
	if videoID != "" {
		parts = append(parts, fmt.Sprintf("YouTube ID: %s", videoID))
	}
	if url != "" {
		parts = append(parts, fmt.Sprintf("URL: %s", url))
	}

	desc := reqDescription
	if desc == "" {
		desc = metaDescription
	}
	if desc != "" {
		if len(desc) > 500 {
			desc = desc[:500] + "..."
		}
		parts = append(parts, fmt.Sprintf("Description: %s", desc))
	}

	if len(tags) > 0 {
		parts = append(parts, fmt.Sprintf("Tags: %s", strings.Join(tags, ", ")))
	}

	return strings.Join(parts, "\n")
}

// UpdateCumulativeMetadataJSON maintains a single metadata.json per group
// folder. Refactored in PR-A Phase 4 BULK from a
// `func (h *Handler) updateCumulativeMetadataJSON` method on
// *sources.Handler to a free function so both packages (clips via
// clip_upload.go, sources via register_from_youtube.go) can pass their
// own driveUploader + tempPath instead of depending on a shared receiver.
//
// W14-PR2 slice 6 (June 2026): driveUploader parameter changed from
// *drive.Uploader to ClipDriveUploaderPort so the api/ layer
// never imports internal/infrastructure/drive. The narrow projection
// already lives in the adapter (see clipsDriveAdapter.ListFiles +
// ClipDriveFileDTO). The caller still passes a port-backed value.
//
// cleanupLegacyMetadataJSON is left package-private internal to the body
// here; if a third caller appears, hoist it next to this function and
// rename to CleanupLegacyMetadataJSON.
//
// # Behavior
//   - Lists existing metadata.json under folderID via Drive search.
//   - If found, downloads current entries, replaces-or-appends the clip
//     entry by clip_id, trashes the old file.
//   - Marshals the merged set to a temp file, upload to the folder as
//     metadata.json, removes the temp.
//   - Cleans up any older per-video .json files in the same folder.
func UpdateCumulativeMetadataJSON(
	ctx context.Context,
	driveUploader ClipDriveUploaderPort,
	tempPath string,
	folderID string,
	clipID string,
	newEntry map[string]interface{},
	log *zap.Logger,
) {
	const metaFilename = "metadata.json"
	if driveUploader == nil || folderID == "" {
		return
	}

	var existing []map[string]interface{}
	query := fmt.Sprintf("'%s' in parents and trashed = false and name = '%s'", folderID, metaFilename)
	list, err := driveUploader.ListFiles(ctx, query)
	if err != nil {
		log.Warn("failed to list metadata.json", zap.Error(err))
	} else if len(list) > 0 {
		existingFileID := list[0].ID
		// Reconstruct the metadata.json filename. The adapter narrows the
		// projection to {ID, Name} so we rederive the name from the
		// query's constant (it is always "metadata.json" per convention).
		// We still need the name to satisfy the DownloadFile flow.
		_ = metaFilename
		body, _, dlErr := driveUploader.DownloadFile(ctx, existingFileID)
		if dlErr == nil && body != nil {
			defer body.Close()
			var raw []map[string]interface{}
			if decErr := json.NewDecoder(body).Decode(&raw); decErr == nil {
				existing = raw
			}
		}
		if err := driveUploader.TrashFile(ctx, existingFileID); err != nil {
			log.Warn("failed to trash old metadata.json", zap.Error(err))
		}
	}

	found := false
	for i, entry := range existing {
		if id, ok := entry["clip_id"].(string); ok && id == clipID {
			existing[i] = newEntry
			found = true
			break
		}
	}
	if !found {
		existing = append(existing, newEntry)
	}

	jsonBytes, err := json.MarshalIndent(existing, "", "  ")
	if err != nil {
		log.Warn("failed to marshal cumulative metadata json", zap.Error(err))
		return
	}
	metaTempPath := filepath.Join(tempPath, fmt.Sprintf("meta_%s_%d.json", clipID, time.Now().UnixNano()))
	if err := os.WriteFile(metaTempPath, jsonBytes, 0644); err != nil {
		log.Warn("failed to write metadata json temp file", zap.Error(err))
		return
	}
	if _, err := driveUploader.UploadFile(ctx, metaTempPath, folderID, metaFilename); err != nil {
		log.Warn("failed to upload metadata.json to Drive", zap.Error(err))
	} else {
		log.Info("uploaded cumulative metadata.json to Drive", zap.Int("entries", len(existing)))
	}
	os.Remove(metaTempPath)

	cleanupLegacyMetadataJSON(ctx, driveUploader, folderID, log)
}

// cleanupLegacyMetadataJSON removes old per-video metadata files.
// Internal to this file; package-private since no caller outside this
// function needs it post-refactor.
//
// W14-PR2 slice 6 (June 2026): signature mirrors UpdateCumulativeMetadataJSON —
// takes the typed port instead of *drive.Uploader.
func cleanupLegacyMetadataJSON(ctx context.Context, driveUploader ClipDriveUploaderPort, folderID string, log *zap.Logger) {
	if driveUploader == nil || folderID == "" {
		return
	}
	query := fmt.Sprintf("'%s' in parents and trashed = false and name contains '.json' and name != 'metadata.json'", folderID)
	list, err := driveUploader.ListFiles(ctx, query)
	if err != nil {
		return
	}
	for _, f := range list {
		log.Info("cleaning up legacy metadata json", zap.String("file_id", f.ID), zap.String("name", f.Name))
		if err := driveUploader.TrashFile(ctx, f.ID); err != nil {
			log.Warn("failed to trash legacy metadata json", zap.String("file_id", f.ID), zap.Error(err))
		}
	}
}
