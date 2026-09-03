// Package indexing — payload_mapper_test.go pins ORCHESTRATOR invariants:
//
//   - canonical lifecycle key (QDRANT-004 PR2, June 2026) — payload MUST be
//     written under the verbatim key "lifecycle_state" (this is the origin/main
//     HEAD literal kept verbatim per AGENTS.md rebase-conflict lesson).
//   - per-channel embedding_version_<channel> roundtrip (PR 6 verdict §11,
//     observed provenance; the wire MUST carry the OBSERVED
//     EmbeddingArtifact.ModelVersion, NOT the schema's expected value).
//
// BuildPayloadFromDocument is the canonical producer of the Qdrant payload.
// After the PR-PAYLOAD-MAPPER-SPLIT-mirror (July 2026), this file owns ONLY
// the two ORCHESTRATOR invariants above. Companion test files mirror the
// production split:
//
//   - payload_mapper_validation_test.go     — VALIDATION subtree (10 funcs)
//   - payload_mapper_document_test.go       — DOCUMENT subtree   (13 funcs)
//   - payload_mapper_searchtext_test.go     — SEARCHTEXT subtree (12 funcs)
//   - payload_mapper_testhelpers_test.go    — shared plumbing    (no Test funcs)
//
// godlike/07 minimum-blast-radius: pure code-motion, no logic change.
package indexing

import (
	"testing"

	qdrantSchema "github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/schema"
)

// TestBuildPayloadFromDocument_LifecycleKeyIsCanonical asserts that the
// canonical document payload builder writes asset.LifecycleState under the
// key "lifecycle_state" (PR 1 / QDRANT-004 §(b)) and does NOT silently emit
// the retired "status" key.
func TestBuildPayloadFromDocument_LifecycleKeyIsCanonical(t *testing.T) {
	asset := &AssetData{
		ID:             "asset-1",
		Name:           "asset-1-name",
		LifecycleState: "ACTIVE",
		Source:         "stock",
	}
	schema := qdrantSchema.DefaultV3Schema()

	payload := BuildPayloadFromDocument(assetToIndexDocumentNoValidate(asset, schema), schema)

	got, ok := payload["lifecycle_state"]
	if !ok {
		t.Fatalf("BuildPayloadFromDocument must write canonical payload key %q; got keys %v", "lifecycle_state", mapKeys(payload))
	}
	if got != "ACTIVE" {
		t.Errorf("lifecycle_state => %v, want %q (PR 1 canonical SSOT)", got, "ACTIVE")
	}

	if got, ok := payload["name"]; !ok || got != "asset-1-name" {
		t.Fatalf("BuildPayloadFromDocument must write name from asset.Name; got %v (present=%v)", got, ok)
	}

	if _, leaked := payload["status"]; leaked {
		t.Errorf("BuildPayloadFromDocument MUST NOT emit legacy %q key (QDRANT-004 PR2 drift window); got keys %v", "status", mapKeys(payload))
	}
}

// TestBuildPayloadFromDocument_EmbeddingVersionsByChannel sanity-checks that
// the per-channel embedding_version_<channel> payload keys roundtrip from the
// manifest into the payload. Without this the reindex verifier's per-channel
// counters could regress.
func TestBuildPayloadFromDocument_EmbeddingVersionsByChannel(t *testing.T) {
	asset := &AssetData{ID: "asset-2", LifecycleState: "ACTIVE"}
	schema := qdrantSchema.DefaultV3Schema()

	payload := BuildPayloadFromDocument(assetToIndexDocumentNoValidate(asset, schema), schema)

	for _, spec := range schema.DenseVectors {
		key := "embedding_version_" + spec.Channel
		got, ok := payload[key]
		if !ok {
			t.Errorf("payload missing %q (channel=%q)", key, spec.Channel)
			continue
		}
		if got != spec.ModelVersion {
			t.Errorf("%q => %v, want %q", key, got, spec.ModelVersion)
		}
	}
}
