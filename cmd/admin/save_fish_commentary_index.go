package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/app"
)

const fishCommentaryClipID = "1HOElzTp17PvzGcAq1LK4tzpPr7RjjrwT"

var fishCommentaryIndex = []map[string]any{
	{"timestamp": "00:00", "moment_description": "The diver begins moving the seaweed aside, exploring the underwater wall.", "text": "Cleaning up the seaweed... 🌿", "suggested_sound": map[string]any{"id": "1AKBfSAXLCT2fuXPsrEQQygETvZshBlcs", "name": "sfx_ambient_sub_bass_drone_01.wav", "reason": "A low-frequency underwater rumble that establishes a mysterious vibe right away."}},
	{"timestamp": "00:03", "moment_description": "The diver continues to pull down the seaweed, revealing more of the hidden structure.", "text": "Wait, what's behind there? 👀", "suggested_sound": map[string]any{"id": "1yL87psoGX0ULtknzodcnWkCn8jxP4IIh", "name": "sfx_ambient_evolve_feedback_01.wav", "reason": "An evolving cinematic drone that adds a dramatic build-up to the upcoming surprise."}},
	{"timestamp": "00:05", "moment_description": "The diver clears the face completely, and a giant stone statue face is revealed.", "text": "OH MY GOD! 🤯🗿", "suggested_sound": map[string]any{"id": "13SHeZMfnDiIkR2OsVK_3XlYX8cjojQa1", "name": "sfx_cartoon_brain_aneurysm_meme_01.mp3", "reason": "A shocking meme effect that perfectly hits right as the scary/mysterious face is fully revealed."}},
	{"timestamp": "00:08", "moment_description": "The video ends with a full look at the spooky submerged face.", "text": "Unlocking deep sea phobias... 💀", "suggested_sound": map[string]any{"id": "1mzBxdV7BVoZm4C9S4AQe-hAYqth1OB_3", "name": "sfx_ambient_spooky_wind_01.wav", "reason": "A horror atmosphere that fades out to leave a lingering eerie feeling about the statue."}},
}

func runSaveFishCommentaryIndex(args []string) error {
	if len(args) != 0 {
		return fmt.Errorf("save-fish-commentary-index accepts no arguments")
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
	if root == nil || root.Repos == nil || root.Repos.ClipsRepo == nil || root.Outbox == nil || root.Outbox.Dispatcher == nil || root.Outbox.EventsPool == nil || root.Outbox.EventsRepo == nil {
		return fmt.Errorf("clips repository and outbox are required")
	}
	clip, err := root.Repos.ClipsRepo.GetClip(context.Background(), fishCommentaryClipID)
	if err != nil {
		return fmt.Errorf("load fish clip: %w", err)
	}
	if clip == nil {
		return fmt.Errorf("fish clip %s is not indexed", fishCommentaryClipID)
	}
	b, err := json.Marshal(fishCommentaryIndex)
	if err != nil {
		return fmt.Errorf("marshal commentary index: %w", err)
	}
	clip.SetMetadataString("commentary_index_json", string(b))
	clip.Name = "Alien Face Reveal"
	clip.SetMetadataString("clip_name", "alien-face-reveal")
	clip.SetMetadataString("commentary_language", "en-US")
	clip.SetMetadataString("commentary_index_version", "fish-statue-reveal.v1")
	clip.SetMetadataString("sound_design_plan", "00:00 ambient sub-bass; 00:03 evolving feedback; 00:05 reveal reaction; 00:08 spooky wind")
	clip.UpdatedAt = time.Now().UTC()
	if strings.TrimSpace(clip.FileHash()) == "" {
		return fmt.Errorf("fish clip has no file hash")
	}
	deadLettersBefore, err := root.Outbox.EventsRepo.CountByEventTypeAndStatus(context.Background(), "asset.index.requested", "dead_letter")
	if err != nil {
		return fmt.Errorf("read outbox baseline: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	go root.Outbox.EventsPool.Start(ctx, 1)
	defer func() { _ = root.Outbox.EventsPool.Stop(15 * time.Second) }()
	if err := root.Outbox.Dispatcher.EnqueueAndIndex(ctx, clip, clip.FileHash()); err != nil {
		return fmt.Errorf("save commentary index: %w", err)
	}
	if err := waitForAssetIndexOutbox(ctx, root, deadLettersBefore); err != nil {
		return err
	}
	fmt.Printf("Fish commentary index saved: asset=%s moments=%d\n", clip.ID, len(fishCommentaryIndex))
	return nil
}
