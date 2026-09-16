// Package media — index_request_payload_test.go: the permanent gate on the
// asset.index.requested payload CONTRACT.
//
// Why this file exists (September 2026 audit): the envelope used to declare
// `requested_vectors: ["text","transcript"]` while the PostgreSQL media plane
// writes exactly ONE vector channel (media_embeddings.embedding_type='text',
// produced by PostgresIndexWorker.EmbeddingType and queried by MediaSearcher).
// The "transcript" channel belonged to the RETIRED SQLite → Qdrant media
// projection. A consumer honouring the declaration was promised work no
// producer performed — the phantom-availability shape godlike/07 forbids
// (observed live on yt_gT0amKtXWdU_0_10_v1, whose outbox payload asked for two
// vectors and whose asset had exactly one).
//
// The payload used to be built inline inside CommitIndexRequestTx, so the
// contract was only reachable through a live transaction — a fact no gate
// protects. assetIndexRequestedPayload is now a pure builder and this test is
// the gate.
//
// No database is required: this is a white-box contract pin.
package media

import (
	"reflect"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/event"
)

// TestAssetIndexRequestedPayload_DeclaresOnlyProducedChannels pins the
// declared vector channels to the canonical single source.
//
// Non-vacuity: restoring the literal ["text","transcript"] fails. Dropping the
// field entirely fails. Changing the emitter to a hand-written list that drifts
// from the helper fails.
func TestAssetIndexRequestedPayload_DeclaresOnlyProducedChannels(t *testing.T) {
	payload := assetIndexRequestedPayload(IndexRequest{
		AssetID:       "yt_gT0amKtXWdU_0_10_v1",
		Source:        "youtube",
		MediaType:     "video",
		SourceVersion: "4754155f7c4f42ae8ec6c115aed04bdbd1c892c9282893fc1db09f671a63c87a",
	}, "evt-1", "evt-key-1")

	raw, ok := payload["requested_vectors"]
	if !ok {
		t.Fatal("payload has no requested_vectors field: the outbox consumer cannot know which channels to expect")
	}
	got, ok := raw.([]string)
	if !ok {
		t.Fatalf("requested_vectors has type %T, want []string", raw)
	}

	// The declaration MUST come from the canonical source, not a local literal.
	want := event.AssetIndexRequestedVectorChannels()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("requested_vectors = %v, want the canonical event.AssetIndexRequestedVectorChannels() = %v", got, want)
	}

	// The retired channel must never come back through this emitter: no
	// PostgreSQL media code writes or searches it.
	for _, channel := range got {
		if channel == "transcript" {
			t.Error("requested_vectors declares the RETIRED \"transcript\" channel: the PostgreSQL media plane writes only embedding_type='text', so this promises vectors no producer creates")
		}
	}
	if len(got) != 1 || got[0] != "text" {
		t.Errorf("requested_vectors = %v, want exactly [\"text\"] (the one channel postgres/media writes and searches)", got)
	}
}

// TestAssetIndexRequestedPayload_KeepsEnvelopeContractShape pins the rest of
// the envelope contract that a consumer keys on, so a future refactor of the
// builder cannot silently drop a field the worker depends on.
func TestAssetIndexRequestedPayload_KeepsEnvelopeContractShape(t *testing.T) {
	payload := assetIndexRequestedPayload(IndexRequest{
		AssetID:       "yt_abc_0_30_v1",
		Source:        "youtube",
		MediaType:     "video",
		SourceVersion: "sha256:content",
	}, "evt-2", "evt-key-2")

	for _, field := range []string{
		"schema_version", "event_id", "asset_id", "operation",
		"source_version", "index_revision", "target_index_version",
		"requested_vectors", "requested_at", "idempotency_key",
		"source", "media_type", "embedding_model", "embedding_version",
	} {
		if _, ok := payload[field]; !ok {
			t.Errorf("payload is missing %q: the SQLite emitter sets it and the two envelopes MUST stay byte-identical", field)
		}
	}

	// index_revision is the supersede fingerprint the worker compares; it
	// carries the same value as source_version on this plane (content_sha256
	// vs index_revision are distinct CONCEPTS, identical VALUE on ingest).
	if payload["index_revision"] != payload["source_version"] {
		t.Errorf("index_revision = %v, want it to equal source_version %v on the ingest path",
			payload["index_revision"], payload["source_version"])
	}
	if payload["idempotency_key"] != "evt-key-2" {
		t.Errorf("idempotency_key = %v, want the supplied event key", payload["idempotency_key"])
	}
	if payload["asset_id"] != "yt_abc_0_30_v1" {
		t.Errorf("asset_id = %v, want the request's asset id", payload["asset_id"])
	}
}
