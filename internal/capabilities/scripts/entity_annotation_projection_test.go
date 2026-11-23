// Package scriptgeneration — entity_annotation_projection_test.go certifies
// entity annotation projection and the PRODUCT/LOGO taxonomy path.
package scriptgeneration

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// TestProjectEntityAnnotations_StampsResolverCanonicalID certifies the runner
// annotation projection carries the canonical id supplied in segment insights
// to the overlay media index.
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

// TestProjectEntityAnnotations_UsesRustRoleBeforeLegacyKind certifies the v3
// authority boundary: the linked role overrides the legacy kind split in both
// directions (a GPE visual concept is secondary; an EVENT named entity is
// primary).
func TestProjectEntityAnnotations_UsesRustRoleBeforeLegacyKind(t *testing.T) {
	text := "Miami hosted the championship."
	seg := scriptpkg.VidRushSegmentResult{
		SegmentID: "scene-1",
		Insights: scriptpkg.SegmentInsights{
			Entities: []scriptpkg.ExtractedEntity{
				{Value: "Miami", Type: "GPE", Confidence: 0.9, Role: "visual_concept"},
				{Value: "championship", Type: "EVENT", Confidence: 0.9, Role: "named_entity"},
			},
		},
	}
	ann := projectEntityAnnotations(text, "en", seg)
	require.NotNil(t, ann)
	require.Len(t, ann.SecondaryEntities, 1)
	assert.Equal(t, "Miami", ann.SecondaryEntities[0].CanonicalName)
	assert.Equal(t, "visual_concept", ann.SecondaryEntities[0].Role)
	require.Len(t, ann.PrimaryEntities, 1)
	assert.Equal(t, "championship", ann.PrimaryEntities[0].CanonicalName)
	assert.Equal(t, "named_entity", ann.PrimaryEntities[0].Role)
}

// TestProjectEntityAnnotations_ProductIsPrimaryAndStamped certifies the
// PRODUCT taxonomy on the runner path: a PRODUCT entity survives the
// normalizer as PRODUCT (never CONCEPT), lands in PrimaryEntities (the
// primary/media imageable set) and carries its supplied canonical id.
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
		Assets: scriptpkg.SegmentAssetSelection{Candidates: []scriptpkg.SegmentAssetCandidate{{
			AssetID: "apple-logo", Provider: scriptpkg.VidRushProviderInternetImages, Entity: "Apple",
			DriveLink: "https://drive.google.com/file/d/apple-logo/view", ContentSHA256: "apple-md5",
			AcquisitionStatus: scriptpkg.VidRushStatusAcquired, VerificationStatus: scriptpkg.VidRushStatusVerified,
			PersistenceStatus: scriptpkg.VidRushStatusPersisted,
			SemanticStatus:    "accepted",
			RightsStatus:      "unknown_allowed",
		}}},
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
func TestEntityImageURLPrefersDurableDriveDownload(t *testing.T) {
	binding := &scriptpkg.EntityImageBinding{
		PreviewURL: "https://cdn.example/volatile.jpg",
		DriveLink:  "https://drive.google.com/file/d/drive-manny-pacquiao/view?usp=drivesdk",
	}
	want := "https://drive.google.com/uc?export=download&id=drive-manny-pacquiao"
	if got := entityImageURL(binding); got != want {
		t.Fatalf("entityImageURL() = %q, want durable Drive URL %q", got, want)
	}
	binding.DriveLink = ""
	binding.DriveFileID = "drive-from-id"
	want = "https://drive.google.com/uc?export=download&id=drive-from-id"
	if got := entityImageURL(binding); got != want {
		t.Fatalf("entityImageURL() with DriveFileID = %q, want %q", got, want)
	}
}

// TestOverlaySceneInput_ProductWithSHA256ProducesContentAddressedItem
// certifies the end-to-end planner projection: a PRODUCT entity with a
// certified occurrence and a content-addressed image binding produces a
// product overlay item whose AssetRef carries the SHA256 (the manifest gate).
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
