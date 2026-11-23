package scriptgeneration

import (
	"context"
	"strings"
	"testing"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"go.uber.org/zap"
)

func imageSFXTestPlan() *capmodel.OverlayPlan {
	plan := &capmodel.OverlayPlan{
		SchemaVersion: capmodel.SchemaVersionPlan, PlanID: "plan-image-sfx", VideoID: "video",
		Width: 1920, Height: 1080, FPSNum: 24, FPSDen: 1,
		Items: []capmodel.OverlayItem{
			{
				ID: "scene-1:img:late", SceneID: "scene_1", Kind: "image", TemplateID: "image_popup",
				StartMs: 2000, EndMs: 2800,
				AssetRefs: []capmodel.OverlayAssetRef{{AssetID: "late", SHA256: strings.Repeat("b", 64), URL: "assets/late.png"}},
			},
			{
				ID: "scene-1:img:early", SceneID: "scene_1", Kind: "image", TemplateID: "image_popup",
				StartMs: 1000, EndMs: 1800,
				AssetRefs: []capmodel.OverlayAssetRef{{AssetID: "early", SHA256: strings.Repeat("a", 64), URL: "assets/early.png"}},
			},
		},
	}
	if err := plan.Validate(); err != nil {
		panic(err)
	}
	return plan
}

func TestOverlaySFXIntentsBindExplicitPinsBySceneAndStartOrder(t *testing.T) {
	long := int64(2000)
	gain := -6.0
	pins := []scriptpkg.OverlayImageAsset{{
		SceneID: "scene_1", AssetID: "image-pin",
		SoundEffect: &scriptpkg.OverlayImageSoundEffect{AssetID: "whoosh", GainDB: &gain, DurationMS: &long},
	}}
	req := GenerateRequest{OverlayImages: pins}
	intents, err := overlaySFXIntentsForPlan(req, imageSFXTestPlan(), nil)
	if err != nil {
		t.Fatalf("project explicit image SFX: %v", err)
	}
	if len(intents) != 1 {
		t.Fatalf("intents = %d, want 1 (one pin, one image item)", len(intents))
	}
	cue := intents[0]
	// The earliest image item is the first positional candidate.
	if cue.AssetID != "whoosh" || cue.AtMS != 1000 || cue.DurationMS != 800 || cue.GainDB != -6 {
		t.Fatalf("cue = %+v, want whoosh at 1000ms clamped to the 800ms window at -6dB", cue)
	}
}

func TestOverlaySFXIntentsRandomEmitsOneCueAtClipStart(t *testing.T) {
	plan := &capmodel.OverlayPlan{
		PlanID: "plan-chain", VideoID: "video",
		Items: []capmodel.OverlayItem{{
			ID: "chain:1", SceneID: "scene_1", Kind: "image", TemplateID: "image_popup",
			StartMs: 1000, EndMs: 4000,
			AssetRefs: []capmodel.OverlayAssetRef{{AssetID: "chain", SHA256: strings.Repeat("c", 64)}},
			ImageLayers: []capmodel.OverlayImageLayer{
				{ID: "a", AssetID: "a", StartMS: 0, EndMS: 1000},
				{ID: "b", AssetID: "b", StartMS: 1000, EndMS: 2200},
				// A simultaneous layer at the same start must not add a cue.
				{ID: "b2", AssetID: "b2", StartMS: 1000, EndMS: 1800},
				{ID: "c", AssetID: "c", StartMS: 2200, EndMS: 3000},
			},
		}},
	}
	req := GenerateRequest{RandomSFXOnImageOverlay: true}
	intents, err := overlaySFXIntentsForPlan(req, plan, nil)
	if err != nil {
		t.Fatalf("project random chain SFX: %v", err)
	}
	// A chain is ONE clip: the cue fires once at its start, never again on an
	// internal segment boundary.
	if len(intents) != 1 {
		t.Fatalf("intents = %d, want one cue at the clip start", len(intents))
	}
	intent := intents[0]
	if intent.AtMS != 1000 {
		t.Fatalf("cue at %dms, want the item start 1000ms", intent.AtMS)
	}
	if intent.DurationMS != overlaySFXCueDurationMS || intent.GainDB != overlaySFXCueGainDB {
		t.Fatalf("cue = %+v, want the editorial 250ms/-18dB cue", intent)
	}
	if !isBuiltInWhoosh(intent.AssetID) {
		t.Fatalf("asset %q is not a bound whoosh alias", intent.AssetID)
	}
	// Determinism: retries must select the same cue.
	again, err := overlaySFXIntentsForPlan(req, plan, nil)
	if err != nil {
		t.Fatalf("re-project random chain SFX: %v", err)
	}
	for i := range intents {
		if intents[i] != again[i] {
			t.Fatalf("intent %d is not deterministic: %+v vs %+v", i, intents[i], again[i])
		}
	}
}

func TestOverlaySFXIntentsRandomUsesIndexedPool(t *testing.T) {
	pool := []string{"sfx_indexed_a", "sfx_indexed_b", "sfx_indexed_c"}
	intents, err := overlaySFXIntentsForPlan(GenerateRequest{RandomSFXOnImageOverlay: true}, imageSFXTestPlan(), pool)
	if err != nil {
		t.Fatalf("project random SFX with indexed pool: %v", err)
	}
	if len(intents) != 2 {
		t.Fatalf("intents = %d, want one per image item", len(intents))
	}
	allowed := map[string]struct{}{}
	for _, id := range pool {
		allowed[id] = struct{}{}
	}
	for i, intent := range intents {
		if _, ok := allowed[intent.AssetID]; !ok {
			t.Fatalf("intent %d asset %q is not in the supplied indexed pool", i, intent.AssetID)
		}
		if intent.DurationMS != overlaySFXCueDurationMS || intent.GainDB != overlaySFXCueGainDB {
			t.Fatalf("intent %d = %+v, want the editorial 250ms/-18dB cue", i, intent)
		}
	}
}

func isBuiltInWhoosh(assetID string) bool {
	for _, alias := range scriptpkg.BuiltInWhooshAliases() {
		if alias == assetID {
			return true
		}
	}
	return false
}

func TestOverlaySFXIntentsFailClosed(t *testing.T) {
	over := []scriptpkg.OverlayImageAsset{
		{SceneID: "scene_1", SoundEffect: &scriptpkg.OverlayImageSoundEffect{AssetID: "whoosh"}},
		{SceneID: "scene_1", SoundEffect: &scriptpkg.OverlayImageSoundEffect{AssetID: "whoosh"}},
		{SceneID: "scene_1", SoundEffect: &scriptpkg.OverlayImageSoundEffect{AssetID: "whoosh"}},
	}
	if _, err := overlaySFXIntentsForPlan(GenerateRequest{OverlayImages: over}, imageSFXTestPlan(), nil); err == nil ||
		!strings.Contains(err.Error(), "declares 3 cues") {
		t.Fatalf("more cues than image items must fail closed, got %v", err)
	}

	invalid := []scriptpkg.OverlayImageAsset{{
		SceneID: "scene_1", SoundEffect: &scriptpkg.OverlayImageSoundEffect{},
	}}
	if _, err := overlaySFXIntentsForPlan(GenerateRequest{OverlayImages: invalid}, imageSFXTestPlan(), nil); err == nil ||
		!strings.Contains(err.Error(), "asset_id is required") {
		t.Fatalf("invalid cue must fail closed, got %v", err)
	}
}

// TestExtendAudioPlanWithOverlaySFXAddsCuesAndAssets certifies the wiring: the
// projected cues become SFX events on the compiled master plan and every
// referenced alias is present in the merged resolved-asset table.
func TestExtendAudioPlanWithOverlaySFXAddsCuesAndAssets(t *testing.T) {
	timeline := capabilityaudio.CanonicalTimeline{
		Version: capabilityaudio.TimelineVersion, DurationUS: 10_000_000,
		Segments: []capabilityaudio.TimelineSegment{{
			ID: "scene_1", Index: 0, TimelineStartUS: 0, DurationUS: 10_000_000,
		}},
	}
	plan, err := capabilityaudio.Compile(timeline, capabilityaudio.DefaultAudioProfile())
	if err != nil {
		t.Fatalf("compile fixture plan: %v", err)
	}
	before := plan.PlanSHA256
	source := newFakeAudioAssetSource(nil)
	for _, alias := range scriptpkg.BuiltInWhooshAliases() {
		canonical := capabilityaudio.CanonicalAssetID(alias)
		source.assets[canonical] = capabilityaudio.ResolvedAudioAsset{AssetID: canonical, Path: "/p/" + alias + ".m4a"}
	}
	runner := &Runner{audioAssetSource: source, log: zap.NewNop()}
	result := &GenerateResult{OverlayPlan: imageSFXTestPlan()}
	extended, assets, err := runner.extendAudioPlanWithOverlaySFX(
		context.Background(), "run", GenerateRequest{RandomSFXOnImageOverlay: true}, result, timeline, plan, nil)
	if err != nil {
		t.Fatalf("extend master plan: %v", err)
	}
	sfx := 0
	var starts []int64
	for _, track := range extended.Tracks {
		if track.Role != capabilityaudio.TrackSFX {
			continue
		}
		for _, event := range track.Events {
			sfx++
			starts = append(starts, event.TimelineStartUS)
		}
	}
	if sfx != 2 || len(starts) != 2 || starts[0] != 1_000_000 || starts[1] != 2_000_000 {
		t.Fatalf("sfx events = %d at %v, want the two image windows at 1s and 2s", sfx, starts)
	}
	if extended.PlanSHA256 == before {
		t.Fatal("the extended master plan must be re-sealed")
	}
	if err := extended.Validate(); err != nil {
		t.Fatalf("extended master plan must validate: %v", err)
	}
	if len(assets) == 0 {
		t.Fatal("the merged asset table must carry the resolved cue aliases")
	}
	for _, asset := range assets {
		if asset.Path == "" {
			t.Fatalf("asset %q has an empty path", asset.AssetID)
		}
	}
}

func TestOverlayImageSFXPayloadValidation(t *testing.T) {
	valid := &scriptpkg.GenerationEnvelopeV2{
		Version: 2,
		Items: []scriptpkg.GenerationItemV2{{
			Source: scriptpkg.SourceSpec{Type: "text", Topic: "image sfx"},
			OverlayImages: []scriptpkg.OverlayImageAsset{{
				SceneID: "scene_1", AssetID: "image", LocalPath: "/tmp/a.png", SHA256: strings.Repeat("a", 64),
				SoundEffect: &scriptpkg.OverlayImageSoundEffect{AssetID: "whoosh"},
			}},
			ScriptParams: scriptpkg.ScriptSpec{TargetWords: 100},
		}},
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid per-image SFX payload rejected: %v", err)
	}

	missingAsset := valid
	missingAsset.Items = append([]scriptpkg.GenerationItemV2(nil), valid.Items...)
	missingAsset.Items[0].OverlayImages = []scriptpkg.OverlayImageAsset{{
		SceneID: "scene_1", SoundEffect: &scriptpkg.OverlayImageSoundEffect{},
	}}
	if err := missingAsset.Validate(); err == nil || !strings.Contains(err.Error(), "asset_id is required") {
		t.Fatalf("missing asset_id must be rejected, got %v", err)
	}

	missingScene := valid
	missingScene.Items = append([]scriptpkg.GenerationItemV2(nil), valid.Items...)
	badGain := 30.0
	missingScene.Items[0].OverlayImages = []scriptpkg.OverlayImageAsset{{
		SoundEffect: &scriptpkg.OverlayImageSoundEffect{AssetID: "whoosh", GainDB: &badGain},
	}}
	if err := missingScene.Validate(); err == nil || !strings.Contains(err.Error(), "requires scene_id") {
		t.Fatalf("scene-less cue must be rejected, got %v", err)
	}
}
