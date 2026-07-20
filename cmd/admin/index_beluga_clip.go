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

const belugaClipDriveID = "1dQPRaUjyqRCjKrH6F03aQ6rcZ5Qopqnv"

func runIndexBelugaClip(args []string) error {
	fs := flag.NewFlagSet("index-beluga-clip", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	driveID := fs.String("drive-file-id", belugaClipDriveID, "Google Drive file ID")
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
		filename = "This beluga knew exactly what it was doing to that poor kid rAni.mp4"
	}
	localDir := cfg.Storage.FullPath(filepath.Join(cfg.Storage.MediaDir, "viral_videos"))
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
		// fallback to 9 seconds if ffprobe fails
		duration = 9 * time.Second
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
	clip.Name = "Beluga knew exactly what it was doing to that poor kid"
	clip.Filename = filename
	clip.Source = asset.Source("clip_drive")
	clip.MediaType = asset.MediaType("video")
	clip.Category = "viral_videos"
	clip.Group = "funny_animals"
	clip.Duration = duration
	clip.Tags = []string{
		"beluga", "whale", "funny", "aquarium", "prank", "kid", "scare", "funny animals", "viral", "video",
	}
	clip.SearchTerms = append([]string{}, clip.Tags...)
	clip.SearchText = strings.Join([]string{
		clip.Name,
		"A calm encounter at the aquarium... Look who is coming! But the beluga has other plans... BOO! Prank perfectly executed! Childhood trauma at the aquarium",
		"beluga whale scaring kid at aquarium prank funny animal video",
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
	clip.SetMetadataString("content_type", "viral video")
	clip.SetMetadataString("subject", "beluga whale / kid")
	clip.SetMetadataString("visual_summary", "A beluga whale swim towards a kid, turns upside down and suddenly opens its mouth to scare the child, making him fall and run away crying.")
	clip.SetMetadataString("timeline_json", `[{"start":"00:00","end":"00:02","visual":"A young child stands in front of the large window of an ocean aquarium, looking at the blue water and pointing as a large white beluga whale swims calmly toward him from the right.","sfx_family":"background_music/ambient_sound"},{"start":"00:03","end":"00:05","visual":"The beluga positions itself directly in front of the child, turns belly up, and suddenly opens its mouth wide to simulate an attack and intentionally scare him.","sfx_family":"audio_control/impact_sfx"},{"start":"00:06","end":"00:09","visual":"The child gets very scared, jumps backward, loses balance, falls to the ground, and runs away screaming and crying in terror, while another girl watches the scene and someone laughs in the background. The beluga swims away, proud of its prank.","sfx_family":"meme_vocal_effect/comedy_sfx"}]`)
	if err := root.Outbox.Dispatcher.EnqueueAndIndex(ctx, clip, hash); err != nil {
		return fmt.Errorf("save and index beluga clip: %w", err)
	}
	if err := waitForAssetIndexOutbox(ctx, root, deadLettersBefore); err != nil {
		return err
	}
	fmt.Printf("Beluga clip indexed: id=%s name=%s duration=%.3fs local=%s\n", clip.ID, clip.Name, duration.Seconds(), localPath)
	return nil
}
