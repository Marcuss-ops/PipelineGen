// Package event_test — identities_test.go: the permanent gate on the canonical
// vector-channel declaration carried by every asset.index.requested envelope.
//
// Why this gate exists (September 2026 audit): the declaration used to be a
// per-emitter literal list, and the two emitters disagreed with the plane they
// describe — the envelope asked for ["text","transcript"] while the PostgreSQL
// media plane writes exactly one channel (media_embeddings.embedding_type
// ='text'). "transcript" belonged to the RETIRED SQLite → Qdrant media
// projection. Declaring it re-introduces phantom availability: a consumer
// honours a request for work no producer performs.
//
// The list now has ONE owner (event.AssetIndexRequestedVectorChannels) consumed
// by every emitter, and this test is the gate that keeps it honest. It is
// deliberately about the VALUE, not the callers: a caller cannot drift while
// the value is right, and the value cannot drift without failing here.
package event_test

import (
	"reflect"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/event"
)

// TestAssetIndexRequestedVectorChannels_DeclaresOnlyProducedChannels pins the
// exact declaration and, more importantly, the RATIONALE as an assertion.
//
// The returned channels are what postgres/media actually produces
// (PostgresIndexWorker writes embedding_type='text' from the asset's live
// search_text) and what postgres/media.MediaSearcher queries. A channel listed
// here but not produced anywhere is a lie the outbox consumer cannot detect.
func TestAssetIndexRequestedVectorChannels_DeclaresOnlyProducedChannels(t *testing.T) {
	got := event.AssetIndexRequestedVectorChannels()

	want := []string{"text"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("AssetIndexRequestedVectorChannels() = %v, want %v (the PostgreSQL media plane writes exactly the 'text' channel)", got, want)
	}

	// Regression pin: the retired channel must stay out. Adding it back
	// without adding a producer on the PostgreSQL plane re-creates the
	// phantom request this gate was written to prevent.
	if len(got) != 1 || got[0] != "text" {
		t.Errorf("declaration = %v; exactly [\"text\"] is the honest set — any other value must be justified by a real producer", got)
	}
}

// TestAssetIndexRequestedVectorChannels_ReturnsFreshCopy pins the defensive
// copy: a consumer that sorts or trims the returned slice must not be able to
// corrupt the declaration for every later caller.
func TestAssetIndexRequestedVectorChannels_ReturnsFreshCopy(t *testing.T) {
	first := event.AssetIndexRequestedVectorChannels()
	if len(first) == 0 {
		t.Fatal("declaration is empty")
	}
	first[0] = "corrupted-by-caller"

	second := event.AssetIndexRequestedVectorChannels()
	if second[0] != "text" {
		t.Errorf("declaration leaked caller mutation: got %q, want \"text\" (each caller must receive its own copy)", second[0])
	}
}
