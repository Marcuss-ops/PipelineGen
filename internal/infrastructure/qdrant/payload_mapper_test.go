// Package qdrant — payload_mapper_test.go pins the canonical
// lifecycle key chosen by QDRANT-004 PR2 (June 2026):
//
//   "lifecycle_state"
//
// Both the writer (BuildPayload) and all readers (search_adapter.go,
// clip_search_adapter.go, mediasearch hydration, clip_update.go
// reads) MUST agree on this exact key. The original drift was
// between the writer ("status") and the readers ("lifecycle_state");
// a unified key is the only safe state. Any future change to a
// different key MUST update this test in lockstep — and the CI gate
// (rg 'payload\[..status..\]' over qdrant infra) is a separate
// defence against accidental reintroduction.
package qdrant

import (
	"testing"
)

// TestBuildPayload_LifecycleKeyIsCanonical asserts that BuildPayload
// writes asset.Status under the canonical key "lifecycle_state" and
// does NOT silently emit a legacy "status" key (which a Qdrant
// payload index in DefaultV3Schema would not be able to filter on).
func TestBuildPayload_LifecycleKeyIsCanonical(t *testing.T) {
	asset := &AssetData{
		ID:     "asset-1",
		Status: "ready",
		Source: "stock",
	}
	schema := DefaultV3Schema()

	payload := BuildPayload(asset, schema)

	got, ok := payload["lifecycle_state"]
	if !ok {
		t.Fatalf("BuildPayload must write canonical payload key %q; got keys %v", "lifecycle_state", mapKeys(payload))
	}
	if got != "ready" {
		t.Errorf("lifecycle_state => %v, want %q", got, "ready")
	}

	if _, leaked := payload["status"]; leaked {
		t.Errorf("BuildPayload MUST NOT emit legacy %q key (QDRANT-004 PR2 drift window); got keys %v", "status", mapKeys(payload))
	}
}

// TestBuildPayload_EmbeddingVersionsByChannel sanity-checks that
// the per-channel embedding_version_<channel> payload keys roundtrip
// from the manifest into the payload (different invariant but co-
// located for the SSOT suite). Without this the reindex verifier's
// per-channel counters could regress.
func TestBuildPayload_EmbeddingVersionsByChannel(t *testing.T) {
	asset := &AssetData{ID: "asset-2", Status: "ready"}
	schema := DefaultV3Schema()

	payload := BuildPayload(asset, schema)

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

// mapKeys is a tiny helper to keep assertion messages readable.
func mapKeys(m map[string]interface{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
