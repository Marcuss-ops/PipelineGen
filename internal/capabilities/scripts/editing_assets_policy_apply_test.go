package scriptgeneration

import (
	"encoding/json"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaregistry"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func combinedTimelineRequest(key string) GenerateRequest {
	return GenerateRequest{
		IdempotencyKey: key,
		Audio:          audio.AudioModeCombinedTimeline,
	}
}

func TestApplyEditingAssetPolicyFillsBlankSelections(t *testing.T) {
	policy := mediaregistry.DefaultEditingAssetsPolicy()
	req := combinedTimelineRequest("matt-damon-intro")
	if err := ApplyEditingAssetPolicy(&req, policy); err != nil {
		t.Fatalf("ApplyEditingAssetPolicy: %v", err)
	}

	if req.Render.Background == nil {
		t.Fatal("background was not selected")
	}
	if req.Render.Background.Mode != videoBackgroundModeAsset {
		t.Errorf("background mode = %q, want %q", req.Render.Background.Mode, videoBackgroundModeAsset)
	}
	if _, ok := mediaregistry.LookupEditorialBackground(req.Render.Background.AssetID); !ok {
		t.Errorf("selected background %q is not a canonical plate", req.Render.Background.AssetID)
	}

	if len(req.BackgroundMusic) != 1 {
		t.Fatalf("background music entries = %d, want 1", len(req.BackgroundMusic))
	}
	bgm := req.BackgroundMusic[0]
	if !containsString(policy.BGM.Pool, bgm.AssetID) {
		t.Errorf("selected bgm %q is outside the policy pool", bgm.AssetID)
	}
	if !bgm.Loop || !bgm.DuckUnderVoiceover {
		t.Errorf("BGM defaults not applied: %+v", bgm)
	}
	if bgm.GainDB != policy.BGM.GainDB || bgm.DuckGainDB != policy.BGM.DuckGainDB {
		t.Errorf("BGM gains = %g/%g, want %g/%g", bgm.GainDB, bgm.DuckGainDB, policy.BGM.GainDB, policy.BGM.DuckGainDB)
	}
}

func TestApplyEditingAssetPolicyIsDeterministic(t *testing.T) {
	policy := mediaregistry.DefaultEditingAssetsPolicy()
	first := combinedTimelineRequest("same-key")
	second := combinedTimelineRequest("same-key")
	if err := ApplyEditingAssetPolicy(&first, policy); err != nil {
		t.Fatal(err)
	}
	if err := ApplyEditingAssetPolicy(&second, policy); err != nil {
		t.Fatal(err)
	}
	if first.Render.Background.AssetID != second.Render.Background.AssetID {
		t.Errorf("background selection is not deterministic: %q vs %q", first.Render.Background.AssetID, second.Render.Background.AssetID)
	}
	if first.BackgroundMusic[0].AssetID != second.BackgroundMusic[0].AssetID {
		t.Errorf("bgm selection is not deterministic: %q vs %q", first.BackgroundMusic[0].AssetID, second.BackgroundMusic[0].AssetID)
	}
}

// TestApplyEditingAssetPolicyNeverOverridesCallerIntent is the guardrail that
// keeps the policy opt-in: an explicit background mode, an explicit BGM list,
// and a non-COMBINED_TIMELINE audio mode must all survive untouched.
func TestApplyEditingAssetPolicyNeverOverridesCallerIntent(t *testing.T) {
	policy := mediaregistry.DefaultEditingAssetsPolicy()

	t.Run("explicit background mode is preserved", func(t *testing.T) {
		for _, mode := range []string{"none", "blur_source"} {
			req := combinedTimelineRequest("key")
			req.Render.Background = &scriptpkg.VideoBackgroundSpec{Mode: mode}
			if err := ApplyEditingAssetPolicy(&req, policy); err != nil {
				t.Fatal(err)
			}
			if req.Render.Background.Mode != mode || req.Render.Background.AssetID != "" {
				t.Errorf("mode %q was overridden: %+v", mode, req.Render.Background)
			}
		}
	})

	t.Run("explicit background asset is preserved", func(t *testing.T) {
		req := combinedTimelineRequest("key")
		req.Render.Background = &scriptpkg.VideoBackgroundSpec{Mode: "asset", AssetID: "classic1"}
		if err := ApplyEditingAssetPolicy(&req, policy); err != nil {
			t.Fatal(err)
		}
		if req.Render.Background.AssetID != "classic1" {
			t.Errorf("background asset overridden: %+v", req.Render.Background)
		}
	})

	t.Run("explicit bgm list is preserved", func(t *testing.T) {
		req := combinedTimelineRequest("key")
		req.BackgroundMusic = []scriptpkg.BackgroundMusicIntent{{AssetID: "bgm3", Loop: true, GainDB: -28}}
		if err := ApplyEditingAssetPolicy(&req, policy); err != nil {
			t.Fatal(err)
		}
		if len(req.BackgroundMusic) != 1 || req.BackgroundMusic[0].AssetID != "bgm3" {
			t.Errorf("bgm list changed: %+v", req.BackgroundMusic)
		}
	})

	t.Run("bgm is not injected outside COMBINED_TIMELINE", func(t *testing.T) {
		for _, mode := range []audio.AudioMode{audio.AudioModeNone, audio.AudioModeChunkedVoiceover, ""} {
			req := GenerateRequest{IdempotencyKey: "key", Audio: mode}
			if err := ApplyEditingAssetPolicy(&req, policy); err != nil {
				t.Fatal(err)
			}
			if len(req.BackgroundMusic) != 0 {
				t.Errorf("audio mode %q received an injected BGM layer: %+v", mode, req.BackgroundMusic)
			}
		}
	})
}

func TestApplyEditingAssetPolicyFailsClosed(t *testing.T) {
	if err := ApplyEditingAssetPolicy(nil, mediaregistry.DefaultEditingAssetsPolicy()); err == nil {
		t.Fatal("nil request must fail closed")
	}
	invalid := mediaregistry.DefaultEditingAssetsPolicy()
	invalid.Backgrounds.Pool = nil
	req := combinedTimelineRequest("key")
	if err := ApplyEditingAssetPolicy(&req, invalid); err == nil {
		t.Fatal("an invalid policy must fail closed")
	}
	if req.Render.Background != nil {
		t.Errorf("a rejected policy must not have mutated the request: %+v", req.Render.Background)
	}
}

// TestBuildGenerateRequest_IntroV2CentralizedBackgroundPlate certifies the
// Intro V2 "background centralizzato" contract AT THE SINGLE INGRESS both job
// handlers share: a clip-based script that names no background receives ONE
// canonical editorial plate (bound to the published video-background-v1
// contract), the selection is deterministic for a repeated request, and an
// explicit caller selection is never overridden.
func TestBuildGenerateRequest_IntroV2CentralizedBackgroundPlate(t *testing.T) {
	decode := func(payload string) *scriptpkg.GenerationEnvelopeV2 {
		t.Helper()
		var env scriptpkg.GenerationEnvelopeV2
		if err := json.Unmarshal([]byte(payload), &env); err != nil {
			t.Fatal(err)
		}
		return &env
	}
	clipScript := func(render string) string {
		return `{"version":2,"items":[{"title":"intro-v2","project":"test-project","language":"en","source":{"type":"clips","clip_ids":["clip-a"]},"output":{"voiceover_enabled":true` + render + `},"audio":{"mode":"COMBINED_TIMELINE"}}]}`
	}

	t.Run("a blank background is filled with a canonical plate", func(t *testing.T) {
		got, err := BuildGenerateRequest(decode(clipScript("")), "intro-v2-key")
		if err != nil {
			t.Fatal(err)
		}
		if got.Render.Background == nil {
			t.Fatal("a clip-based script with no background must receive the centralized plate")
		}
		if got.Render.Background.Mode != videoBackgroundModeAsset {
			t.Fatalf("background mode = %q, want %q", got.Render.Background.Mode, videoBackgroundModeAsset)
		}
		plate, ok := mediaregistry.LookupEditorialBackground(got.Render.Background.AssetID)
		if !ok {
			t.Fatalf("selected background %q is not a canonical editorial plate", got.Render.Background.AssetID)
		}
		if plate.SHA256 == "" || plate.DriveFileID == "" {
			t.Fatalf("canonical plate must carry its certified identity: %+v", plate)
		}
	})

	t.Run("the selection is deterministic for the same request", func(t *testing.T) {
		first, err := BuildGenerateRequest(decode(clipScript("")), "intro-v2-key")
		if err != nil {
			t.Fatal(err)
		}
		second, err := BuildGenerateRequest(decode(clipScript("")), "intro-v2-key")
		if err != nil {
			t.Fatal(err)
		}
		if first.Render.Background == nil || second.Render.Background == nil {
			t.Fatal("both requests must receive a plate")
		}
		if first.Render.Background.AssetID != second.Render.Background.AssetID {
			t.Fatalf("plate selection is not deterministic: %q vs %q",
				first.Render.Background.AssetID, second.Render.Background.AssetID)
		}
	})

	t.Run("an explicit caller selection is preserved", func(t *testing.T) {
		got, err := BuildGenerateRequest(decode(clipScript(`,"render":{"background":{"mode":"none"}}`)), "intro-v2-key")
		if err != nil {
			t.Fatal(err)
		}
		if got.Render.Background == nil || got.Render.Background.Mode != "none" || got.Render.Background.AssetID != "" {
			t.Fatalf("explicit background selection was overridden: %+v", got.Render.Background)
		}
	})

	t.Run("an explicit plate is preserved", func(t *testing.T) {
		got, err := BuildGenerateRequest(decode(clipScript(`,"render":{"background":{"mode":"asset","asset_id":"drive-background-01"}}`)), "intro-v2-key")
		if err != nil {
			t.Fatal(err)
		}
		if got.Render.Background == nil || got.Render.Background.AssetID != "drive-background-01" {
			t.Fatalf("explicit plate was overridden: %+v", got.Render.Background)
		}
	})
}

func containsString(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}
