package render

import (
	"strings"
	"testing"
)

func hash64(c byte) string { return strings.Repeat(string(c), 64) }

func validSceneFingerprintInput() SceneFingerprintInput {
	return SceneFingerprintInput{
		Version:           SceneFingerprintVersion,
		SceneID:           "scene_07",
		VideoAssets:       []AssetFingerprint{{AssetID: "clip-a", SHA256: hash64('a')}, {AssetID: "clip-b", SHA256: hash64('b')}},
		TimelineHash:      hash64('c'),
		OverlayPlanHash:   hash64('d'),
		SubtitlePlanHash:  hash64('e'),
		AudioPlanHash:     hash64('f'),
		OutputProfileHash: hash64('0'),
		RendererVersion:   "rust-render/v3",
	}
}

func TestComputeSceneFingerprintIsDeterministic(t *testing.T) {
	first, err := ComputeSceneFingerprint(validSceneFingerprintInput())
	if err != nil {
		t.Fatal(err)
	}
	second, err := ComputeSceneFingerprint(validSceneFingerprintInput())
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("fingerprint must be deterministic: %q != %q", first, second)
	}
	if len(first) != 64 {
		t.Fatalf("fingerprint must be a 64-hex SHA256, got %q", first)
	}
}

func TestComputeSceneFingerprintIgnoresAssetOrder(t *testing.T) {
	input := validSceneFingerprintInput()
	reversed := validSceneFingerprintInput()
	reversed.VideoAssets = []AssetFingerprint{{AssetID: "clip-b", SHA256: hash64('b')}, {AssetID: "clip-a", SHA256: hash64('a')}}
	got, err := ComputeSceneFingerprint(input)
	if err != nil {
		t.Fatal(err)
	}
	want, err := ComputeSceneFingerprint(reversed)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("asset order must not change the fingerprint: %q != %q", got, want)
	}
}

func TestComputeSceneFingerprintChangesWithAnyInput(t *testing.T) {
	base, err := ComputeSceneFingerprint(validSceneFingerprintInput())
	if err != nil {
		t.Fatal(err)
	}
	mutations := map[string]SceneFingerprintInput{
		"scene id":     sceneWith(validSceneFingerprintInput(), func(i *SceneFingerprintInput) { i.SceneID = "scene_08" }),
		"asset id":     sceneWith(validSceneFingerprintInput(), func(i *SceneFingerprintInput) { i.VideoAssets[0].AssetID = "clip-c" }),
		"asset sha256": sceneWith(validSceneFingerprintInput(), func(i *SceneFingerprintInput) { i.VideoAssets[0].SHA256 = hash64('9') }),
		"added asset": sceneWith(validSceneFingerprintInput(), func(i *SceneFingerprintInput) {
			i.VideoAssets = append(i.VideoAssets, AssetFingerprint{AssetID: "clip-c", SHA256: hash64('c')})
		}),
		"timeline hash":   sceneWith(validSceneFingerprintInput(), func(i *SceneFingerprintInput) { i.TimelineHash = hash64('9') }),
		"overlay hash":    sceneWith(validSceneFingerprintInput(), func(i *SceneFingerprintInput) { i.OverlayPlanHash = hash64('9') }),
		"subtitle hash":   sceneWith(validSceneFingerprintInput(), func(i *SceneFingerprintInput) { i.SubtitlePlanHash = hash64('9') }),
		"audio plan hash": sceneWith(validSceneFingerprintInput(), func(i *SceneFingerprintInput) { i.AudioPlanHash = hash64('9') }),
		"output profile":  sceneWith(validSceneFingerprintInput(), func(i *SceneFingerprintInput) { i.OutputProfileHash = hash64('9') }),
		"renderer":        sceneWith(validSceneFingerprintInput(), func(i *SceneFingerprintInput) { i.RendererVersion = "rust-render/v4" }),
	}
	for name, mutated := range mutations {
		got, err := ComputeSceneFingerprint(mutated)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got == base {
			t.Errorf("%s must change the fingerprint", name)
		}
	}
	// An overlay appearing/disappearing changes the identity even though the
	// field is optional.
	withoutOverlay := validSceneFingerprintInput()
	withoutOverlay.OverlayPlanHash = ""
	got, err := ComputeSceneFingerprint(withoutOverlay)
	if err != nil {
		t.Fatal(err)
	}
	if got == base {
		t.Error("removing the overlay plan hash must change the fingerprint")
	}
}

func TestComputeSceneFingerprintRejectsInvalidInput(t *testing.T) {
	cases := map[string]SceneFingerprintInput{
		"unsupported version":     sceneWith(validSceneFingerprintInput(), func(i *SceneFingerprintInput) { i.Version = 2 }),
		"missing scene id":        sceneWith(validSceneFingerprintInput(), func(i *SceneFingerprintInput) { i.SceneID = "  " }),
		"no video assets":         sceneWith(validSceneFingerprintInput(), func(i *SceneFingerprintInput) { i.VideoAssets = nil }),
		"asset without id":        sceneWith(validSceneFingerprintInput(), func(i *SceneFingerprintInput) { i.VideoAssets[0].AssetID = "" }),
		"asset with bad sha256":   sceneWith(validSceneFingerprintInput(), func(i *SceneFingerprintInput) { i.VideoAssets[0].SHA256 = "not-a-hash" }),
		"duplicate asset":         sceneWith(validSceneFingerprintInput(), func(i *SceneFingerprintInput) { i.VideoAssets = append(i.VideoAssets, i.VideoAssets[0]) }),
		"missing timeline hash":   sceneWith(validSceneFingerprintInput(), func(i *SceneFingerprintInput) { i.TimelineHash = "" }),
		"bad timeline hash":       sceneWith(validSceneFingerprintInput(), func(i *SceneFingerprintInput) { i.TimelineHash = hash64('g') }),
		"bad overlay hash":        sceneWith(validSceneFingerprintInput(), func(i *SceneFingerprintInput) { i.OverlayPlanHash = "short" }),
		"bad subtitle hash":       sceneWith(validSceneFingerprintInput(), func(i *SceneFingerprintInput) { i.SubtitlePlanHash = "not-hex" }),
		"missing audio plan hash": sceneWith(validSceneFingerprintInput(), func(i *SceneFingerprintInput) { i.AudioPlanHash = "" }),
		"missing output profile":  sceneWith(validSceneFingerprintInput(), func(i *SceneFingerprintInput) { i.OutputProfileHash = "" }),
		"missing renderer":        sceneWith(validSceneFingerprintInput(), func(i *SceneFingerprintInput) { i.RendererVersion = "" }),
	}
	for name, input := range cases {
		if _, err := ComputeSceneFingerprint(input); err == nil {
			t.Errorf("%s must be rejected", name)
		}
	}
}

func TestComputeSceneFingerprintAllowsEmptyOverlayAndSubtitleHashes(t *testing.T) {
	input := validSceneFingerprintInput()
	input.OverlayPlanHash = ""
	input.SubtitlePlanHash = ""
	if _, err := ComputeSceneFingerprint(input); err != nil {
		t.Fatalf("scenes without overlays/subtitles must fingerprint cleanly: %v", err)
	}
}

func TestAssetsFromManifestDropsStagingDetails(t *testing.T) {
	manifest := []AssetManifestEntry{
		{AssetID: "clip-a", Path: "/tmp/staging/a.mp4", SHA256: hash64('a'), FrameCount: 2000},
		{AssetID: "clip-b", Path: "/var/cas/b.mp4", SHA256: hash64('b'), FrameCount: 1000},
	}
	assets := AssetsFromManifest(manifest)
	if len(assets) != 2 {
		t.Fatalf("expected 2 projected assets, got %d", len(assets))
	}
	for i, asset := range assets {
		if asset.AssetID != manifest[i].AssetID || asset.SHA256 != manifest[i].SHA256 {
			t.Fatalf("projection lost identity at %d: %+v", i, asset)
		}
	}
	if _, err := ComputeSceneFingerprint(SceneFingerprintInput{
		Version:           SceneFingerprintVersion,
		SceneID:           "scene_01",
		VideoAssets:       assets,
		TimelineHash:      hash64('c'),
		AudioPlanHash:     hash64('f'),
		OutputProfileHash: hash64('0'),
		RendererVersion:   "rust-render/v3",
	}); err != nil {
		t.Fatalf("manifest-projected assets must fingerprint cleanly: %v", err)
	}
}

// sceneWith clones the input and applies the mutation to the clone, so tests
// never mutate the shared valid fixture.
func sceneWith(input SceneFingerprintInput, mutate func(*SceneFingerprintInput)) SceneFingerprintInput {
	mutated := input
	mutated.VideoAssets = append([]AssetFingerprint(nil), input.VideoAssets...)
	mutate(&mutated)
	return mutated
}
