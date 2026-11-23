package scriptgeneration

import (
	"strings"
	"testing"

	capabilityentities "github.com/Marcuss-ops/PipelineGen/internal/capabilities/entities"
)

// The plan joins every lane by the stable occurrence id. The canonical id is
// metadata projected from the identity that actually resolved one (annotation
// or media lane), never a copy of the stable id: OVL-03.
func TestEntityTimelineCarriesTheResolvedCanonicalIdentity(t *testing.T) {
	result := promotionSurfaceResult()
	media := overlayPlanMedia(result, Language("it"), nil)
	timeline := overlayPlanEntityTimeline(
		result,
		Language("it"),
		"project-1",
		map[string][2]int64{"scene-1": {0, 8_000_000}},
		overlayPlanCanonicalEntityIDs(result, media),
	)
	if len(timeline.Scenes) != 1 || len(timeline.Scenes[0].Entities) != 1 {
		t.Fatalf("timeline = %#v, want one occurrence", timeline.Scenes)
	}
	occurrence := timeline.Scenes[0].Entities[0]
	if occurrence.EntityID != "person-ada" {
		t.Fatalf("entity id = %q, want the stable occurrence identity", occurrence.EntityID)
	}
	if occurrence.CanonicalEntityID != "person:ada-lovelace" {
		t.Fatalf("canonical id = %q, want the resolved identity", occurrence.CanonicalEntityID)
	}
}

// An occurrence nobody resolved keeps an empty canonical id. The field must not
// claim canonicality by repeating the stable id.
// An Augias run repeats one entity across scenes. The stable occurrence id is
// the wire join in every scene; the canonical id rides alongside as metadata and
// never becomes the join key. This is the cross-scene case that the single-scene
// assertions above cannot see.
func TestAugiasCrossSceneEntityKeepsOneStableIdentity(t *testing.T) {
	result := promotionSurfaceResult()
	second := result.EntityTimeline.Scenes[0]
	second.SceneID = "scene-2"
	second.TimelineStartUS = 8_000_000
	second.Entities = append([]capabilityentities.EntityOccurrence(nil), second.Entities...)
	second.Entities[0].SceneID = "scene-2"
	second.Entities[0].TimelineStartUS = 8_000_000
	second.Entities[0].AudioStartUS = 8_500_000
	second.Entities[0].AudioEndUS = 9_500_000
	result.EntityTimeline.Scenes = append(result.EntityTimeline.Scenes, second)

	media := overlayPlanMedia(result, Language("it"), nil)
	timeline := overlayPlanEntityTimeline(
		result,
		Language("it"),
		"project-1",
		map[string][2]int64{"scene-1": {0, 8_000_000}, "scene-2": {8_000_000, 16_000_000}},
		overlayPlanCanonicalEntityIDs(result, media),
	)
	if len(timeline.Scenes) != 2 {
		t.Fatalf("timeline scenes = %d, want both scenes", len(timeline.Scenes))
	}
	for _, scene := range timeline.Scenes {
		if len(scene.Entities) != 1 {
			t.Fatalf("scene %s occurrences = %d, want one", scene.SceneID, len(scene.Entities))
		}
		occurrence := scene.Entities[0]
		if occurrence.EntityID != "person-ada" {
			t.Fatalf("scene %s entity id = %q, want the stable occurrence identity", scene.SceneID, occurrence.EntityID)
		}
		if occurrence.CanonicalEntityID != "person:ada-lovelace" {
			t.Fatalf("scene %s canonical id = %q, want the resolved identity", scene.SceneID, occurrence.CanonicalEntityID)
		}
		if strings.Contains(occurrence.EntityID, "overlay-") || strings.HasPrefix(occurrence.EntityID, "person:") || strings.Contains(occurrence.EntityID, "::") {
			t.Fatalf("scene %s entity id = %q, want no presentation alias or canonical join", scene.SceneID, occurrence.EntityID)
		}
		if occurrence.SceneID != scene.SceneID {
			t.Fatalf("occurrence scene = %q, want %q", occurrence.SceneID, scene.SceneID)
		}
	}
	if len(media.VerifiedEntityAssets) != 1 {
		t.Fatalf("verified assets = %d, want one entry for the shared occurrence identity", len(media.VerifiedEntityAssets))
	}
	if got := media.VerifiedEntityAssets[0].EntityID; got != "person-ada" {
		t.Fatalf("media entity id = %q, want the stable occurrence identity", got)
	}
}

// The v2 request carries the same two identities: entity_id is the stable
// occurrence id Rust plans by, canonical_entity_id is provenance. A GPE proves
// the rule is not PERSON-only: place:new-orleans is metadata, never the join.
func TestV2RequestCarriesTheStableJoinAndTheCanonicalMetadata(t *testing.T) {
	request := buildMapPlanV2RequestForFixture(t, mapPlanV2FixtureByName(t, "grounded_map"))
	if request.EntityTimeline == nil || len(request.EntityTimeline.Scenes) != 1 {
		t.Fatalf("entity timeline = %#v, want one scene", request.EntityTimeline)
	}
	occurrences := request.EntityTimeline.Scenes[0].Entities
	if len(occurrences) != 1 {
		t.Fatalf("occurrences = %d, want one", len(occurrences))
	}
	occurrence := occurrences[0]
	if occurrence.EntityID != "ent_5d06710f3ec2ac53" {
		t.Fatalf("entity_id = %q, want the stable occurrence id", occurrence.EntityID)
	}
	if occurrence.CanonicalEntityID != "place:new-orleans" {
		t.Fatalf("canonical_entity_id = %q, want the resolved canonical identity", occurrence.CanonicalEntityID)
	}
	if occurrence.CanonicalEntityID == occurrence.EntityID {
		t.Fatalf("canonical_entity_id repeats the stable join id %q", occurrence.EntityID)
	}
	if strings.Contains(occurrence.EntityID, "overlay-") || strings.Contains(occurrence.EntityID, "::") {
		t.Fatalf("entity_id = %q, want no presentation alias in the wire join", occurrence.EntityID)
	}
}

func TestEntityTimelineLeavesAnUnresolvedCanonicalIdentityEmpty(t *testing.T) {
	result := promotionSurfaceResult()
	result.SemanticRenderBundle = nil
	media := overlayPlanMedia(result, Language("it"), nil)
	timeline := overlayPlanEntityTimeline(
		result,
		Language("it"),
		"project-1",
		nil,
		overlayPlanCanonicalEntityIDs(result, media),
	)
	occurrence := timeline.Scenes[0].Entities[0]
	if occurrence.CanonicalEntityID != "" {
		t.Fatalf("canonical id = %q, want empty when no identity was resolved", occurrence.CanonicalEntityID)
	}
	if occurrence.CanonicalEntityID == occurrence.EntityID {
		t.Fatalf("canonical id repeats the stable id %q", occurrence.EntityID)
	}
}
