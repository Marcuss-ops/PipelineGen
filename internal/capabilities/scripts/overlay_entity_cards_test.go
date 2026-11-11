package scriptgeneration

import (
	"encoding/json"
	"fmt"
	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
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
	item := attachEntityCardAsset(capmodel.OverlayItem{
		ID: "ada-card", EntityID: stableID, Kind: string(capmodel.KindEntityCard),
		EntityRef:    &capmodel.OverlayEntityRef{EntityID: stableID, Type: "PERSON", Name: "Ada Lovelace"},
		MotionID:     "phrase_apple_clean_01_blur_soft_reveal",
		MotionParams: map[string]any{"stagger": 3},
	}, media, canonicalByStable, "plan-1")
	if item.Kind != string(capmodel.KindEntityImage) || len(item.AssetRefs) != 1 {
		t.Fatalf("entity image = %#v, want resolved image card", item)
	}
	if item.MotionID != "" || item.MotionParams != nil {
		t.Fatalf("entity image motion = %q params=%v, want run-level selection without stale text params", item.MotionID, item.MotionParams)
	}
	if item.AssetRefs[0].LocalPath != localPath {
		t.Fatalf("local path = %q, want verified producer path", item.AssetRefs[0].LocalPath)
	}
	if item.EntityCaption != "Ada Lovelace" || item.Text != "" {
		t.Fatalf("single-image caption/text = %q/%q, want visible entity caption and no generic image text", item.EntityCaption, item.Text)
	}
	wire, err := json.Marshal(item)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(wire), localPath) {
		t.Fatal("producer-local filesystem path leaked into the semantic overlay plan")
	}
	if !strings.Contains(string(wire), `"entity_caption":"Ada Lovelace"`) {
		t.Fatalf("single-image caption missing from semantic plan wire: %s", wire)
	}
}

func TestAttachEntityCardAssetResolvesLocalizedStableIdentity(t *testing.T) {
	image := &scriptpkg.EntityImageBinding{
		Status: "resolved", AssetID: "asset-mike-tyson", SHA256: strings.Repeat("a", 64),
		PreviewURL: "https://drive.google.com/uc?export=download&id=entity-image-person-mike-tyson",
		MediaType:  "image/jpeg",
	}
	result := &GenerateResult{Scenes: []Scene{{
		Annotations: &scriptpkg.SceneAnnotations{PrimaryEntities: []scriptpkg.AnnotatedEntity{{
			Type: "PERSON", CanonicalName: "Mike Tyson", CanonicalEntityID: "person:mike-tyson", Image: image,
		}}},
		LocalizedAnnotations: map[Language]*scriptpkg.SceneAnnotations{
			"pl": {PrimaryEntities: []scriptpkg.AnnotatedEntity{{
				Type: "PERSON", CanonicalName: "Mike’a Tysona", CanonicalEntityID: "person:mike-tyson", Image: image,
			}}},
		},
	}}}

	media, canonicalByStable := entityCardMediaIndex(result)
	localizedStableID := capabilityentities.StableEntityID("PERSON", "Mike’a Tysona")
	item := attachEntityCardAsset(capmodel.OverlayItem{
		ID: "localized-mike-card", EntityID: localizedStableID, Kind: string(capmodel.KindEntityCard),
	}, media, canonicalByStable, "plan-1")
	if item.Kind != string(capmodel.KindEntityImage) || len(item.AssetRefs) != 1 {
		t.Fatalf("localized entity image = %#v, want resolved image card", item)
	}
	if item.EntityRef == nil || item.EntityRef.CanonicalEntityID != "person:mike-tyson" {
		t.Fatalf("localized entity ref = %#v, want canonical source identity", item.EntityRef)
	}
}

func TestCapEntityImageOverlaysKeepsDistinctIdentitiesUpToRunCeiling(t *testing.T) {
	maxImages := capabilityoverlay.MaxEntityImageOverlaysPerRun
	items := make([]capmodel.OverlayItem, 0, maxImages+4)
	for i := 0; i < maxImages+3; i++ {
		entityID := fmt.Sprintf("entity-%d", i)
		items = append(items, capmodel.OverlayItem{
			ID:   "image-" + entityID,
			Kind: string(capmodel.KindEntityImage), EntityID: entityID,
		})
	}
	// A repeated occurrence must not consume a second slot.
	items = append(items, capmodel.OverlayItem{
		ID: "image-entity-0-repeat", Kind: string(capmodel.KindEntityImage), EntityID: "entity-0",
	})
	items = append(items, capmodel.OverlayItem{ID: "phrase", Kind: "text_phrase"})

	got := capEntityImageOverlays(items, capabilityoverlay.MaxEntityImageOverlaysPerRun)
	if len(got) != maxImages+1 {
		t.Fatalf("items=%d, want %d image items plus phrase", len(got), maxImages)
	}
	seen := map[string]bool{}
	for _, item := range got {
		if item.Kind == string(capmodel.KindEntityImage) {
			seen[item.EntityID] = true
		}
	}
	if len(seen) != capabilityoverlay.MaxEntityImageOverlaysPerRun {
		t.Fatalf("distinct image identities=%d, want %d", len(seen), capabilityoverlay.MaxEntityImageOverlaysPerRun)
	}
}

func TestCapEntityImageOverlaysPerSceneKeepsEntityOnlyOnceAcrossRun(t *testing.T) {
	items := []capmodel.OverlayItem{
		{ID: "scene-1-milton", SceneID: "scene-1", EntityID: "milton", Kind: string(capmodel.KindEntityImage), EntityRef: &capmodel.OverlayEntityRef{CanonicalEntityID: "person:milton-leite"}},
		{ID: "scene-2-milton", SceneID: "scene-2", EntityID: "milton", Kind: string(capmodel.KindEntityImage), EntityRef: &capmodel.OverlayEntityRef{CanonicalEntityID: "person:milton-leite"}},
		{ID: "scene-2-other", SceneID: "scene-2", EntityID: "other", Kind: string(capmodel.KindEntityImage), EntityRef: &capmodel.OverlayEntityRef{CanonicalEntityID: "person:other"}},
		{ID: "phrase", SceneID: "scene-2", Kind: "text_phrase"},
	}
	got := capEntityImageOverlays(items, capabilityoverlay.MaxEntityImageOverlaysPerRun, true)
	if len(got) != 3 || got[0].ID != "scene-1-milton" || got[1].ID != "scene-2-other" || got[2].Kind != "text_phrase" {
		t.Fatalf("per-scene image cap = %+v; want repeated Milton omitted and unique entity plus phrase kept", got)
	}
}

func TestCapEntityImageOverlaysPerSceneOnlyDropsRepeatedPhotoHashWithinScene(t *testing.T) {
	items := []capmodel.OverlayItem{
		{ID: "scene-1-first", SceneID: "scene-1", EntityID: "person-a", Kind: string(capmodel.KindEntityImage), EntityRef: &capmodel.OverlayEntityRef{CanonicalEntityID: "person:a"}, AssetRefs: []capmodel.OverlayAssetRef{{AssetID: "asset-a", SHA256: "same-photo"}}},
		{ID: "scene-1-duplicate", SceneID: "scene-1", EntityID: "person-a-copy", Kind: string(capmodel.KindEntityImage), EntityRef: &capmodel.OverlayEntityRef{CanonicalEntityID: "person:a-copy"}, AssetRefs: []capmodel.OverlayAssetRef{{AssetID: "asset-a-copy", SHA256: "same-photo"}}},
		{ID: "scene-2-second", SceneID: "scene-2", EntityID: "person-a", Kind: string(capmodel.KindEntityImage), EntityRef: &capmodel.OverlayEntityRef{CanonicalEntityID: "person:a"}, AssetRefs: []capmodel.OverlayAssetRef{{AssetID: "asset-b", SHA256: "same-photo"}}},
		{ID: "scene-3-third", SceneID: "scene-3", EntityID: "person-a", Kind: string(capmodel.KindEntityImage), EntityRef: &capmodel.OverlayEntityRef{CanonicalEntityID: "person:a"}, AssetRefs: []capmodel.OverlayAssetRef{{AssetID: "asset-c", SHA256: "different-photo"}}},
	}
	got := capEntityImageOverlays(items, capabilityoverlay.MaxEntityImageOverlaysPerRun, true)
	if len(got) != 3 || got[0].ID != "scene-1-first" || got[1].ID != "scene-2-second" || got[2].ID != "scene-3-third" {
		t.Fatalf("per-scene photo dedupe = %+v; want same-scene duplicate dropped and later reuse retained", got)
	}
}

func TestNamedEntityCardCeilingDoesNotLimitImageCompositeChildren(t *testing.T) {
	items := make([]capmodel.OverlayItem, 0, 5+maxNamedEntityCardsPerScene+1)
	for index := 0; index < 5; index++ {
		id := fmt.Sprintf("image-%d", index)
		items = append(items, capmodel.OverlayItem{
			ID: id, SceneID: "scene", EntityID: id, Kind: string(capmodel.KindEntityImage),
			StartMs: int64(index * 100), EndMs: int64(index*100 + 5000),
			EntityRef: &capmodel.OverlayEntityRef{EntityID: id, Type: "PERSON", Name: id},
			AssetRefs: []capmodel.OverlayAssetRef{{AssetID: "asset-" + id, SHA256: "sha-" + id}},
		})
	}
	for index := 0; index < maxNamedEntityCardsPerScene+1; index++ {
		items = append(items, capmodel.OverlayItem{
			ID: fmt.Sprintf("card-%d", index), SceneID: "scene", EntityID: fmt.Sprintf("card-ent-%d", index),
			Kind: string(capmodel.KindEntityCard), Text: fmt.Sprintf("Named entity %d", index),
		})
	}
	items = capNamedEntityCardsPerScene(items, maxNamedEntityCardsPerScene)
	cards := 0
	images := 0
	for _, item := range items {
		if item.Kind == string(capmodel.KindEntityCard) {
			cards++
		} else if item.Kind == string(capmodel.KindEntityImage) {
			images++
		}
	}
	if cards != maxNamedEntityCardsPerScene || images != 5 {
		t.Fatalf("per-scene cap retained %d named cards and %d image entities; want %d and 5", cards, images, maxNamedEntityCardsPerScene)
	}
}

func TestCapEntityImageOverlaysDeduplicatesSameCanonicalEntity(t *testing.T) {
	items := []capmodel.OverlayItem{
		{ID: "scene-1", EntityID: "occurrence-1", Kind: string(capmodel.KindEntityImage),
			EntityRef: &capmodel.OverlayEntityRef{CanonicalEntityID: "person:mike-tyson"},
			AssetRefs: []capmodel.OverlayAssetRef{{SHA256: "portrait-sha"}}},
		{ID: "scene-2", EntityID: "occurrence-2", Kind: string(capmodel.KindEntityImage),
			EntityRef: &capmodel.OverlayEntityRef{CanonicalEntityID: "person:mike-tyson"},
			AssetRefs: []capmodel.OverlayAssetRef{{SHA256: "portrait-sha"}}},
		{ID: "scene-3", EntityID: "occurrence-3", Kind: string(capmodel.KindEntityImage),
			EntityRef: &capmodel.OverlayEntityRef{CanonicalEntityID: "person:evander-holyfield"},
			AssetRefs: []capmodel.OverlayAssetRef{{SHA256: "other-sha"}}},
	}

	got := capEntityImageOverlays(items, capabilityoverlay.MaxEntityImageOverlaysPerRun)
	if len(got) != 2 {
		t.Fatalf("items=%d, want one Tyson image plus one other entity", len(got))
	}
	if got[0].EntityRef == nil || got[0].EntityRef.CanonicalEntityID != "person:mike-tyson" {
		t.Fatalf("first retained image = %#v, want Mike Tyson", got[0])
	}
}

func TestCapEntityImageOverlaysDeduplicatesIdentityInPerSceneScope(t *testing.T) {
	items := []capmodel.OverlayItem{
		{ID: "scene-1", SceneID: "scene-1", EntityID: "occurrence-1", Kind: string(capmodel.KindEntityImage),
			EntityRef: &capmodel.OverlayEntityRef{CanonicalEntityID: "person:isabelle-caracristi"}},
		{ID: "scene-2", SceneID: "scene-2", EntityID: "occurrence-2", Kind: string(capmodel.KindEntityImage),
			EntityRef: &capmodel.OverlayEntityRef{CanonicalEntityID: "person:isabelle-caracristi"}},
	}

	got := capEntityImageOverlays(items, capabilityoverlay.MaxEntityImageOverlaysPerRun, true)
	if len(got) != 1 {
		t.Fatalf("per-scene image count = %d, want one render for repeated identity", len(got))
	}
	if got[0].SceneID != "scene-1" {
		t.Fatalf("retained scene = %q, want first occurrence in scene-1", got[0].SceneID)
	}
}

func TestApplyHeavySentenceBoostPilotsPhrasePriority(t *testing.T) {
	in := &capabilityoverlay.SceneInput{ID: "s1", Phrases: []capabilityoverlay.TimedAnnotation{
		{Text: "il raccolto è crollato", Score: 0.5},
		{Text: "una frase qualunque", Score: 0.5},
	}}
	heavies := []scriptpkg.ImportantSentence{
		{Index: 0, Text: "Il raccolto è crollato a causa della siccità estiva.", Importance: 0.95},
	}
	applyHeavySentenceBoost(in, heavies)
	if in.Phrases[0].Score != 0.95 {
		t.Fatalf("matching phrase must inherit heavy importance, got %v", in.Phrases[0].Score)
	}
	if in.Phrases[1].Score != 0.5 {
		t.Fatalf("non-matching phrase must keep its score, got %v", in.Phrases[1].Score)
	}
}

func TestApplyHeavySentenceBoostNeverLowersAndToleratesEmpty(t *testing.T) {
	in := &capabilityoverlay.SceneInput{ID: "s1", Phrases: []capabilityoverlay.TimedAnnotation{
		{Text: "il raccolto è crollato", Score: 0.98},
	}}
	applyHeavySentenceBoost(in, []scriptpkg.ImportantSentence{
		{Index: 0, Text: "Il raccolto è crollato.", Importance: 0.4},
	})
	if in.Phrases[0].Score != 0.98 {
		t.Fatalf("scores must only rise, got %v", in.Phrases[0].Score)
	}
	applyHeavySentenceBoost(nil, nil)
	applyHeavySentenceBoost(&capabilityoverlay.SceneInput{}, nil)
}
