package observability

import (
	"context"
	"testing"
	"time"
)

func TestOperationMetaContextRoundTrip(t *testing.T) {
	want := OperationMeta{
		WorkerID: "seg-worker-2",
		QueuedAt: time.Now(),
		Metadata: map[string]string{"segment_id": "intro", "segment_index": "2"},
	}
	ctx := WithOperationMeta(context.Background(), want)
	got, ok := OperationMetaFromContext(ctx)
	if !ok {
		t.Fatal("OperationMetaFromContext: expected bound meta")
	}
	if got.WorkerID != want.WorkerID || !got.QueuedAt.Equal(want.QueuedAt) {
		t.Fatalf("round-trip mismatch: got %+v want %+v", got, want)
	}
	if got.Metadata["segment_id"] != "intro" || got.Metadata["segment_index"] != "2" {
		t.Fatalf("metadata round-trip mismatch: %v", got.Metadata)
	}
}

func TestOperationMetaFromContextAbsent(t *testing.T) {
	if _, ok := OperationMetaFromContext(context.Background()); ok {
		t.Fatal("expected no bound meta on a plain context")
	}
	if _, ok := OperationMetaFromContext(nil); ok {
		t.Fatal("expected no bound meta on a nil context")
	}
}

func TestOperationMetaApplyDoesNotOverwriteExplicitFields(t *testing.T) {
	explicitQueued := time.Now().Add(-time.Second)
	info := OperationInfo{
		WorkerID:     "owner-worker",
		QueuedAt:     explicitQueued,
		MetadataJSON: `{"owner":true}`,
	}
	OperationMeta{
		WorkerID: "fanout-worker",
		QueuedAt: time.Now(),
		Metadata: map[string]string{"segment_id": "x"},
	}.Apply(&info)

	if info.WorkerID != "owner-worker" {
		t.Fatalf("WorkerID overwritten: %q", info.WorkerID)
	}
	if !info.QueuedAt.Equal(explicitQueued) {
		t.Fatalf("QueuedAt overwritten: %v", info.QueuedAt)
	}
	if info.MetadataJSON != `{"owner":true}` {
		t.Fatalf("MetadataJSON overwritten: %q", info.MetadataJSON)
	}
}

func TestOperationMetaApplyFillsEmptyFields(t *testing.T) {
	queued := time.Now()
	info := OperationInfo{}
	OperationMeta{
		WorkerID: "seg-worker-0",
		QueuedAt: queued,
		Metadata: map[string]string{"segment_id": "segment-0"},
	}.Apply(&info)

	if info.WorkerID != "seg-worker-0" {
		t.Fatalf("WorkerID = %q, want seg-worker-0", info.WorkerID)
	}
	if !info.QueuedAt.Equal(queued) {
		t.Fatalf("QueuedAt = %v, want %v", info.QueuedAt, queued)
	}
	if info.MetadataJSON != `{"segment_id":"segment-0"}` {
		t.Fatalf("MetadataJSON = %q, want segment_id json", info.MetadataJSON)
	}
}

func TestOperationMetaMetadataJSONDeterministic(t *testing.T) {
	m := OperationMeta{Metadata: map[string]string{"b": "2", "a": "1"}}
	first := m.MetadataJSON()
	second := m.MetadataJSON()
	if first != second {
		t.Fatalf("MetadataJSON not deterministic: %q vs %q", first, second)
	}
	// Go marshals maps with sorted keys, so the canonical shape is stable.
	if first != `{"a":"1","b":"2"}` {
		t.Fatalf("MetadataJSON = %q, want sorted keys", first)
	}
	if empty := (OperationMeta{}).MetadataJSON(); empty != "" {
		t.Fatalf("empty MetadataJSON = %q, want empty", empty)
	}
}
