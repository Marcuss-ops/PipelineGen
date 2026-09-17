package scriptgeneration

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	capabilityentities "github.com/Marcuss-ops/PipelineGen/internal/capabilities/entities"
	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func TestAttachEntityCardAssetCarriesVerifiedLocalPathWithoutSerializingIt(t *testing.T) {
	localPath := filepath.Join(t.TempDir(), "verified-person.jpg")
	if err := os.WriteFile(localPath, []byte("verified image"), 0o600); err != nil {
		t.Fatal(err)
	}
	result := &GenerateResult{
		Scenes: []Scene{{Annotations: &scriptpkg.SceneAnnotations{
			PrimaryEntities: []scriptpkg.AnnotatedEntity{{
				ID: "person-1", Type: "PERSON", CanonicalName: "Ada Lovelace", Confidence: 0.9,
				Image: &scriptpkg.EntityImageBinding{
					Status: "resolved", AssetID: "ada-asset", SHA256: "ada-sha",
					PreviewURL: "https://drive.google.com/uc?export=download&id=ada",
					MediaType:  "image/jpeg",
				},
			},
			}}}},
		Segments: []scriptpkg.VidRushSegmentResult{{Assets: scriptpkg.SegmentAssetSelection{
			Candidates: []scriptpkg.SegmentAssetCandidate{{AssetID: "ada-asset", LocalPath: localPath}},
		}}},
	}
	media, canonicalByStable := entityCardMediaIndex(result)
	stableID := capabilityentities.StableEntityID("PERSON", "Ada Lovelace")
	item := attachEntityCardAsset(capabilityoverlay.OverlayItem{
		ID: "ada-card", EntityID: stableID, Kind: string(capabilityoverlay.KindEntityCard),
	}, media, canonicalByStable, "plan-1")
	if item.Kind != string(capabilityoverlay.KindEntityImage) || len(item.AssetRefs) != 1 {
		t.Fatalf("entity image = %#v, want resolved image card", item)
	}
	if item.AssetRefs[0].LocalPath != localPath {
		t.Fatalf("local path = %q, want verified producer path", item.AssetRefs[0].LocalPath)
	}
	wire, err := json.Marshal(item)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(wire), localPath) {
		t.Fatal("producer-local filesystem path leaked into the semantic overlay plan")
	}
}

func TestCapEntityImageOverlaysKeepsFiveDistinctIdentities(t *testing.T) {
	items := make([]capabilityoverlay.OverlayItem, 0, 8)
	for i, entityID := range []string{"a", "b", "a", "c", "d", "e", "f", "g"} {
		items = append(items, capabilityoverlay.OverlayItem{
			ID:   "image-" + entityID + "-" + string(rune('0'+i)),
			Kind: string(capabilityoverlay.KindEntityImage), EntityID: entityID,
		})
	}
	items = append(items, capabilityoverlay.OverlayItem{ID: "phrase", Kind: "text_phrase"})

	got := capEntityImageOverlays(items, capabilityoverlay.MaxEntityImageOverlaysPerRun)
	if len(got) != 6 {
		t.Fatalf("items=%d, want five image items plus phrase", len(got))
	}
	seen := map[string]bool{}
	for _, item := range got {
		if item.Kind == string(capabilityoverlay.KindEntityImage) {
			seen[item.EntityID] = true
		}
	}
	if len(seen) != capabilityoverlay.MaxEntityImageOverlaysPerRun {
		t.Fatalf("distinct image identities=%d, want %d", len(seen), capabilityoverlay.MaxEntityImageOverlaysPerRun)
	}
}

func TestCapEntityImageOverlaysDeduplicatesSameCanonicalEntity(t *testing.T) {
	items := []capabilityoverlay.OverlayItem{
		{ID: "scene-1", EntityID: "occurrence-1", Kind: string(capabilityoverlay.KindEntityImage),
			EntityRef: &capabilityoverlay.OverlayEntityRef{CanonicalEntityID: "person:mike-tyson"},
			AssetRefs: []capabilityoverlay.OverlayAssetRef{{SHA256: "portrait-sha"}}},
		{ID: "scene-2", EntityID: "occurrence-2", Kind: string(capabilityoverlay.KindEntityImage),
			EntityRef: &capabilityoverlay.OverlayEntityRef{CanonicalEntityID: "person:mike-tyson"},
			AssetRefs: []capabilityoverlay.OverlayAssetRef{{SHA256: "portrait-sha"}}},
		{ID: "scene-3", EntityID: "occurrence-3", Kind: string(capabilityoverlay.KindEntityImage),
			EntityRef: &capabilityoverlay.OverlayEntityRef{CanonicalEntityID: "person:evander-holyfield"},
			AssetRefs: []capabilityoverlay.OverlayAssetRef{{SHA256: "other-sha"}}},
	}

	got := capEntityImageOverlays(items, capabilityoverlay.MaxEntityImageOverlaysPerRun)
	if len(got) != 2 {
		t.Fatalf("items=%d, want one Tyson image plus one other entity", len(got))
	}
	if got[0].EntityRef == nil || got[0].EntityRef.CanonicalEntityID != "person:mike-tyson" {
		t.Fatalf("first retained image = %#v, want Mike Tyson", got[0])
	}
}
