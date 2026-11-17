package scriptgeneration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
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

func TestAttachImageAssetSFXBindsPinsBySceneAndStartOrder(t *testing.T) {
	assetBytes := []byte("explicit-image-cue-bytes")
	sum := sha256.Sum256(assetBytes)
	path := filepath.Join(t.TempDir(), "cue.m4a")
	if err := os.WriteFile(path, assetBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	source := newFakeAudioAssetSource(map[string]string{"whoosh": path})
	long := int64(2000)
	gain := -6.0
	pins := []scriptpkg.OverlayImageAsset{{
		SceneID: "scene_1", AssetID: "image-pin",
		SoundEffect: &scriptpkg.OverlayImageSoundEffect{AssetID: "whoosh", GainDB: &gain, DurationMS: &long},
	}}
	plan := imageSFXTestPlan()
	if err := attachImageAssetSFX(context.Background(), source, []*capmodel.OverlayPlan{plan}, pins); err != nil {
		t.Fatalf("attach explicit image SFX: %v", err)
	}

	byID := map[string]*capmodel.OverlayItem{}
	for index := range plan.Items {
		byID[plan.Items[index].ID] = &plan.Items[index]
	}
	early := byID["scene-1:img:early"]
	if early.SoundEffect == nil {
		t.Fatalf("earliest image item did not receive the cue: %+v", early)
	}
	// The requested 2000 ms cue is clamped to the 800 ms visible window.
	if early.SoundEffect.DurationMS != 800 {
		t.Fatalf("duration = %d, want 800 (visible window clamp)", early.SoundEffect.DurationMS)
	}
	if early.SoundEffect.StartOffsetMS != 0 || early.SoundEffect.GainDB != -6 {
		t.Fatalf("cue timing/gain = %+v", early.SoundEffect)
	}
	if early.SoundEffect.AssetRef.SHA256 != hex.EncodeToString(sum[:]) || early.SoundEffect.AssetRef.MediaType != "audio/mp4" {
		t.Fatalf("cue asset identity = %+v", early.SoundEffect.AssetRef)
	}
	if !strings.HasPrefix(early.SoundEffect.AssetRef.URL, "assets/semantic/overlay-sfx-") {
		t.Fatalf("cue url = %q", early.SoundEffect.AssetRef.URL)
	}
	if byID["scene-1:img:late"].SoundEffect != nil {
		t.Fatal("only one pin was declared; the second image must stay unclaimed")
	}
	if err := plan.Validate(); err != nil {
		t.Fatalf("sealed plan must validate: %v", err)
	}
	// A remaining random pass must not overwrite the explicit cue.
	if err := attachRandomOverlaySFXFromAssets([]*capmodel.OverlayPlan{plan}, []randomOverlaySFXAsset{{path: path, sha256: hex.EncodeToString(sum[:])}}); err != nil {
		t.Fatalf("random pass over explicit cue: %v", err)
	}
	if byID["scene-1:img:early"].SoundEffect.DurationMS != 800 {
		t.Fatal("random pass overwrote the explicit cue")
	}
}

func TestAttachImageAssetSFXFailsClosed(t *testing.T) {
	pins := []scriptpkg.OverlayImageAsset{{
		SceneID: "scene_1", SoundEffect: &scriptpkg.OverlayImageSoundEffect{AssetID: "whoosh"},
	}}
	if err := attachImageAssetSFX(context.Background(), nil, []*capmodel.OverlayPlan{imageSFXTestPlan()}, pins); err == nil ||
		!strings.Contains(err.Error(), "resolver") {
		t.Fatalf("nil resolver must fail closed, got %v", err)
	}

	source := newFakeAudioAssetSource(map[string]string{"whoosh": "/nonexistent/cue.m4a"})
	over := []scriptpkg.OverlayImageAsset{
		{SceneID: "scene_1", SoundEffect: &scriptpkg.OverlayImageSoundEffect{AssetID: "whoosh"}},
		{SceneID: "scene_1", SoundEffect: &scriptpkg.OverlayImageSoundEffect{AssetID: "whoosh"}},
		{SceneID: "scene_1", SoundEffect: &scriptpkg.OverlayImageSoundEffect{AssetID: "whoosh"}},
	}
	if err := attachImageAssetSFX(context.Background(), source, []*capmodel.OverlayPlan{imageSFXTestPlan()}, over); err == nil ||
		!strings.Contains(err.Error(), "declares 3 cues") {
		t.Fatalf("more cues than image items must fail closed, got %v", err)
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
