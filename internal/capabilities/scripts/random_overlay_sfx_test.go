package scriptgeneration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	capoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
)

type captureSFXQueueClient struct {
	job RenderQueueJob
}

func (c *captureSFXQueueClient) Submit(_ context.Context, job RenderQueueJob) error {
	c.job = job
	return nil
}

func (c *captureSFXQueueClient) Get(_ context.Context, id string) (RenderQueueJob, error) {
	return RenderQueueJob{ID: id, State: "completed", Artifact: &RenderArtifact{ID: "result", URL: "https://store/result.mp4", SHA256: "ab", SizeBytes: 1}}, nil
}

func TestLoadRandomOverlaySFXAssetsFromManifestVerifiesContent(t *testing.T) {
	dir := t.TempDir()
	contents := []byte("known-audio-bytes")
	sum := sha256.Sum256(contents)
	if err := os.WriteFile(filepath.Join(dir, "cue.m4a"), contents, 0o600); err != nil {
		t.Fatal(err)
	}
	manifest := randomOverlaySFXManifest{}
	manifest.Entries = append(manifest.Entries, struct {
		Path      string `json:"path"`
		SHA256    string `json:"sha256"`
		MediaType string `json:"media_type"`
	}{Path: "data/media/sfx_clip_random/cue.m4a", SHA256: hex.EncodeToString(sum[:]), MediaType: "audio/mp4"})
	manifestBytes, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(dir, "manifest.json")
	if err := os.WriteFile(manifestPath, manifestBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	assets, err := loadRandomOverlaySFXAssetsFromManifest(manifestPath)
	if err != nil || len(assets) != 1 || assets[0].sha256 != hex.EncodeToString(sum[:]) {
		t.Fatalf("load verified assets = %+v, err=%v", assets, err)
	}

	manifest.Entries[0].Path = "data/media/sfx_clip_random/../outside.m4a"
	manifestBytes, _ = json.Marshal(manifest)
	if err := os.WriteFile(manifestPath, manifestBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadRandomOverlaySFXAssetsFromManifest(manifestPath); err == nil {
		t.Fatal("manifest traversal path must fail")
	}
}

func TestAttachRandomOverlaySFXIsDeterministicAndVisibleWindowBounded(t *testing.T) {
	assetBytes := []byte("manifest-verified-audio")
	sum := sha256.Sum256(assetBytes)
	assetHash := hex.EncodeToString(sum[:])
	localPath := filepath.Join(t.TempDir(), "cue.m4a")
	if err := os.WriteFile(localPath, assetBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	assets := []randomOverlaySFXAsset{{path: localPath, sha256: assetHash}}
	newPlan := func() *capoverlay.OverlayPlan {
		plan := &capoverlay.OverlayPlan{
			SchemaVersion: capoverlay.SchemaVersionPlan, PlanID: "plan-stable", VideoID: "video",
			Width: 1920, Height: 1080, FPSNum: 24, FPSDen: 1,
			Items: []capoverlay.OverlayItem{{
				ID: "img", Kind: "image", TemplateID: "image_popup", StartMs: 1000, EndMs: 1800,
				AssetRefs: []capoverlay.OverlayAssetRef{{AssetID: "image", SHA256: strings.Repeat("a", 64), URL: "assets/image.png"}},
			}},
		}
		if err := plan.Validate(); err != nil {
			t.Fatal(err)
		}
		return plan
	}
	first, second := newPlan(), newPlan()
	if err := attachRandomOverlaySFXFromAssets([]*capoverlay.OverlayPlan{first}, assets); err != nil {
		t.Fatal(err)
	}
	if err := attachRandomOverlaySFXFromAssets([]*capoverlay.OverlayPlan{second}, assets); err != nil {
		t.Fatal(err)
	}
	cue := first.Items[0].SoundEffect
	if cue == nil || second.Items[0].SoundEffect == nil {
		t.Fatal("expected one cue on each image")
	}
	if cue.AssetRef.SHA256 != second.Items[0].SoundEffect.AssetRef.SHA256 || cue.StartOffsetMS != 0 || cue.DurationMS != randomOverlaySFXDurationMS {
		t.Fatalf("cue selection/timing is not stable: %+v / %+v", cue, second.Items[0].SoundEffect)
	}
	if cue.AssetRef.LocalPath != localPath {
		t.Fatalf("cue LocalPath = %q, want staging input %q", cue.AssetRef.LocalPath, localPath)
	}
	wire, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(wire), localPath) {
		t.Fatal("producer-only cue LocalPath leaked onto semantic wire")
	}
	before := first.Items[0].RenderKey
	first.Items[0].SoundEffect.GainDB = -12
	first.Items[0].RenderKey = ""
	first.Fingerprint = ""
	if err := first.Validate(); err != nil {
		t.Fatal(err)
	}
	if first.Items[0].RenderKey == before || first.Fingerprint == second.Fingerprint {
		t.Fatal("cue content must affect render key and plan fingerprint")
	}
}

func TestAttachRandomOverlaySFXUsesEarliestCompositeLayerWindow(t *testing.T) {
	assetBytes := []byte("composite-audio")
	sum := sha256.Sum256(assetBytes)
	assetHash := hex.EncodeToString(sum[:])
	plan := &capoverlay.OverlayPlan{
		SchemaVersion: capoverlay.SchemaVersionPlan, PlanID: "composite-plan", VideoID: "video",
		Width: 1920, Height: 1080, FPSNum: 24, FPSDen: 1,
		Items: []capoverlay.OverlayItem{{
			ID: "composite", Kind: "entity_image", TemplateID: "image_popup", StartMs: 0, EndMs: 2000,
			AssetRefs: []capoverlay.OverlayAssetRef{
				{AssetID: "late", SHA256: strings.Repeat("b", 64), URL: "late.png"},
				{AssetID: "early", SHA256: strings.Repeat("c", 64), URL: "early.png"},
			},
			ImageLayers: []capoverlay.OverlayImageLayer{
				{ID: "late", AssetID: "late", StartMS: 500, EndMS: 1500},
				{ID: "early", AssetID: "early", StartMS: 100, EndMS: 220},
			},
		}},
	}
	if err := plan.Validate(); err != nil {
		t.Fatal(err)
	}
	if err := attachRandomOverlaySFXFromAssets([]*capoverlay.OverlayPlan{plan}, []randomOverlaySFXAsset{{path: "cue.m4a", sha256: assetHash}}); err != nil {
		t.Fatal(err)
	}
	cue := plan.Items[0].SoundEffect
	if cue == nil || cue.StartOffsetMS != 100 || cue.DurationMS != 120 {
		t.Fatalf("composite cue = %+v, want offset 100ms capped to first layer's 120ms", cue)
	}
}
