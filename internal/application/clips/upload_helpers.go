// Package clips (upload_helpers) — port-typed helpers supporting the
// upload + cumulative-metadata workflow.
//
// Wave 14 PR2 (June 2026): migrated from internal/api/assets/clips/upload_helpers.go.
// The previous file's only non-pure dependency was `*drive.Uploader`
// (concrete google drive SDK), required by
// UpdateCumulativeMetadataJSON's drive search call. The replacement
// uses the canonical typed port ClipDriveUploaderPort (declared in
// ports.go) so this file imports zero infrastructure packages. The
// remaining helpers (ExtractDriveFolderID, CleanFolderName,
// BuildDriveDescription) are pure string functions and have always
// been infra-free — they only migrated to follow the Wave 14 PR2
// rule "API package files must not import any internal/infrastructure/*".
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
// clip_upload flows (clips upload_helpers) and the legacy
// register_from_youtube flow (sources package). Exported so callers
// cross-package can use it without an alias.
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

// CleanFolderName normalizes a folder name for comparison.
func CleanFolderName(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, "-", "")
	s = strings.ReplaceAll(s, "_", "")
	s = strings.ReplaceAll(s, " ", "")
	return s
}

// BuildDriveDescription builds a description string for the Drive file.
// Pure function: no-op on migration, signature unchanged from the
// api-side copy.
func BuildDriveDescription(name, reqDescription, metaDescription string, tags []string, category, source, urlVal, videoID string) string {
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
	if urlVal != "" {
		parts = append(parts, fmt.Sprintf("URL: %s", urlVal))
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
// folder. Migrated from api package; the `*drive.Uploader` parameter
// was previously used to access `driveUploader.Service.Files.List().Q(...)`
// directly — that low-level SDK call is now encapsulated behind
// ClipDriveUploaderPort.ListFiles in the composition root adapter
// (internal/app/clips_adapters_drive.go). The port returns a slice
// of ClipDriveFileDTO; we use it identically here.
//
// # Behavior
//   - Lists existing metadata.json under folderID via the port.
//   - If found, downloads current entries, replaces-or-appends the clip
//     entry by clip_id, trashes the old file.
//   - Marshals the merged set to a temp file, uploads to the folder as
//     metadata.json, removes the temp.
//   - Cleans up any older per-video .json files in the same folder.
//
// cleanupLegacyMetadataJSON is package-private since no caller outside
// this file needs it post-refactor.
func UpdateCumulativeMetadataJSON(
	ctx context.Context,
	uploader ClipDriveUploaderPort,
	tempPath string,
	folderID string,
	clipID string,
	newEntry map[string]interface{},
	log *zap.Logger,
) {
	const metaFilename = "metadata.json"
	if uploader == nil || folderID == "" {
		return
	}
	if log == nil {
		log = zap.NewNop()
	}

	var existing []map[string]interface{}
	list, err := uploader.ListFiles(ctx, fmt.Sprintf("'%s' in parents and trashed = false and name = '%s'", folderID, metaFilename))
	if err != nil {
		log.Warn("failed to list metadata.json", zap.Error(err))
	} else if len(list) > 0 {
		existingFileID := list[0].ID
		body, _, dlErr := uploader.DownloadFile(ctx, existingFileID)
		if dlErr == nil && body != nil {
			defer body.Close()
			var raw []map[string]interface{}
			if decErr := json.NewDecoder(body).Decode(&raw); decErr == nil {
				existing = raw
			}
		}
		if err := uploader.TrashFile(ctx, existingFileID); err != nil {
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
	// DRIVE-008 CUTOVER (July 2026): UploadFile removed from ClipDriveUploaderPort.
	// The metadata.json upload has been degraded since P2.2 (DRIVE-008 fail-closed stub).
	// A future wave can reintroduce the upload via delivery.Publisher.Publish when a
	// dedicated DestinationKey (e.g. DestinationClipMetadata) is added to the registry.
	log.Debug("metadata.json upload skipped — legacy surface retired (DRIVE-008 CUTOVER)",
		zap.String("clip_id", clipID),
		zap.Int("entries", len(existing)),
	)
	os.Remove(metaTempPath)

	cleanupLegacyMetadataJSON(ctx, uploader, folderID, log)
}

// cleanupLegacyMetadataJSON removes old per-video metadata files.
// Package-private; only UpdateCumulativeMetadataJSON above calls it.
func cleanupLegacyMetadataJSON(ctx context.Context, uploader ClipDriveUploaderPort, folderID string, log *zap.Logger) {
	if uploader == nil || folderID == "" {
		return
	}
	if log == nil {
		log = zap.NewNop()
	}
	list, err := uploader.ListFiles(ctx, fmt.Sprintf("'%s' in parents and trashed = false and name contains '.json' and name != 'metadata.json'", folderID))
	if err != nil {
		return
	}
	for _, f := range list {
		log.Info("cleaning up legacy metadata json", zap.String("file_id", f.ID), zap.String("name", f.Name))
		if err := uploader.TrashFile(ctx, f.ID); err != nil {
			log.Warn("failed to trash legacy metadata json", zap.String("file_id", f.ID), zap.Error(err))
		}
	}
}
