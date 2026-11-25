package scriptgeneration

import (
	"encoding/json"
	"testing"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func TestBuildGenerateRequestMapsClipStartSoundEffects(t *testing.T) {
	var env scriptpkg.GenerationEnvelopeV2
	payload := `{"version":2,"items":[{"title":"clip-sfx","project":"p","language":"en",` +
		`"source":{"type":"clips","clip_ids":["clip-a"]},` +
		`"script_params":{"target_words":10},` +
		`"audio":{"clip_start_sound_effects":[{"clip_id":"clip-a","asset_id":"whoosh1",` +
		`"duration_ms":300,"gain_db":-4}]}}]}`
	if err := json.Unmarshal([]byte(payload), &env); err != nil {
		t.Fatal(err)
	}
	got, err := BuildGenerateRequest(&env, "clip-sfx-key")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.ClipStartSoundEffects) != 1 {
		t.Fatalf("ClipStartSoundEffects = %+v", got.ClipStartSoundEffects)
	}
	cue := got.ClipStartSoundEffects[0]
	if cue.ClipID != "clip-a" || cue.AssetID != "whoosh1" || cue.EffectiveGainDB() != -4 || cue.EffectiveDurationMS() != 300 {
		t.Fatalf("cue = %+v", cue)
	}
}
