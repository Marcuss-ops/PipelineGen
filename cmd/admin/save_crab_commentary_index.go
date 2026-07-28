package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/app"
)

const crabCommentaryClipID = "1YnVrMXHYLC69iV_LqiFLf6nBCbGlm57Q"

var crabCommentaryIndex = []map[string]any{
	{"clip_name": "Giant Crab Encounter", "timestamp": "00:00", "moment_description": "A massive spider crab looms over the diver on the sandy seabed.", "text": "Faced with a literal ocean monster... 🦀💨", "suggested_sound": map[string]any{"id": "1AKBfSAXLCT2fuXPsrEQQygETvZshBlcs", "name": "sfx_ambient_sub_bass_drone_01.wav", "reason": "Deep, low-frequency rumble that immediately builds tension and fits the scale of the giant crab."}},
	{"clip_name": "Giant Crab Encounter", "timestamp": "00:02", "moment_description": "The crab plants its long, glowing leg directly into the sand right in front of the camera.", "text": "DON'T MOVE. 😳🛑", "suggested_sound": map[string]any{"id": "1yL87psoGX0ULtknzodcnWkCn8jxP4IIh", "name": "sfx_ambient_evolve_feedback_01.wav", "reason": "Cinematic resonance modulations elevate the sci-fi, tense feel as the leg drops."}},
	{"clip_name": "Giant Crab Encounter", "timestamp": "00:04", "moment_description": "The crab shifts its weight and steps over the diver, its leg glowing intensely.", "text": "It's stepping right over me! 🤯🔥", "suggested_sound": map[string]any{"id": "16Ykb6V8mX69vqhruEMw0oOKv-IZzG4Nd", "name": "sfx_ambient_cave_echo_01.mp3", "reason": "Adds an eerie, suspended atmospheric echo as the massive creature passes above."}},
	{"clip_name": "Giant Crab Encounter", "timestamp": "00:07", "moment_description": "The giant leg lifts back up into the blue water, leaving a small cloud of sand behind.", "text": "New phobia unlocked. 💀🌊", "suggested_sound": map[string]any{"id": "1mzBxdV7BVoZm4C9S4AQe-hAYqth1OB_3", "name": "sfx_ambient_spooky_wind_01.wav", "reason": "A haunting cinematic drone fade-out that seals the ominous mood."}},
}

func runSaveCrabCommentaryIndex(args []string) error {
	if len(args) != 0 {
		return fmt.Errorf("save-crab-commentary-index accepts no arguments")
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
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	clip, err := root.Repos.ClipsRepo.GetClip(ctx, crabCommentaryClipID)
	if err != nil {
		return fmt.Errorf("load crab clip: %w", err)
	}
	if clip == nil {
		return fmt.Errorf("crab clip %s is not indexed", crabCommentaryClipID)
	}
	if strings.TrimSpace(clip.FileHash()) == "" {
		return fmt.Errorf("crab clip has no file hash")
	}
	b, err := json.Marshal(crabCommentaryIndex)
	if err != nil {
		return fmt.Errorf("marshal crab commentary index: %w", err)
	}
	clip.Name = "Giant Crab Encounter"
	clip.SetMetadataString("clip_name", "giant-crab-encounter")
	clip.SetMetadataString("commentary_index_json", string(b))
	clip.SetMetadataString("commentary_language", "en-US")
	clip.SetMetadataString("commentary_index_version", "giant-crab-encounter.v1")
	clip.SetMetadataString("sound_design_plan", "00:00 sub-bass; 00:02 evolving feedback; 00:04 cave echo; 00:07 spooky wind")
	clip.UpdatedAt = time.Now().UTC()
	deadLettersBefore, err := root.Outbox.EventsRepo.CountByEventTypeAndStatus(ctx, "asset.index.requested", "dead_letter")
	if err != nil {
		return fmt.Errorf("read outbox baseline: %w", err)
	}
	go root.Outbox.EventsPool.Start(ctx, 1)
	defer func() { _ = root.Outbox.EventsPool.Stop(15 * time.Second) }()
	if err := root.Outbox.Dispatcher.EnqueueAndIndex(ctx, clip, clip.FileHash()); err != nil {
		return fmt.Errorf("save crab commentary index: %w", err)
	}
	if err := waitForAssetIndexOutbox(ctx, root, deadLettersBefore); err != nil {
		return err
	}
	fmt.Printf("Crab commentary index saved: asset=%s clip_name=%s moments=%d\n", clip.ID, "giant-crab-encounter", len(crabCommentaryIndex))
	return nil
}
