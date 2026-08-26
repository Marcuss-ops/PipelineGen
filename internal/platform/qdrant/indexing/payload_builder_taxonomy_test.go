// Package indexing — payload_builder_taxonomy_test.go pins the item-8
// invariant: 1 canonical asset = 1 Qdrant point whose payload carries the
// canonical taxonomy dimensions (media_type / asset_kind / source_type /
// namespace / semantic_role) alongside the existing asset_id +
// lifecycle_state identity keys.
//
// The point ID is the canonical SHA-256 v8 derivation
// (schema.AssetIDToQdrantPointID) of the bare asset ID — never the raw asset
// ID, a provider ID, or a per-source prefix. Replaying the same asset must
// upsert the same point, so there is exactly ONE point per canonical asset.
package indexing

import (
	asset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"context"
	"testing"

	qdrantSchema "github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/schema"
)

// TestBuildPayloadFromDocument_TaxonomyDimensionsEmittedWhenSet pins the
// payload-builder leg: populated taxonomy dimensions emit their canonical
// keys verbatim.
func TestBuildPayloadFromDocument_TaxonomyDimensionsEmittedWhenSet(t *testing.T) {
	doc := emptyDoc()
	doc.Metadata.MediaType = "video"
	doc.Metadata.Namespace = "stock"
	doc.Metadata.AssetKind = "stock_video"
	doc.Metadata.SourceType = "artlist"
	doc.Metadata.SemanticRole = "primary"

	p := BuildPayloadFromDocument(doc, nil)
	want := map[string]string{
		"media_type":    "video",
		"namespace":     "stock",
		"asset_kind":    "stock_video",
		"source_type":   "artlist",
		"semantic_role": "primary",
	}
	for k, wantV := range want {
		v, ok := p[k]
		if !ok {
			t.Fatalf("payload[%s] absent — canonical taxonomy dimension not emitted; keys = %v", k, mapKeys(p))
		}
		if v != wantV {
			t.Fatalf("payload[%s] = %v, want %q", k, v, wantV)
		}
	}
}

// TestBuildPayloadFromDocument_TaxonomyDimensionsAbsentWhenEmpty pins the
// omitempty contract: legacy rows without the migration-195 columns must NOT
// leak empty taxonomy keys into the Qdrant payload (godlike/07
// NO-FAKE-AVAILABILITY).
func TestBuildPayloadFromDocument_TaxonomyDimensionsAbsentWhenEmpty(t *testing.T) {
	doc := emptyDoc()
	p := BuildPayloadFromDocument(doc, nil)
	for _, k := range []string{"namespace", "asset_kind", "source_type", "semantic_role"} {
		if _, ok := p[k]; ok {
			t.Fatalf("payload[%s] present when empty — want ABSENT (omitempty contract)", k)
		}
	}
}

// TestAssetToPoint_TaxonomyPayloadAndCanonicalPointID pins the full chain:
// AssetData → airlock → BuildPayload → schema.Point. It asserts both halves
// of the item-8 invariant:
//
//  1. one point whose ID is the canonical SHA-256 v8 derivation of the bare
//     asset ID (not the raw ID / provider ID);
//  2. that point's payload carries the canonical taxonomy dimensions.
func TestAssetToPoint_TaxonomyPayloadAndCanonicalPointID(t *testing.T) {
	asset := &AssetData{
		ID:             "yt_vid_0_60_v1",
		Source:         "youtube",
		MediaType:      "video",
		Namespace:      "stock",
		AssetKind:      "stock_video",
		SourceType:     "youtube",
		SemanticRole:   "primary",
		LifecycleState: "ACTIVE",
		TextVector:     makeFloat32Slice(768),
	}

	mapper := NewPayloadMapper(&fakeAssetStore{asset: asset, ids: []string{asset.ID}}, nil)
	point, err := mapper.AssetToPoint(context.Background(), asset, qdrantSchema.DefaultV3Schema())
	if err != nil {
		t.Fatalf("AssetToPoint error: %v", err)
	}
	requirePointID(t, point)

	// 1 asset = 1 point: the point ID must be the canonical derivation of the
	// bare asset ID — never the raw asset ID string itself.
	if want := qdrantSchema.AssetIDToQdrantPointID(asset.ID); point.ID != want {
		t.Fatalf("point.ID = %q, want canonical SHA-256 v8 = %q", point.ID, want)
	}
	if point.ID == asset.ID {
		t.Fatalf("point.ID must not be the raw asset ID %q (canonical derivation required)", asset.ID)
	}

	// The payload must carry the canonical taxonomy dimensions.
	want := map[string]string{
		"media_type":    "video",
		"namespace":     "stock",
		"asset_kind":    "stock_video",
		"source_type":   "youtube",
		"semantic_role": "primary",
	}
	for k, wantV := range want {
		v, ok := point.Payload[k]
		if !ok {
			t.Fatalf("payload[%s] absent — canonical taxonomy dimension not emitted; keys = %v", k, mapKeys(point.Payload))
		}
		if v != wantV {
			t.Fatalf("payload[%s] = %v, want %q", k, v, wantV)
		}
	}
}
