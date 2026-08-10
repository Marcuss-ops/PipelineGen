package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/app"
	"github.com/Marcuss-ops/PipelineGen/internal/app/wiring"
	"github.com/Marcuss-ops/PipelineGen/internal/application/adminmedia"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/media/rustexec"
)

// runTrimSoundEffects caps local SFX files at maxSeconds. Files already at or
// below the cap are untouched. Each changed asset gets a new hash/duration
// and is re-emitted through the canonical asset index outbox.
func runTrimSoundEffects(args []string) error {
	fs := flag.NewFlagSet("trim-sound-effects", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	maxSeconds := fs.Float64("max-seconds", 2, "maximum duration in seconds")
	dryRun := fs.Bool("dry-run", false, "report files without changing or reindexing")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *maxSeconds <= 0 {
		return fmt.Errorf("max-seconds must be positive")
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
	if root == nil || root.DB == nil || root.Repos == nil || root.Repos.ClipsRepo == nil {
		return fmt.Errorf("database and clips repository are required")
	}
	if !*dryRun && (root.Outbox == nil || root.Outbox.Dispatcher == nil) {
		return fmt.Errorf("outbox dispatcher is required unless --dry-run is used")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Hour)
	defer cancel()
	rows, err := root.DB.DB.QueryContext(ctx, `
		SELECT id FROM media_assets
		WHERE source='sound_effect' AND media_type='sound_effect' AND category='file'
		ORDER BY name, id`)
	if err != nil {
		return fmt.Errorf("list sound effects: %w", err)
	}
	defer rows.Close()

	mediaConfig := wiring.MediaexecConfig(cfg)
	mediaEditor := rustexec.NewAdminMediaProcessor(cfg.External.RustMusclesPath, cfg.External.FfmpegPath, mediaConfig.Policy, mediaConfig.Profile, log)
	changed, untouched, metadataUpdated := 0, 0, 0
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return fmt.Errorf("scan sound effect: %w", err)
		}
		clip, err := root.Repos.ClipsRepo.GetClip(ctx, id)
		if err != nil || clip == nil {
			return fmt.Errorf("load sound effect %s: %w", id, err)
		}
		localPath := clip.LocalPath()
		if strings.TrimSpace(localPath) == "" {
			return fmt.Errorf("sound effect %s has no local_path", id)
		}
		duration, err := probeSoundEffectDuration(ctx, localPath)
		if err != nil {
			return fmt.Errorf("probe %s: %w", localPath, err)
		}
		if duration <= time.Duration(*maxSeconds*float64(time.Second)) {
			if clip.Duration != duration && !*dryRun {
				clip.Duration = duration
				if err := root.Outbox.Dispatcher.EnqueueAndIndex(ctx, clip, clip.FileHash()); err != nil {
					return fmt.Errorf("refresh metadata for %s: %w", clip.Name, err)
				}
				metadataUpdated++
			}
			untouched++
			continue
		}

		if *dryRun {
			fmt.Printf("trim %.3fs -> %.3fs: %s\n", duration.Seconds(), *maxSeconds, clip.Name)
			changed++
			continue
		}

		targetSeconds := trimTargetSeconds(localPath, *maxSeconds)
		if err := trimSoundEffect(ctx, localPath, targetSeconds, mediaEditor); err != nil {
			return fmt.Errorf("trim %s: %w", clip.Name, err)
		}
		newDuration, err := probeSoundEffectDuration(ctx, localPath)
		if err != nil {
			return fmt.Errorf("probe trimmed %s: %w", localPath, err)
		}
		if newDuration <= 0 || newDuration > time.Duration(*maxSeconds*float64(time.Second)) {
			return fmt.Errorf("trimmed duration invalid for %s: %.3fs", clip.Name, newDuration.Seconds())
		}
		hash, err := sha256File(localPath)
		if err != nil {
			return fmt.Errorf("hash %s: %w", localPath, err)
		}
		clip.Duration = newDuration
		clip.SetFileHash(hash)
		if err := root.Outbox.Dispatcher.EnqueueAndIndex(ctx, clip, hash); err != nil {
			return fmt.Errorf("reindex trimmed effect %s: %w", clip.Name, err)
		}
		changed++
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate sound effects: %w", err)
	}
	fmt.Printf("Sound effects: changed=%d untouched=%d metadata_updated=%d max_seconds=%.2f\n", changed, untouched, metadataUpdated, *maxSeconds)
	return nil
}

// Encoders can add container padding. Leave a small safety margin so the
// measured duration, rather than only the requested ffmpeg cut point, stays
// within the operator's hard maximum.
func trimTargetSeconds(inputPath string, maxSeconds float64) float64 {
	ext := strings.ToLower(filepath.Ext(inputPath))
	switch ext {
	case ".mp3":
		return maxSeconds - 0.10
	case ".mp4", ".mov", ".mkv":
		return maxSeconds - 0.05
	default:
		return maxSeconds
	}
}

func trimSoundEffect(ctx context.Context, inputPath string, maxSeconds float64, editor adminmedia.AudioEditor) error {
	if editor == nil {
		return fmt.Errorf("audio editor is required")
	}
	if err := editor.Trim(ctx, inputPath, maxSeconds); err != nil {
		return fmt.Errorf("ffmpeg trim: %w", err)
	}
	return nil
}
