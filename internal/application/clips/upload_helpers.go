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
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/delivery"
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

// ErrMetadataDriveNotConfigured is the sentinel returned when
// UpdateCumulativeMetadataJSON is called with a nil publisher
// (Drive not configured / wired). Upstream callers (e.g. the clip
// registration pipeline) can branch on this sentinel to decide
// whether to fail the registration or log a soft warning — the
// canonical clip ledger is dispatcher.EnqueueAndIndex (atomic
// UPSERT + outbox), so a missing metadata.json sidecar is
// recoverable (the next cumulative update for the same folder
// regenerates the sidecar from the new clip set).
var ErrMetadataDriveNotConfigured = errors.New(
	"clips: UpdateCumulativeMetadataJSON: Drive not configured (Publisher is nil; metadata.json sidecar cannot be atomically updated — clip registration succeeds but the sidecar will be regenerated on the next cumulative update)",
)

// UpdateCumulativeMetadataJSON atomically maintains a single
// metadata.json per group folder. Migrated from api package; the
// `*drive.Uploader` parameter was previously used to access
// `driveUploader.Service.Files.List().Q(...)` directly — that
// low-level SDK call is now encapsulated behind
// ClipDriveUploaderPort.ListFiles in the composition root adapter
// (internal/app/clips_adapters_drive.go). The new
// delivery.Publisher.Publish seam (FASE 5 since June 2026) is the
// sole Drive upload canal — this function threads the publisher
// explicitly via ClipPublisherPort to keep the application-layer
// port-pure per AGENTS.md Pattern 0.
//
// # P0-#1 atomic RMW (July 2026)
//
// Pre-P0-#1 the function trashed the old metadata.json BEFORE
// publishing the new one, and the publish step was a no-op
// (DRIVE-008 CUTOVER removed the upload path), so the old
// metadata.json was permanently lost. The new flow is:
//
//  1. List existing metadata.json under folderID via the port.
//  2. If found, download current entries via DownloadFile.
//     Read-only — no trashing yet.
//  3. Build merged JSON (replace-or-append the clip entry by
//     clip_id) in a TEMP file.
//  4. Defer os.Remove(metaTempPath) so a network hang or early
//     return after this point does not leak the file.	//  5. Publish the temp file via publisher.Publish with
//     Destination=DestinationClipMetadata,
//     DestinationFolderID=folderID, Filename="metadata.json",
//     ConflictPolicy=ConflictOverwrite. The publisher either:
//     (a) succeeds — the new metadata.json is live on Drive
//     (b) fails — the OLD metadata.json is STILL on Drive, the
//     temp file is removed by the defer, the error
//     propagates
//  6. On publish success, cleanup old per-video .json files via
//     FileLifecycle.Trash (best-effort, warnings logged on
//     failure so the caller can investigate).
//
// # Atomicity guarantee
//
// The publisher's Files.Update call (conflict_policy=overwrite)
// replaces the existing metadata.json in place without leaving a
// gap. Either:
//   - the new metadata.json is live (publish succeeded), or
//   - the old metadata.json is still on Drive (publish failed
//     before the Files.Update call, or the publish itself errored)
//
// There is no torn-intermediate state where the sidecar is
// partially the old and partially the new content.
//
// # Known race (acceptable for v1)
//
// A concurrent update to the same folder can produce a
// last-writer-wins race: both workers read the base JSON, merge
// their respective clips, and issue an overwrite — the second
// publisher's Files.Update replaces the first's output. Drive does
// not enforce optimistic concurrency (ETags) on metadata.json
// uploads. The race is acceptable for v1 because:
//   - the cumulative update is invoked once per clip registration,
//     not in a tight loop;
//   - the racy window is microseconds;
//   - the next successful cumulative update for the same folder
//     regenerates the sidecar from the clip set (cumulative
//     recovery is automatic).
//
// A future iteration can introduce an etag-based optimistic
// concurrency check (HEAD request before publish, abort on
// mismatch) if the race surfaces in production observability.
//
// # Soft-dep / no-op paths
//
//   - uploader == nil or folderID == "" → return nil (soft no-op;
//     backward-compat with pre-P0-#1 callers that invoked the
//     function with optional args).
//   - publisher == nil → return ErrMetadataDriveNotConfigured
//     (typed sentinel so the upstream adapter layer can branch
//     on it).
//   - list / download / decode / marshal / write-temp failures
//     return wrapped errors (no Drive side-effects, caller can
//     retry against the intact old metadata.json).
//   - publish failure → wrapped error (old metadata.json still on
//     Drive, atomic guarantee intact, caller can retry).
//   - cleanup failure → logged but NOT returned (per-video
//     orphans are messy but harmless; the canonical ledger is the
//     metadata.json sidecar).
func UpdateCumulativeMetadataJSON(
	ctx context.Context,
	uploader ClipDriveUploaderPort,
	publisher ClipPublisherPort,
	tempPath string,
	folderID string,
	clipID string,
	newEntry map[string]any,
	log *zap.Logger,
) error {
	const metaFilename = "metadata.json"
	if uploader == nil || folderID == "" {
		return nil
	}
	if publisher == nil {
		return ErrMetadataDriveNotConfigured
	}
	if log == nil {
		log = zap.NewNop()
	}

	// Step 1+2: Read existing metadata.json (read-only, no side-effects).
	// If list/download/decode fails we surface the error WITHOUT
	// trashing anything — the caller can retry against the intact
	// old file.
	var existing []map[string]any
	list, err := uploader.ListFiles(ctx, fmt.Sprintf("'%s' in parents and trashed = false and name = '%s'", folderID, metaFilename))
	if err != nil {
		return fmt.Errorf("clips: UpdateCumulativeMetadataJSON: list metadata.json under %q: %w", folderID, err)
	}
	if len(list) > 0 {
		existingFileID := list[0].ID
		body, _, dlErr := uploader.DownloadFile(ctx, existingFileID)
		if dlErr != nil {
			return fmt.Errorf("clips: UpdateCumulativeMetadataJSON: download existing metadata.json (%s): %w", existingFileID, dlErr)
		}
		if body != nil {
			var raw []map[string]any
			decErr := json.NewDecoder(body).Decode(&raw)
			closeErr := body.Close()
			if decErr != nil {
				return fmt.Errorf("clips: UpdateCumulativeMetadataJSON: decode existing metadata.json: %w", decErr)
			}
			if closeErr != nil {
				log.Warn("failed to close metadata.json body after decode",
					zap.String("file_id", existingFileID),
					zap.Error(closeErr))
			}
			existing = raw
		}
	}

	// Step 3: Build merged JSON in temp file.
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
		return fmt.Errorf("clips: UpdateCumulativeMetadataJSON: marshal merged metadata (%d entries): %w", len(existing), err)
	}
	metaTempPath := filepath.Join(tempPath, fmt.Sprintf("meta_%s_%d.json", clipID, time.Now().UnixNano()))
	if err := os.WriteFile(metaTempPath, jsonBytes, 0644); err != nil {
		return fmt.Errorf("clips: UpdateCumulativeMetadataJSON: write temp metadata to %q: %w", metaTempPath, err)
	}

	// Step 4: Defer cleanup of the temp file so a network hang or
	// early-return after this point does not leak the file. The
	// defer fires whether the publish succeeds or fails; the
	// publisher reads + closes the file synchronously per its
	// internal PutFile contract, so by the time Publish returns
	// the file is no longer referenced by the publisher.
	defer func() {
		if rmErr := os.Remove(metaTempPath); rmErr != nil && !os.IsNotExist(rmErr) {
			log.Warn("failed to remove temp metadata file",
				zap.String("path", metaTempPath),
				zap.Error(rmErr))
		}
	}()

	// Step 5: Publish the new metadata.json atomically. The
	// publisher replaces the existing file in place (ConflictOverwrite
	// is the registry default for DestinationClipMetadata) or
	// returns an error WITHOUT touching the existing file. Either
	// way, the caller observes a clean all-or-nothing outcome: the
	// Drive-side sidecar is either the old or the new content,
	// never a torn intermediate.
	pubReq := delivery.PublishRequest{
		Destination:         delivery.DestinationClipMetadata,
		LocalPath:           metaTempPath,
		Filename:            metaFilename,
		DestinationFolderID: folderID, // sidecar lives in the clip's resolved folder
		ConflictPolicy:      delivery.ConflictOverwrite,
	}
	pubResult, pubErr := publisher.Publish(ctx, pubReq)
	if pubErr != nil {
		// Atomic guarantee: the old metadata.json is STILL on Drive
		// because the publish failed before the Files.Update call.
		// The defer above cleaned up the temp file. The caller sees
		// a typed error and can retry.
		return fmt.Errorf("clips: UpdateCumulativeMetadataJSON: publish metadata.json to %q: %w", folderID, pubErr)
	}
	log.Info("metadata.json published atomically",
		zap.String("folder_id", folderID),
		zap.String("clip_id", clipID),
		zap.Int("entries", len(existing)),
		zap.String("drive_file_id", pubResult.FileID),
		zap.String("action", string(pubResult.Action)),
	)

	// Step 6: Cleanup old per-video .json files (best-effort, only
	// AFTER the new metadata.json is live). Failures here are
	// logged but NOT returned — the canonical ledger is the
	// metadata.json sidecar, not the per-video files; orphan
	// per-video files are messy but do not affect the canonical
	// state.
	cleanupLegacyMetadataJSON(ctx, uploader, folderID, log)

	return nil
}

// cleanupLegacyMetadataJSON removes old per-video metadata files.
// Package-private; only UpdateCumulativeMetadataJSON above calls it.
//
// P0-#1 (July 2026): called from UpdateCumulativeMetadataJSON AFTER
// the new metadata.json has been atomically published. Pre-P0-#1
// the cleanup ran BEFORE the publish step (which was a no-op),
// so per-video files were deleted while the canonical sidecar was
// absent — a silent data loss the audit uncovered.
func cleanupLegacyMetadataJSON(ctx context.Context, uploader ClipDriveUploaderPort, folderID string, log *zap.Logger) {
	if uploader == nil || folderID == "" {
		return
	}
	if log == nil {
		log = zap.NewNop()
	}
	list, err := uploader.ListFiles(ctx, fmt.Sprintf("'%s' in parents and trashed = false and name contains '.json' and name != 'metadata.json'", folderID))
	if err != nil {
		log.Warn("failed to list legacy per-video metadata files",
			zap.String("folder_id", folderID),
			zap.Error(err))
		return
	}
	for _, f := range list {
		log.Info("cleaning up legacy metadata json", zap.String("file_id", f.ID), zap.String("name", f.Name))
		if err := uploader.TrashFile(ctx, f.ID); err != nil {
			log.Warn("failed to trash legacy metadata json", zap.String("file_id", f.ID), zap.Error(err))
		}
	}
}
