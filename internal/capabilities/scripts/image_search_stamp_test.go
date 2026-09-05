// Package scriptgeneration — image_search_stamp_test.go certifies the
// canonical-identity stamping contract: the Image Search Intent resolver's
// canonical_entity_id decisions are stamped into every segment's insights by
// the coordinator, and both annotation projections (projectEntityAnnotations
// and the batch sceneAnnotations merger) join on that stamped identity —
// never a re-derivation. It also certifies the PRODUCT/LOGO taxonomy path and
// the SHA256 propagation into the overlay plan's product/logo candidates.
package scriptgeneration

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	capabilityentities "github.com/Marcuss-ops/PipelineGen/internal/capabilities/entities"
	capabilityimagesearch "github.com/Marcuss-ops/PipelineGen/internal/capabilities/imagesearch"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// TestIncrementalCoordinator_StampsImageSearchCanonicalIDs certifies that a
// wired Image Search Intent resolver rides the run: every segment result
// carries the resolver's canonical_entity_id decisions (keyed by the
// lowercased surface) plus the primary entity's canonical id, so the
// annotation projection joins on the SAME identity the resolver chose.
func TestIncrementalCoordinator_StampsImageSearchCanonicalIDs(t *testing.T) {
	enricher := &fakeSegmentEnricher{errs: map[string]error{}}
	coordinator := NewVidRushIncrementalCoordinator(enricher, nil, 4)
	coordinator.SetImageSearchResolver(capabilityimagesearch.NewResolver(nil))

	text := "Floyd Mayweather defeated Manny Pacquiao in the ring."
	commit(t, coordinator, "run-1", "scene-0", 0, text, 1)

	results, err := coordinator.Wait(context.Background())
	require.NoError(t, err)
	require.Len(t, results, 1)
	ids := results[0].Insights.ImageEntityCanonicalIDs
	require.NotEmpty(t, ids, "the resolver decision must be stamped into the segment insights")
	assert.Equal(t, "person:floyd-mayweather", ids["floyd mayweather"])
	assert.Equal(t, "person:manny-pacquiao", ids["manny pacquiao"])
	assert.Equal(t, "person:floyd-mayweather", results[0].Insights.ImagePrimaryCanonicalID)
}

// TestIncrementalCoordinator_StampsProductCanonicalID certifies that the
// stamped decision covers the PRODUCT taxonomy (product:slug ids), which the
// planner needs to render the product image under the resolver's identity.
func TestIncrementalCoordinator_StampsProductCanonicalID(t *testing.T) {
	enricher := &fakeSegmentEnricher{errs: map[string]error{}}
	coordinator := NewVidRushIncrementalCoordinator(enricher, nil, 4)
	coordinator.SetImageSearchResolver(capabilityimagesearch.NewResolver(nil))

	commit(t, coordinator, "run-1", "scene-0", 0, "Apple unveiled the Vision Pro at the event.", 1)

	results, err := coordinator.Wait(context.Background())
	require.NoError(t, err)
	require.Len(t, results, 1)
	ids := results[0].Insights.ImageEntityCanonicalIDs
	require.NotEmpty(t, ids)
	assert.Equal(t, "product:apple-vision-pro", ids["apple vision pro"])
}

// TestIncrementalCoordinator_NoResolverKeepsLegacyDerivation certifies that
// an unwired resolver is a no-op: insights stay unstamped and the annotation
// projection falls back to the deterministic (type, name) derivation.
func TestIncrementalCoordinator_NoResolverKeepsLegacyDerivation(t *testing.T) {
	enricher := &fakeSegmentEnricher{errs: map[string]error{}}
	coordinator := NewVidRushIncrementalCoordinator(enricher, nil, 4)

	commit(t, coordinator, "run-1", "scene-0", 0, "Floyd Mayweather won the fight.", 1)

	results, err := coordinator.Wait(context.Background())
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Empty(t, results[0].Insights.ImageEntityCanonicalIDs)
	assert.Empty(t, results[0].Insights.ImagePrimaryCanonicalID)
}

// TestProjectEntityAnnotations_StampsResolverCanonicalID certifies the runner
// annotation projection stamps the stamped canonical_entity_id onto every
// annotation entity — the join key of the overlay media index.
func TestProjectEntityAnnotations_StampsResolverCanonicalID(t *testing.T) {
	seg := scriptpkg.VidRushSegmentResult{
		SegmentID: "scene-1",
		Insights: scriptpkg.SegmentInsights{
			Entities: []scriptpkg.ExtractedEntity{{Value: "Floyd Mayweather", Type: "PERSON", Confidence: 0.98}},
			ImageEntityCanonicalIDs: map[string]string{
				"floyd mayweather": "person:floyd-mayweather",
			},
		},
	}
	ann := projectEntityAnnotations("Floyd Mayweather dominated the fight.", "en", seg)
	require.NotNil(t, ann)
	require.Len(t, ann.PrimaryEntities, 1)
	assert.Equal(t, "person:floyd-mayweather", ann.PrimaryEntities[0].CanonicalEntityID)
}

// TestProjectEntityAnnotations_ProductIsPrimaryAndStamped certifies the
// PRODUCT taxonomy on the runner path: a PRODUCT entity survives the
// normalizer as PRODUCT (never CONCEPT), lands in PrimaryEntities (the
// primary/media imageable set) and carries the resolver's stamped canonical
// id.
func TestProjectEntityAnnotations_ProductIsPrimaryAndStamped(t *testing.T) {
	seg := scriptpkg.VidRushSegmentResult{
		SegmentID: "scene-1",
		Insights: scriptpkg.SegmentInsights{
			Entities: []scriptpkg.ExtractedEntity{{Value: "Vision Pro", Type: "PRODUCT", Confidence: 0.95}},
			ImageEntityCanonicalIDs: map[string]string{
				"vision pro": "product:apple-vision-pro",
			},
		},
	}
	ann := projectEntityAnnotations("Apple unveiled the Vision Pro at the event.", "en", seg)
	require.NotNil(t, ann)
	require.Len(t, ann.PrimaryEntities, 1)
	entity := ann.PrimaryEntities[0]
	assert.Equal(t, "PRODUCT", entity.Type, "PRODUCT must never collapse to CONCEPT")
	assert.Equal(t, "product:apple-vision-pro", entity.CanonicalEntityID)
}

// TestProjectEntityAnnotations_LogoIsPrimary certifies the LOGO taxonomy on
// the runner path: LOGO entities land in the primary imageable set.
func TestProjectEntityAnnotations_LogoIsPrimary(t *testing.T) {
	seg := scriptpkg.VidRushSegmentResult{
		SegmentID: "scene-1",
		Insights: scriptpkg.SegmentInsights{
			Entities: []scriptpkg.ExtractedEntity{{Value: "Apple", Type: "LOGO", Confidence: 0.97}},
			ImageEntityCanonicalIDs: map[string]string{
				"apple": "logo:apple",
			},
		},
	}
	ann := projectEntityAnnotations("Apple changed everything.", "en", seg)
	require.NotNil(t, ann)
	require.Len(t, ann.PrimaryEntities, 1)
	assert.Equal(t, "LOGO", ann.PrimaryEntities[0].Type)
	assert.Equal(t, "logo:apple", ann.PrimaryEntities[0].CanonicalEntityID)
}

// TestImageCandidateCarriesSHA256 certifies the product/logo bridge keeps the
// binding's verified content address: an image candidate without SHA256 would
// silently lose its asset from the queue render manifest.
func TestImageCandidateCarriesSHA256(t *testing.T) {
	binding := &scriptpkg.EntityImageBinding{
		Status: "resolved", AssetID: "vision-pro-img",
		PreviewURL: "https://cdn.example.com/vision-pro.png",
		SHA256:     "cc33dd44ee55ff66778899aabbccddeeff00112233445566778899aabbccdd",
		Source:     "internet_images",
	}
	occ := &capabilityentities.EntityOccurrence{
		EntityID: "ent_visionpro", Name: "Vision Pro", Type: "PRODUCT",
		AudioStartUS: 1_300_000, AudioEndUS: 1_500_000,
	}
	candidate := imageCandidate(binding, occ, 0.95)
	assert.Equal(t, binding.AssetID, candidate.AssetID)
	assert.Equal(t, binding.PreviewURL, candidate.URL)
	assert.Equal(t, binding.SHA256, candidate.SHA256, "the verified content address must cross the bridge")
	assert.Equal(t, "image", candidate.MediaType)
	assert.Equal(t, int64(1300), candidate.StartMs)
	assert.Equal(t, int64(1500), candidate.EndMs)
}

// TestOverlaySceneInput_ProductWithSHA256ProducesContentAddressedItem
// certifies the end-to-end planner projection: a PRODUCT entity with a
// certified occurrence and a content-addressed image binding produces a
// product overlay item whose AssetRef carries the SHA256 (the manifest gate).
func TestOverlaySceneInput_ProductWithSHA256ProducesContentAddressedItem(t *testing.T) {
	scene := Scene{
		ID:   "scene-0",
		Text: map[Language]string{"en": "Apple unveiled the Vision Pro."},
		Annotations: &scriptpkg.SceneAnnotations{
			Version: 1, Language: "en", Status: "completed",
			PrimaryEntities: []scriptpkg.AnnotatedEntity{{
				ID: "e-vision-pro", CanonicalName: "Vision Pro", Type: "PRODUCT", Confidence: 0.95,
				CanonicalEntityID: "product:apple-vision-pro",
				Image: &scriptpkg.EntityImageBinding{
					Status: "resolved", AssetID: "vision-pro-img",
					PreviewURL: "https://cdn.example.com/vision-pro.png",
					SHA256:     "cc33dd44ee55ff66778899aabbccddeeff00112233445566778899aabbccdd",
				},
			}},
		},
	}
	timing := capabilityaudio.SpeechTimingArtifact{
		Version: capabilityaudio.SpeechTimingVersion, Provider: "edge_tts",
		BoundaryMode: capabilityaudio.BoundaryWord, Language: "en",
		TextSHA256: "text-hash", AudioSHA256: "audio-hash", DurationUS: 300_000,
		Words: []capabilityaudio.SpeechWordTiming{
			{Index: 0, Text: "Apple", StartUS: 0, EndUS: 100_000},
			{Index: 1, Text: "unveiled", StartUS: 100_000, EndUS: 200_000},
			{Index: 2, Text: "the", StartUS: 200_000, EndUS: 250_000},
			{Index: 3, Text: "Vision", StartUS: 250_000, EndUS: 280_000},
			{Index: 4, Text: "Pro", StartUS: 280_000, EndUS: 300_000},
		},
	}
	occ := capabilityentities.EntityOccurrence{
		EntityID: capabilityentities.StableEntityID("PRODUCT", "Vision Pro"),
		Name:     "Vision Pro", Type: "PRODUCT", SceneID: "scene-0",
		TextStart: 20, TextEnd: 29, WordStart: 3, WordEnd: 4,
		LocalStartUS: 250_000, LocalEndUS: 300_000,
		TimelineStartUS: 0, AudioStartUS: 250_000, AudioEndUS: 300_000,
		Confidence: 0.95,
	}
	input, err := overlaySceneInput(scene, timing, 0, []capabilityentities.EntityOccurrence{occ})
	require.NoError(t, err)
	require.NotNil(t, input)
	require.Len(t, input.Products, 1, "the spoken PRODUCT entity must produce a product candidate")
	require.Equal(t, "vision-pro-img", input.Products[0].AssetID)
	require.Equal(t, "cc33dd44ee55ff66778899aabbccddeeff00112233445566778899aabbccdd", input.Products[0].SHA256)
}

// TestNormalizeAnnotationTypeRegistry certifies the single kernel taxonomy
// registry: PRODUCT/LOGO survive (never CONCEPT), the entity-card kinds are
// reported by IsAnnotationEntityKind, and unknown types still collapse to
// CONCEPT.
func TestNormalizeAnnotationTypeRegistry(t *testing.T) {
	cases := map[string]string{
		"PERSON": "PERSON", "person": "PERSON",
		"ORG": "ORG", "ORGANIZATION": "ORG", "COMPANY": "ORG",
		"GPE": "GPE", "LOCATION": "GPE", "CITY": "GPE",
		"PRODUCT": "PRODUCT", "LOGO": "LOGO",
		"DATE": "DATE", "MONEY": "MONEY", "CARDINAL": "CARDINAL",
		"NUMBER": "NUMBER", "QUOTE": "QUOTE",
		"KEYWORD": "KEYWORD", "VISUAL_SUBJECT": "VISUAL_SUBJECT",
		"FLYING_THING": "CONCEPT", "": "CONCEPT",
	}
	for raw, want := range cases {
		if got := scriptpkg.NormalizeAnnotationType(raw); got != want {
			t.Errorf("NormalizeAnnotationType(%q) = %q, want %q", raw, got, want)
		}
	}
	for _, kind := range []string{"PERSON", "ORG", "GPE", "PRODUCT", "LOGO"} {
		if !scriptpkg.IsAnnotationEntityKind(kind) {
			t.Errorf("IsAnnotationEntityKind(%q) = false, want true", kind)
		}
	}
	for _, kind := range []string{"DATE", "MONEY", "CONCEPT", "QUOTE", "KEYWORD", ""} {
		if scriptpkg.IsAnnotationEntityKind(kind) {
			t.Errorf("IsAnnotationEntityKind(%q) = true, want false", kind)
		}
	}
}

// TestResolverCanonicalIDLookup certifies the SSOT lookup: canonical name
// first, raw value second, both lowercased; empty when the resolver was not
// wired.
func TestResolverCanonicalIDLookup(t *testing.T) {
	seg := scriptpkg.VidRushSegmentResult{Insights: scriptpkg.SegmentInsights{
		ImageEntityCanonicalIDs: map[string]string{
			"floyd mayweather": "person:floyd-mayweather",
			"torre eiffel":     "gpe:eiffel-tower",
		},
	}}
	assert.Equal(t, "person:floyd-mayweather", seg.ResolverCanonicalID("Floyd Mayweather", "Mayweather"))
	assert.Equal(t, "gpe:eiffel-tower", seg.ResolverCanonicalID("Eiffel Tower", "Torre Eiffel"))
	assert.Empty(t, seg.ResolverCanonicalID("Unknown Person", "Unknown"))
	empty := scriptpkg.VidRushSegmentResult{}
	assert.Empty(t, empty.ResolverCanonicalID("Floyd Mayweather", "Floyd Mayweather"))
}
