package scriptgeneration

import (
	"testing"

	capabilityentities "github.com/Marcuss-ops/PipelineGen/internal/capabilities/entities"
)

// TestOverlayDisplayAliasIsNeverAnIdentity pins the OVL-03 contract for the
// legacy "overlay-<scene>-<slug>" spelling: it exists only so a resolver card
// that predates the stable occurrence id can be joined to the canonical id that
// card already carries. It is never an identity: it is scene-scoped by
// construction (so it cannot even represent one entity across scenes) and it
// enters the join as an input key, never as a resolved canonical value.
func TestOverlayDisplayAliasIsNeverAnIdentity(t *testing.T) {
	if got, want := overlayDisplayAlias("scene-1", "Desiree Washington", ""), "overlay-scene-1-desiree-washington"; got != want {
		t.Fatalf("alias = %q, want %q", got, want)
	}

	// Scene-scoped by construction: the same entity in two scenes has two
	// different aliases, so an alias can never stand in for the entity across
	// scenes. If this ever becomes scene-independent it has turned into an
	// identity and the guard in overlay_plan_deletion_guard_test.go is moot.
	if a, b := overlayDisplayAlias("scene-1", "Roma", ""), overlayDisplayAlias("scene-2", "Roma", ""); a == b {
		t.Fatalf("aliases for the same entity in two scenes must differ, both were %q", a)
	}

	// A resolver card that only knows the alias resolves to the canonical id
	// the media index already holds: the alias is an input key, and the value
	// that comes out is the canonical id, never the alias.
	canonical := map[string]string{
		"overlay-scene-1-desiree-washington": "scene-1::person:desiree-washington",
	}
	occurrence := capabilityentities.EntityOccurrence{
		EntityID: "overlay-scene-1-desiree-washington",
		Name:     "Desiree Washington",
		Type:     "PERSON",
		SceneID:  "scene-1",
	}
	ids := entityAssetLookupIDs(occurrence, canonical)
	if len(ids) == 0 || ids[0] != "scene-1::person:desiree-washington" {
		t.Fatalf("alias lookup must resolve the canonical id first, got %v", ids)
	}
}
