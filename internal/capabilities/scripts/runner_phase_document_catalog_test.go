package scriptgeneration

import (
	"encoding/json"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaregistry"
)

// TestRemoteBackgroundCatalogIsProjectedFromTheRegistry pins that every
// curated plate reaches the remote assembler from the SAME registry that binds
// BGM and SFX — not from a second catalog maintained next to the payload.
func TestRemoteBackgroundCatalogIsProjectedFromTheRegistry(t *testing.T) {
	catalog := remoteBackgroundCatalog()
	assets := mediaregistry.EditorialBackgroundAssets()
	if len(catalog) != len(assets) {
		t.Fatalf("remote background catalog = %d entries, registry binds %d", len(catalog), len(assets))
	}
	for _, asset := range assets {
		row, ok := catalog[asset.ID]
		if !ok {
			t.Errorf("remote background catalog is missing %q", asset.ID)
			continue
		}
		if row["drive_file_id"] != asset.DriveFileID {
			t.Errorf("%s drive_file_id = %q, want %q", asset.ID, row["drive_file_id"], asset.DriveFileID)
		}
		if row["sha256"] != asset.SHA256 {
			t.Errorf("%s sha256 = %q, registry = %q", asset.ID, row["sha256"], asset.SHA256)
		}
		if row["media_type"] != asset.MediaType {
			t.Errorf("%s media_type = %q, want %q", asset.ID, row["media_type"], asset.MediaType)
		}
		if row["url"] != "velox-drive://"+asset.DriveFileID {
			t.Errorf("%s url = %q", asset.ID, row["url"])
		}
	}
}

// TestRemoteAudioCatalogsStayDisjointFromBackgrounds keeps the three projected
// catalogs from silently overlapping: an alias must belong to exactly one
// editorial domain, matching ValidateEditorialCatalog on the registry side.
func TestRemoteAudioCatalogsStayDisjointFromBackgrounds(t *testing.T) {
	backgrounds := remoteBackgroundCatalog()
	bgm := remoteBackgroundMusicCatalog()
	sfx := remoteSoundEffectCatalog()

	if len(bgm) == 0 || len(sfx) == 0 {
		t.Fatalf("audio catalogs are empty (bgm=%d sfx=%d)", len(bgm), len(sfx))
	}
	for alias := range bgm {
		if _, clash := backgrounds[alias]; clash {
			t.Errorf("alias %q is projected in both the background and BGM catalogs", alias)
		}
	}
	for alias := range sfx {
		if _, clash := backgrounds[alias]; clash {
			t.Errorf("alias %q is projected in both the background and SFX catalogs", alias)
		}
	}
}

// TestRemoteJobPayloadCarriesTheBackgroundCatalog proves the catalog is
// actually published in the sealed remote payload, not merely computed.
// TestRemoteEditingAssetsPolicyMatchesTheCanonicalPolicy pins the published
// policy to the registry default: the remote assembler may select from these
// pools, so a drift between the two would let it pick an asset the catalogs do
// not bind.
func TestRemoteEditingAssetsPolicyMatchesTheCanonicalPolicy(t *testing.T) {
	want := mediaregistry.DefaultEditingAssetsPolicy()
	got := remoteEditingAssetsPolicy()

	backgrounds, ok := got["backgrounds"].(map[string]any)
	if !ok {
		t.Fatalf("backgrounds policy has the wrong shape: %T", got["backgrounds"])
	}
	if pool, ok := backgrounds["pool"].([]string); !ok || !equalStringSlices(pool, want.Backgrounds.Pool) {
		t.Errorf("backgrounds pool = %v, want %v", backgrounds["pool"], want.Backgrounds.Pool)
	}

	bgm, ok := got["bgm"].(map[string]any)
	if !ok {
		t.Fatalf("bgm policy has the wrong shape: %T", got["bgm"])
	}
	if pool, ok := bgm["pool"].([]string); !ok || !equalStringSlices(pool, want.BGM.Pool) {
		t.Errorf("bgm pool = %v, want %v", bgm["pool"], want.BGM.Pool)
	}
	if bgm["gain_db"] != want.BGM.GainDB || bgm["duck_gain_db"] != want.BGM.DuckGainDB {
		t.Errorf("bgm gains = %v/%v, want %v/%v", bgm["gain_db"], bgm["duck_gain_db"], want.BGM.GainDB, want.BGM.DuckGainDB)
	}
	if bgm["loop"] != want.BGM.Loop || bgm["duck_under_voiceover"] != want.BGM.DuckUnderVoiceover {
		t.Errorf("bgm flags = loop:%v duck:%v, want loop:%v duck:%v", bgm["loop"], bgm["duck_under_voiceover"], want.BGM.Loop, want.BGM.DuckUnderVoiceover)
	}

	sfx, ok := got["sfx"].(map[string]any)
	if !ok {
		t.Fatalf("sfx policy has the wrong shape: %T", got["sfx"])
	}
	if pool, ok := sfx["transition_pool"].([]string); !ok || !equalStringSlices(pool, want.SFX.TransitionPool) {
		t.Errorf("sfx transition pool = %v, want %v", sfx["transition_pool"], want.SFX.TransitionPool)
	}
	if sfx["gain_db"] != want.SFX.GainDB {
		t.Errorf("sfx gain = %v, want %v", sfx["gain_db"], want.SFX.GainDB)
	}
}

func TestRemoteJobPayloadCarriesTheEditingAssetsPolicy(t *testing.T) {
	raw := buildRemoteJobPayload(GenerateRequest{}, nil)
	if len(raw) == 0 {
		t.Fatal("buildRemoteJobPayload returned an empty payload")
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("payload is not valid json: %v", err)
	}
	var remote map[string]json.RawMessage
	if err := json.Unmarshal(payload["remote_render"], &remote); err != nil {
		t.Fatalf("remote_render is not a json object: %v", err)
	}
	policyRaw, present := remote["editing_assets_policy"]
	if !present {
		t.Fatal("remote_render carries no editing_assets_policy")
	}
	var decoded struct {
		Backgrounds struct {
			Pool []string `json:"pool"`
		} `json:"backgrounds"`
		BGM struct {
			Pool []string `json:"pool"`
		} `json:"bgm"`
	}
	if err := json.Unmarshal(policyRaw, &decoded); err != nil {
		t.Fatalf("editing_assets_policy has the wrong shape: %v", err)
	}
	want := mediaregistry.DefaultEditingAssetsPolicy()
	if !equalStringSlices(decoded.Backgrounds.Pool, want.Backgrounds.Pool) {
		t.Errorf("published background pool = %v, want %v", decoded.Backgrounds.Pool, want.Backgrounds.Pool)
	}
	if !equalStringSlices(decoded.BGM.Pool, want.BGM.Pool) {
		t.Errorf("published bgm pool = %v, want %v", decoded.BGM.Pool, want.BGM.Pool)
	}
}

func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestRemoteJobPayloadCarriesTheBackgroundCatalog(t *testing.T) {
	raw := buildRemoteJobPayload(GenerateRequest{}, nil)
	if len(raw) == 0 {
		t.Fatal("buildRemoteJobPayload returned an empty payload")
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("payload is not valid json: %v", err)
	}
	remoteRaw, ok := payload["remote_render"]
	if !ok {
		t.Fatal("payload has no remote_render block")
	}
	var remote map[string]json.RawMessage
	if err := json.Unmarshal(remoteRaw, &remote); err != nil {
		t.Fatalf("remote_render is not a json object: %v", err)
	}
	catalogRaw, present := remote["background_catalog"]
	if !present {
		t.Fatal("remote_render carries no background_catalog")
	}
	var catalog map[string]map[string]string
	if err := json.Unmarshal(catalogRaw, &catalog); err != nil {
		t.Fatalf("background_catalog is not a string map: %v", err)
	}
	if len(catalog) != len(mediaregistry.EditorialBackgroundAssets()) {
		t.Fatalf("background_catalog has %d entries, want %d", len(catalog), len(mediaregistry.EditorialBackgroundAssets()))
	}
	if _, ok := catalog["drive-background-01"]; !ok {
		t.Fatal("background_catalog is missing drive-background-01")
	}
}
