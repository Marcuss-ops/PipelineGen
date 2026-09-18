package scriptgeneration

import (
	"strings"
	"testing"

	capabilityentities "github.com/Marcuss-ops/PipelineGen/internal/capabilities/entities"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// The bundle builder had NO test at all before this file, which is how a guard
// swap in it could have gone unnoticed. These tests exist for the two claims the
// builder makes about the media identity it publishes onto the cross-stage
// contract:
//
//  1. the identity it writes into a BoundAsset comes from the canonical kernel
//     type (kernel/asset.Ref), so the content address on the bundle is the
//     canonical spelling of the digest and not whatever case the producer
//     happened to stamp;
//  2. a binding that is not content-addressed is NOT promoted into the bundle —
//     a half-bound asset must never cross the semantic contract, and it is
//     refused by the guard, not by a validator failing later.

const bundleTestDigest = "c4813c9d7d4f0f6b1a2c3d4e5f60718293a4b5c6d7e8f9012345678901abcdef"

// bundleTestEntityID is the content-addressed identity the canonical entity
// timeline stamps for the fixture's PERSON annotation. It is DERIVED through the
// single identity owner rather than hard-coded: the asset↔entity join is the
// stable identity both surfaces derive from (type, canonical name), never a
// comparison of the two display strings — a fixture with an invented label would
// only pass against the string join this test suite exists to rule out.
var bundleTestEntityID = capabilityentities.StableEntityID("person", "Michael Jordan")

// bundleBuilderResult builds the smallest result the builder accepts: one scene
// whose text grounds one entity occurrence, plus that entity's image binding.
// The binding is passed in so a test can vary only the media identity.
func bundleBuilderResult(image *scriptpkg.EntityImageBinding) *GenerateResult {
	return &GenerateResult{
		Scenes: []Scene{{
			ID:   "scene-1",
			Text: map[Language]string{"en": "Michael Jordan ha vinto."},
			Annotations: &scriptpkg.SceneAnnotations{
				Language: "en",
				PrimaryEntities: []scriptpkg.AnnotatedEntity{{
					Text:          "Michael Jordan",
					CanonicalName: "Michael Jordan",
					Type:          "person",
					Image:         image,
				}},
			},
		}},
		EntityTimeline: &capabilityentities.EntityTimeline{
			Version: 1,
			Scenes: []capabilityentities.SceneEntityTimeline{{
				SceneID: "scene-1",
				Entities: []capabilityentities.EntityOccurrence{{
					EntityID: bundleTestEntityID, Name: "Michael Jordan", Type: "person",
					SceneID: "scene-1", TextStart: 0, TextEnd: len("Michael Jordan"),
					AudioStartUS: 1_000_000, AudioEndUS: 2_000_000,
				}},
			}},
		},
	}
}

// TestBuildSemanticRenderBundleProjectsCanonicalAssetIdentity pins the identity
// the cross-stage contract receives. The producer stamps a MIXED-CASE digest on
// purpose: the bundle must carry the canonical lower-case content address, or
// the same bytes would join the render contract under two different keys
// depending on which producer happened to write the binding.
func TestBuildSemanticRenderBundleProjectsCanonicalAssetIdentity(t *testing.T) {
	result := bundleBuilderResult(&scriptpkg.EntityImageBinding{
		Status:      "bound",
		AssetID:     "asset-michael-jordan",
		SHA256:      strings.ToUpper(bundleTestDigest),
		MediaType:   "image/jpeg",
		PreviewURL:  "https://cdn.example/jordan.jpg",
		DriveFileID: "drive-file-id",
		DriveLink:   "https://drive.google.com/file/d/drive-file-id/view",
		LocalPath:   "/tmp/producer-only/jordan.jpg",
	})

	bundle, err := BuildSemanticRenderBundleFromResult(result, "en", "run-1", "video-1")
	if err != nil {
		t.Fatalf("build bundle: %v", err)
	}
	if bundle == nil {
		t.Fatal("builder returned no bundle and no error")
	}
	if len(bundle.Assets) != 1 {
		t.Fatalf("bundle assets = %d, want 1: %+v", len(bundle.Assets), bundle.Assets)
	}

	asset := bundle.Assets[0]
	if asset.AssetID != "asset-michael-jordan" {
		t.Errorf("bundle asset id = %q, want the binding's logical asset id", asset.AssetID)
	}
	if asset.ContentHash != bundleTestDigest {
		t.Errorf("bundle content address = %q, want the canonical spelling %q", asset.ContentHash, bundleTestDigest)
	}
	if !asset.Verified {
		t.Errorf("a binding with a fetchable location must be promoted as verified: %+v", asset)
	}
	if asset.EntityID != bundleTestEntityID {
		t.Errorf("bundle asset joins entity %q, want the timeline occurrence id %q", asset.EntityID, bundleTestEntityID)
	}

	// The bundle is the cross-stage contract, so it must have been accepted by
	// its own validator and must not have smuggled a producer path into it.
	if err := bundle.Validate(); err != nil {
		t.Errorf("the emitted bundle must satisfy its own contract: %v", err)
	}
	if strings.Contains(asset.SourceURL, "/tmp/") {
		t.Errorf("producer-local path leaked into the bundle asset source: %q", asset.SourceURL)
	}
}

// TestBuildSemanticRenderBundleRefusesNonContentAddressedBinding pins the guard
// the builder applies before promoting an annotation binding. A binding whose
// "digest" is a scheme-prefixed legacy value, or is missing, is not a content
// address: it must produce NO asset rather than a half-bound one, and the bundle
// itself must remain valid (the entity card simply stays text-only).
func TestBuildSemanticRenderBundleRefusesNonContentAddressedBinding(t *testing.T) {
	for name, digest := range map[string]string{
		"missing":          "",
		"scheme-prefixed":  "sha256:7f83b1657ff1fc53b92dc18148a1d65dfa135e2f",
		"short":            "abc123",
		"non-hex":          strings.Repeat("z", 64),
		"missing-asset-id": bundleTestDigest,
	} {
		t.Run(name, func(t *testing.T) {
			binding := &scriptpkg.EntityImageBinding{
				Status:     "bound",
				AssetID:    "asset-michael-jordan",
				SHA256:     digest,
				MediaType:  "image/jpeg",
				PreviewURL: "https://cdn.example/jordan.jpg",
			}
			if name == "missing-asset-id" {
				binding.AssetID = ""
			}

			bundle, err := BuildSemanticRenderBundleFromResult(bundleBuilderResult(binding), "en", "run-1", "video-1")
			if err != nil {
				t.Fatalf("build bundle: %v", err)
			}
			if bundle == nil {
				t.Fatal("builder returned no bundle and no error")
			}
			if len(bundle.Assets) != 0 {
				t.Errorf("a non-content-addressed binding was promoted into the bundle: %+v", bundle.Assets)
			}
			if err := bundle.Validate(); err != nil {
				t.Errorf("an asset-less bundle must still satisfy its contract: %v", err)
			}
		})
	}
}
