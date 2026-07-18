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
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
)

const fishClipDriveID = "19zV7kxyINQeMGP68VxlhuvWCRJUX-bc4"

// runIndexDriveFishClip ingests one Drive fish clip with the human-authored
// timeline metadata supplied for the current TopFive test.
func runIndexDriveFishClip(args []string) error {
	fs := flag.NewFlagSet("index-drive-fish-clip", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	driveID := fs.String("drive-file-id", fishClipDriveID, "Google Drive file ID")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*driveID) == "" {
		return fmt.Errorf("drive-file-id is required")
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
	meta, err := root.Drive.Reader.GetFileMeta(ctx, strings.TrimSpace(*driveID))
	if err != nil {
		return fmt.Errorf("read Drive metadata: %w", err)
	}
	if meta == nil || meta.Trashed {
		return fmt.Errorf("Drive file is missing or trashed")
	}
	filename := strings.TrimSpace(meta.Name)
	if filename == "" {
		filename = "stargazer-fish-underwater.mp4"
	}
	localDir := cfg.Storage.FullPath(filepath.Join(cfg.Storage.MediaDir, "fish_shorts"))
	if err := os.MkdirAll(localDir, 0o755); err != nil {
		return fmt.Errorf("create local directory: %w", err)
	}
	localPath := filepath.Join(localDir, filepath.Base(filename))
	reader, _, err := root.Drive.Reader.DownloadFile(ctx, *driveID)
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
	duration, err := probeSoundEffectDuration(ctx, localPath)
	if err != nil {
		return fmt.Errorf("probe clip duration: %w", err)
	}
	hash, err := sha256File(localPath)
	if err != nil {
		return fmt.Errorf("hash clip: %w", err)
	}

	clip, err := root.Repos.ClipsRepo.GetClip(ctx, *driveID)
	if err != nil {
		return fmt.Errorf("look up existing clip: %w", err)
	}
	now := time.Now().UTC()
	if clip == nil {
		clip = &asset.Asset{ID: *driveID, CreatedAt: now}
	}
	clip.Name = "Stargazer Fish Revealed Underwater – Sand Ambush"
	clip.Filename = filename
	clip.Source = asset.Source("ai_generated")
	clip.MediaType = asset.MediaType("video")
	clip.Category = "Ocean"
	clip.Group = "topfive_fish"
	clip.Duration = duration
	clip.Tags = []string{
		"fish", "stargazer", "stargazer fish", "pesce prete", "underwater", "subacqueo",
		"ocean", "sand", "sabbia", "buried animal", "surprise reveal", "hook", "shorts",
	}
	clip.SearchTerms = append([]string{}, clip.Tags...)
	clip.SearchText = strings.Join([]string{
		clip.Name,
		"stargazer fish hidden under sand underwater hand digging sudden reveal flat face eyes looking up bizarre marine creature",
		"subacqueo fondale sabbioso mano scava nube di sabbia pesce mimetico rivelazione sorpresa hook video short",
		strings.Join(clip.Tags, " "),
	}, " ")
	clip.LifecycleState = asset.StateActive
	clip.UpdatedAt = now
	clip.SetDriveFileID(*driveID)
	clip.SetDriveLink("https://drive.google.com/file/d/" + *driveID + "/view")
	clip.SetDownloadLink("https://drive.google.com/uc?export=download&id=" + *driveID)
	clip.SetLocalPath(localPath)
	clip.SetFileHash(hash)
	clip.SetMetadataString("mime_type", meta.MimeType)
	clip.SetMetadataString("content_type", "vertical fish short")
	clip.SetMetadataString("subject", "stargazer fish / pesce prete")
	clip.SetMetadataString("visual_summary", "A hand digs into clear underwater sand and reveals a camouflaged stargazer fish staring directly at the camera.")
	clip.SetMetadataString("hook", "The sand cloud opens and an unexpected stargazer fish appears face-first.")
	clip.SetMetadataString("audio_policy", "preserve original clip audio")
	clip.SetMetadataString("sound_design_plan", "00:00-00:04 underwater ambience and sand movement; 00:05 stargazer fish reveal with one low cinematic bass impact; 00:06-00:08 natural underwater tail and clean loop cut.")
	clip.SetMetadataString("timeline_json", `[{"start":"00:00","end":"00:02","visual":"underwater sandy seabed, hand enters and begins digging","sfx_family":"ambient/foley"},{"start":"00:02","end":"00:04","visual":"sand cloud builds and hides the reveal","sfx_family":"foley"},{"start":"00:04","end":"00:05","visual":"silhouette becomes visible beneath the sand","sfx_family":"tension"},{"start":"00:05","end":"00:06","visual":"stargazer fish face is revealed","sfx_family":"impact/cinematic_bass_drop"},{"start":"00:06","end":"00:08.5","visual":"fish remains visible, underwater resolution","sfx_family":"ambient"}]`)
	if err := root.Outbox.Dispatcher.EnqueueAndIndex(ctx, clip, hash); err != nil {
		return fmt.Errorf("save and index fish clip: %w", err)
	}
	if err := waitForAssetIndexOutbox(ctx, root, deadLettersBefore); err != nil {
		return err
	}
	fmt.Printf("Fish clip indexed: id=%s name=%s duration=%.3fs local=%s\n", clip.ID, clip.Name, duration.Seconds(), localPath)
	return nil
}
