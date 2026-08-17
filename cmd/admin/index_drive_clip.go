// cmd/admin/index_drive_clip.go — Sprint 2.2
//
// Replaces the per-animal hardcoded indexers retired in this sprint:
//   - cmd/admin/index_beluga_clip.go     (runIndexBelugaClip)
//   - cmd/admin/index_drive_fish_clip.go (runIndexDriveFishClip)
//
// Operational data (Drive ID, name, tags, description, timeline,
// fallback policy) is now supplied by a JSON manifest under
// cmd/admin/manifests/*.json. The CLI surface is intentionally narrow:
//
//	admin index-drive-clip --manifest <path.json>
//	admin index-drive-clip --manifest <path.json> --drive-file-id <override>
//
// Behaviour preserved from the retired commands:
//   - Drive metadata fetch + non-trashed guard
//   - Local download under cfg.Storage.MediaDir/<local_subdir|category>
//   - ffprobe duration + per-manifest fallback policy
//   - sha256 of the downloaded file
//   - Asset population: Name, Filename, Source, MediaType, Category,
//     Group, Duration, Tags, SearchTerms, SearchText, LifecycleState,
//     DriveFileID, DriveLink, DownloadLink, LocalPath, FileHash,
//     MetadataString (mime_type + free-form manifest.Metadata)
//   - Dispatcher.EnqueueAndIndex(ctx, clip, hash) + waitForAssetIndexOutbox
//
// Sprint 1.2 (per-event wait + IndexAssetAndWait application service)
// is intentionally out of scope here: the goal of Sprint 2.2 is to
// retire the hardcoded files; the global-count wait and the
// per-command EventsPool.Start remain in place until Sprint 1.2 lands.

package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/app"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/media/rustexec"
)

func runIndexDriveClip(args []string) error {
	fs := flag.NewFlagSet("index-drive-clip", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	manifestPath := fs.String("manifest", "", "Path to the index-clip JSON manifest (required)")
	driveIDOverride := fs.String("drive-file-id", "", "Override the manifest's drive_file_id (optional)")
	allowDeclared := fs.Bool("allow-declared-duration", false, "Permit using the manifest's duration_fallback_seconds when ffprobe fails. The asset is then tagged metadata.duration_source=declared_fallback. Without this flag, probe failure fails closed.")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*manifestPath) == "" {
		return fmt.Errorf("--manifest is required")
	}

	manifest, err := loadIndexClipManifest(*manifestPath)
	if err != nil {
		return fmt.Errorf("load manifest %s: %w", *manifestPath, err)
	}

	driveID := strings.TrimSpace(*driveIDOverride)
	if driveID == "" {
		driveID = strings.TrimSpace(manifest.DriveFileID)
	}
	if driveID == "" {
		return fmt.Errorf("drive_file_id is required (set in manifest or pass --drive-file-id)")
	}

	cfg, log, cleanup, err := appLogger()
	if err != nil {
		return err
	}
	defer cleanup()
	root, _, rootCleanup, err := app.InitComposition(cfg, log)
	if err != nil {
		return fmt.Errorf("initialize composition: %w", err)
	}
	defer rootCleanup()
	if root == nil || root.Drive == nil || root.Drive.Reader == nil || root.Repos == nil || root.Repos.ClipsRepo == nil || root.Outbox == nil || root.Outbox.Dispatcher == nil || root.Outbox.EventsPool == nil || root.Outbox.EventsRepo == nil {
		return fmt.Errorf("Drive reader, clips repository and outbox dispatcher are required")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	deadLettersBefore, err := root.Outbox.EventsRepo.CountByEventTypeAndStatus(ctx, "asset.index.requested", "dead_letter")
	if err != nil {
		return fmt.Errorf("read outbox baseline: %w", err)
	}
	go root.Outbox.EventsPool.Start(ctx, 1)
	defer func() { _ = root.Outbox.EventsPool.Stop(15 * time.Second) }()

	meta, err := root.Drive.Reader.GetFileMeta(ctx, driveID)
	if err != nil {
		return fmt.Errorf("read Drive metadata: %w", err)
	}
	if meta == nil || meta.Trashed {
		return fmt.Errorf("Drive file is missing or trashed")
	}

	filename := strings.TrimSpace(meta.Name)
	if filename == "" {
		filename = strings.TrimSpace(manifest.DefaultFilename)
	}
	if filename == "" {
		return fmt.Errorf("Drive file name is empty and manifest does not provide default_filename")
	}

	localSubdir := strings.TrimSpace(manifest.LocalSubdir)
	if localSubdir == "" {
		localSubdir = strings.TrimSpace(manifest.Category)
	}
	if localSubdir == "" {
		return fmt.Errorf("manifest must declare local_subdir or category")
	}
	localDir := cfg.Storage.FullPath(filepath.Join(cfg.Storage.MediaDir, localSubdir))
	if err := os.MkdirAll(localDir, 0o755); err != nil {
		return fmt.Errorf("create local directory: %w", err)
	}
	localPath := filepath.Join(localDir, filepath.Base(filename))

	reader, _, err := root.Drive.Reader.DownloadFile(ctx, driveID)
	if err != nil {
		return fmt.Errorf("download Drive clip: %w", err)
	}
	defer reader.Close()
	out, err := os.Create(localPath)
	if err != nil {
		return fmt.Errorf("create local clip: %w", err)
	}
	if _, err := io.Copy(out, reader); err != nil {
		_ = out.Close()
		return fmt.Errorf("save local clip: %w", err)
	}
	if err := out.Close(); err != nil {
		return fmt.Errorf("close local clip: %w", err)
	}

	prober := rustexec.NewVideoProcessor(cfg.External.RustMusclesPath, cfg.External.FfmpegPath, log)
	probeDuration, probeErr := probeSoundEffectDuration(ctx, prober, localPath)
	duration, durationSource, err := resolveClipDuration(probeDuration, probeErr, manifest.DurationFallbackSeconds, *allowDeclared)
	if err != nil {
		return err
	}

	hash, err := sha256File(localPath)
	if err != nil {
		return fmt.Errorf("hash clip: %w", err)
	}

	clip, err := root.Repos.ClipsRepo.GetClip(ctx, driveID)
	if err != nil {
		return fmt.Errorf("look up existing clip: %w", err)
	}
	now := time.Now().UTC()
	if clip == nil {
		clip = &asset.Asset{ID: driveID, CreatedAt: now}
	}
	clip.Name = manifest.Name
	clip.Filename = filename
	clip.Source = asset.Source(manifest.Source)
	clip.MediaType = asset.MediaType("video")
	clip.Category = manifest.Category
	clip.Group = manifest.Group
	clip.Duration = duration
	clip.Tags = append([]string{}, manifest.Tags...)
	clip.SearchTerms = append([]string{}, clip.Tags...)

	searchTextParts := []string{clip.Name, manifest.Description}
	if alt := strings.TrimSpace(manifest.DescriptionAlt); alt != "" {
		searchTextParts = append(searchTextParts, alt)
	}
	searchTextParts = append(searchTextParts, strings.Join(clip.Tags, " "))
	clip.SearchText = strings.Join(searchTextParts, " ")

	clip.LifecycleState = asset.StateActive
	clip.UpdatedAt = now
	clip.SetDriveFileID(driveID)
	clip.SetDriveLink("https://drive.google.com/file/d/" + driveID + "/view")
	clip.SetDownloadLink("https://drive.google.com/uc?export=download&id=" + driveID)
	clip.SetLocalPath(localPath)
	clip.SetFileHash(hash)
	clip.SetMetadataString("mime_type", meta.MimeType)
	// duration_source tags how the duration above was obtained:
	//   "measured"           — ffprobe succeeded
	//   "declared_fallback"  — probe failed and the manifest's
	//                          duration_fallback_seconds was honoured
	//                          via --allow-declared-duration
	// The key is set unconditionally so downstream consumers can
	// filter on it without inspecting the asset's history.
	clip.SetMetadataString("duration_source", durationSource)
	for k, v := range manifest.Metadata {
		clip.SetMetadataString(k, v)
	}

	if err := root.Outbox.Dispatcher.EnqueueAndIndex(ctx, clip, hash); err != nil {
		return fmt.Errorf("save and index drive clip: %w", err)
	}
	if err := waitForAssetIndexOutbox(ctx, root, deadLettersBefore); err != nil {
		return err
	}
	fmt.Printf("Drive clip indexed: id=%s name=%s duration=%.3fs duration_source=%s local=%s manifest=%s\n",
		clip.ID, clip.Name, duration.Seconds(), durationSource, localPath, *manifestPath)
	return nil
}

// resolveClipDuration decides which duration to use for the indexed clip
// without ever producing a silent false success on probe failure.
//
// Behaviour:
//   - probeErr == nil and probeDur > 0:
//     return (probeDur, "measured", nil)
//   - probeErr == nil and probeDur <= 0:
//     return error (degenerate probe result, e.g. empty path)
//   - probeErr != nil, fallbackSeconds <= 0:
//     return error (no declared fallback to honour)
//   - probeErr != nil, fallbackSeconds > 0, !allowDeclared:
//     return error mentioning --allow-declared-duration
//   - probeErr != nil, fallbackSeconds > 0, allowDeclared:
//     return (fallbackSeconds * time.Second, "declared_fallback", nil)
//
// The caller MUST fail closed on any error returned here; never swallow
// it and substitute a hardcoded value.
func resolveClipDuration(probeDur time.Duration, probeErr error, fallbackSeconds int, allowDeclared bool) (time.Duration, string, error) {
	if probeErr == nil {
		if probeDur <= 0 {
			return 0, "", fmt.Errorf("probe returned non-positive duration %s (degenerate result; refusing to index)", probeDur)
		}
		return probeDur, "measured", nil
	}
	if fallbackSeconds <= 0 {
		return 0, "", fmt.Errorf("probe clip duration: %w", probeErr)
	}
	if !allowDeclared {
		return 0, "", fmt.Errorf("probe clip duration failed and manifest declares fallback (%ds); pass --allow-declared-duration to honour it: %w", fallbackSeconds, probeErr)
	}
	return time.Duration(fallbackSeconds) * time.Second, "declared_fallback", nil
}
