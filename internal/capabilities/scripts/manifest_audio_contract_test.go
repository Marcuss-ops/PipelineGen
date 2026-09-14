package scriptgeneration

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// TestMattDamonTysonManifestAudioContract keeps the user-facing manifest
// aligned with the canonical generation envelope. It is deliberately a
// request-level gate: a live run must additionally inspect audio_plan and
// final_audio for the materialized BGM/SFX events.
func TestMattDamonTysonManifestAudioContract(t *testing.T) {
	path := filepath.Join("..", "..", "..", "ops", "jobs", "matt_damon_intro_mike_tyson_stock.generate.json")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var envelope scriptpkg.GenerationEnvelopeV2
	if err := json.Unmarshal(body, &envelope); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	if err := envelope.Validate(); err != nil {
		t.Fatalf("manifest violates GenerationEnvelopeV2: %v", err)
	}

	item := envelope.Items[0]
	if item.MediaMode != scriptpkg.MediaModeStockOnly {
		t.Fatalf("media_mode = %q, want stock_only", item.MediaMode)
	}
	if item.Intro == nil || len(item.Intro.ClipIDs) != 1 || item.Intro.ClipIDs[0] != "yt_0ElQTzSx3ec_72_91_v1" {
		t.Fatalf("intro does not pin the Matt Damon clip: %+v", item.Intro)
	}
	if item.Intro.Playback.Normalize().AudioMode != scriptpkg.FixedPlaybackOriginalClip {
		t.Fatalf("intro audio mode = %q, want original_clip", item.Intro.Playback.AudioMode)
	}

	if item.Output.VoiceoverEnabled != scriptpkg.ToggleEnabled || item.Output.VoiceoverGroup != "Velox Max" {
		t.Fatalf("voiceover routing = enabled:%q group:%q, want enabled:enabled group:Velox Max", item.Output.VoiceoverEnabled, item.Output.VoiceoverGroup)
	}
	if item.Audio.Mode != "COMBINED_TIMELINE" || len(item.Audio.BackgroundMusic) != 1 || len(item.Audio.SoundEffects) != 2 {
		t.Fatalf("audio contract = mode:%q bgm:%d sfx:%d, want COMBINED_TIMELINE/1/2", item.Audio.Mode, len(item.Audio.BackgroundMusic), len(item.Audio.SoundEffects))
	}
	if item.Audio.BackgroundMusic[0].AssetID != "bgm3" || !item.Audio.BackgroundMusic[0].Loop {
		t.Fatalf("BGM = %+v, want looped bgm3", item.Audio.BackgroundMusic[0])
	}
	if item.Audio.SoundEffects[0].AssetID != "whop1" || item.Audio.SoundEffects[1].AssetID != "whop2" {
		t.Fatalf("SFX assets = %q, %q, want whop1 and whop2", item.Audio.SoundEffects[0].AssetID, item.Audio.SoundEffects[1].AssetID)
	}

	if len(item.Output.StockBindings) != 1 {
		t.Fatalf("stock bindings = %d, want one Mike Tyson folder binding", len(item.Output.StockBindings))
	}
	binding := item.Output.StockBindings[0]
	if binding.FolderID == "" || binding.FolderLink == "" || binding.AssetID != "" || binding.DriveLink != "" {
		t.Fatalf("stock binding must be folder-only for stock_only: %+v", binding)
	}
}
