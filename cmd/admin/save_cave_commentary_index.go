package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/app"
)

const caveCommentaryClipID = "1SG-f4R29R36O7rD7ILzj152e-Irj5z7h"

var caveCommentaryIndex = []map[string]any{
	{"clip_name": "Underwater Cave Attack", "timestamp": "00:00", "moment_description": "The diver approaches a dark crevice on the underwater wall, torch light shining on the orange sponge.", "text": "Exploring this dark underwater hole... 🤿", "suggested_sound": map[string]any{"id": "1AKBfSAXLCT2fuXPsrEQQygETvZshBlcs", "name": "sfx_ambient_sub_bass_drone_01.wav", "reason": "Deep sub-bass rumble setting up a tense, mysterious atmospheric mood."}},
	{"clip_name": "Underwater Cave Attack", "timestamp": "00:02", "moment_description": "The diver's hand hesitates near the opening, reaching slightly inside the dark cavity.", "text": "Is there something inside? 👀", "suggested_sound": map[string]any{"id": "1hsle1Rj6KafogJcQB1TtTsApkNJcxwCq", "name": "sfx_ambient_upset_pulses_01.wav", "reason": "Pulsating dark drone that spikes psychological tension right before the jump scare."}},
	{"clip_name": "Underwater Cave Attack", "timestamp": "00:04", "moment_description": "A massive creature suddenly launches out of the hole, creating a huge cloud of dust and sand.", "text": "OH MY GOD! IT LAUNCHED AT ME! 🤯🦈", "suggested_sound": map[string]any{"id": "1e7eZSFas-WokHJclqLxxeWcEV2AtBmTb", "name": "sfx_cartoon_hiyakkk_scream_01.mp3", "reason": "An energetic high-impact vocal scream matching the sudden shock of the creature's attack."}},
	{"clip_name": "Underwater Cave Attack", "timestamp": "00:07", "moment_description": "The video cuts or ends inside the thick, murky cloud of dust left behind by the creature.", "text": "Never touching a cave again. 💀", "suggested_sound": map[string]any{"id": "16Ykb6V8mX69vqhruEMw0oOKv-IZzG4Nd", "name": "sfx_ambient_cave_echo_01.mp3", "reason": "Spooky echo atmosphere fading out into the dark murky water."}},
}

func runSaveCaveCommentaryIndex(args []string) error {
	if len(args) != 0 {
		return fmt.Errorf("save-cave-commentary-index accepts no arguments")
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
	clip, err := root.Repos.ClipsRepo.GetClip(ctx, caveCommentaryClipID)
	if err != nil || clip == nil {
		return fmt.Errorf("load cave clip: %w", err)
	}
	if strings.TrimSpace(clip.FileHash()) == "" {
		return fmt.Errorf("cave clip has no file hash")
	}
	b, err := json.Marshal(caveCommentaryIndex)
	if err != nil {
		return fmt.Errorf("marshal cave commentary index: %w", err)
	}
	clip.SetMetadataString("commentary_index_json", string(b))
	clip.Name = "Underwater Cave Attack"
	clip.SetMetadataString("clip_name", "underwater-cave-attack")
	clip.SetMetadataString("commentary_language", "en-US")
	clip.SetMetadataString("commentary_index_version", "underwater-cave-attack.v1")
	clip.SetMetadataString("sound_design_plan", "00:00 sub-bass; 00:02 upset pulses; 00:04 scream impact; 00:07 cave echo")
	clip.UpdatedAt = time.Now().UTC()
	deadLettersBefore, err := root.Outbox.EventsRepo.CountByEventTypeAndStatus(ctx, "asset.index.requested", "dead_letter")
	if err != nil {
		return fmt.Errorf("read outbox baseline: %w", err)
	}
	go root.Outbox.EventsPool.Start(ctx, 1)
	defer func() { _ = root.Outbox.EventsPool.Stop(15 * time.Second) }()
	if err := root.Outbox.Dispatcher.EnqueueAndIndex(ctx, clip, clip.FileHash()); err != nil {
		return fmt.Errorf("save cave commentary index: %w", err)
	}
	if err := waitForAssetIndexOutbox(ctx, root, deadLettersBefore); err != nil {
		return err
	}
	fmt.Printf("Cave commentary index saved: asset=%s moments=%d\n", clip.ID, len(caveCommentaryIndex))
	return nil
}
