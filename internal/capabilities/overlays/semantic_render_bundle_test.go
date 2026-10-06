package overlays

import "testing"

func TestSemanticRenderBundleV1_RejectsAssetWithoutMatchingEntity(t *testing.T) {
	// An asset that names an entity the bundle does not carry is an
	// unjoinable provenance record. Validate must fail closed rather than
	// allowing a downstream consumer to silently downgrade the image.
	scene := NewSceneIR("scene-1", 0, "Gerard Butler spoke in London.", "", SegmentSemanticProfile{})
	b := SemanticRenderBundleV1{
		Version: SemanticRenderBundleVersion,
		RunID:   "run-1",
		Scene:   scene,
		Entities: []ResolvedEntity{{
			EntityID: "ent_gerard", Type: "PERSON", Text: "Gerard Butler", CanonicalText: "Gerard Butler",
			Evidence: "Gerard Butler", Start: 0, End: 13, Confidence: .97, SceneID: "scene-1",
		}},
		Assets: []BoundAsset{{
			// EntityID belongs to no bundle entity: the join must fail.
			EntityID: "ent_ghost", AssetID: "asset-1",
			ContentHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			SourceURL:   "https://example.test/gerard.jpg", Verified: true,
		}},
	}
	if err := b.Validate(); err == nil {
		t.Fatal("expected asset↔entity join validation failure")
	}
}

func TestSemanticRenderBundleV1_AcceptsJoinableAsset(t *testing.T) {
	// The same bundle with the asset joined to the real entity validates.
	scene := NewSceneIR("scene-1", 0, "Gerard Butler spoke in London.", "", SegmentSemanticProfile{})
	b := SemanticRenderBundleV1{
		Version: SemanticRenderBundleVersion,
		RunID:   "run-1",
		Scene:   scene,
		Entities: []ResolvedEntity{{
			EntityID: "ent_gerard", Type: "PERSON", Text: "Gerard Butler", CanonicalText: "Gerard Butler",
			Evidence: "Gerard Butler", Start: 0, End: 13, Confidence: .97, SceneID: "scene-1",
		}},
		Assets: []BoundAsset{{
			EntityID: "ent_gerard", AssetID: "asset-1",
			ContentHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			SourceURL:   "https://example.test/gerard.jpg", Verified: true,
		}},
	}
	if err := b.Validate(); err != nil {
		t.Fatalf("joinable asset must validate: %v", err)
	}
}

func TestSemanticRenderBundleV1_RejectsUngroundedEntity(t *testing.T) {
	b := SemanticRenderBundleV1{
		Version:  SemanticRenderBundleVersion,
		RunID:    "run-1",
		Scene:    NewSceneIR("scene-1", 0, "Gerard Butler spoke in London.", "", SegmentSemanticProfile{}),
		Entities: []ResolvedEntity{{EntityID: "person:gerard-butler", Type: "PERSON", Text: "Gerard", CanonicalText: "Gerard Butler", Evidence: "Gerard Butler", Start: 0, End: 13, Confidence: .97, SceneID: "scene-1"}},
	}
	if err := b.Validate(); err == nil {
		t.Fatal("expected source-grounding validation failure")
	}
}
