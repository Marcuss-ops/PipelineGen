package media

// prefetch_batch_test.go pins the N→1 batch fast lane of the PostgreSQL media
// outbox worker — the "one search_text read for a whole multilingual claim
// batch" contract the clip.render batch hot path relies on.
//
// It is a white-box (package media) test on purpose: prefetchBatchVectors is
// the seam, and it must be provable without a database. The worker struct is
// built directly so no connection is opened; the method under test only reads
// w.embedder.
//
// The contract, exactly:
//   - the batch leg runs only when the wired embedder exposes the OPTIONAL
//     BatchAssetEmbedder surface;
//   - it runs only for a batch with >= 2 DISTINCT assets (one round-trip is
//     only worth it when there is something to amortize);
//   - duplicate assets in the batch collapse to one id;
//   - a batch error returns nil so every event falls back to the per-asset
//     path — batching can never change delivery, retry or dead-letter
//     semantics;
//   - only asset.index.requested claims participate; anything else (a
//     different event type, a nil claim, an unparseable envelope) is skipped,
//     and a payload that parses but carries no asset_id falls back to the
//     aggregate id exactly as handleIndexEvent resolves it.

import (
	"context"
	"errors"
	"testing"
)

// batchPrefetchEmbedder implements BOTH AssetEmbedder and the optional
// BatchAssetEmbedder surface, recording every batch invocation.
type batchPrefetchEmbedder struct {
	vecs   map[string][]float32
	fails  map[string]error
	calls  int
	lastID []string
}

func (s *batchPrefetchEmbedder) EmbedAssetText(context.Context, string) ([]float32, error) {
	return []float32{0}, nil
}

func (s *batchPrefetchEmbedder) EmbedAssetTexts(_ context.Context, assetIDs []string) (map[string][]float32, error) {
	s.calls++
	s.lastID = append([]string(nil), assetIDs...)
	for _, id := range assetIDs {
		if err := s.fails[id]; err != nil {
			return nil, err
		}
	}
	out := make(map[string][]float32, len(assetIDs))
	for _, id := range assetIDs {
		out[id] = s.vecs[id]
	}
	return out, nil
}

// singleSurfaceEmbedder implements ONLY AssetEmbedder, so the worker must
// never take the batch lane for it.
type singleSurfaceEmbedder struct{}

func (singleSurfaceEmbedder) EmbedAssetText(context.Context, string) ([]float32, error) {
	return []float32{0}, nil
}

func indexClaim(assetID string) *OutboxClaim {
	return &OutboxClaim{Event: OutboxEvent{
		EventType:   EventAssetIndexRequested,
		AggregateID: assetID,
		PayloadJSON: `{"asset_id":"` + assetID + `"}`,
	}}
}

func TestPrefetchBatchVectors_OneReadForDistinctAssets(t *testing.T) {
	emb := &batchPrefetchEmbedder{vecs: map[string][]float32{"a": {1}, "b": {2}, "c": {3}}}
	w := &PostgresIndexWorker{embedder: emb}

	got := w.prefetchBatchVectors(context.Background(), []*OutboxClaim{
		indexClaim("a"), indexClaim("b"), indexClaim("b"), indexClaim("c"),
	})

	if emb.calls != 1 {
		t.Fatalf("batch leg called %d times, want 1 (N assets, ONE round-trip)", emb.calls)
	}
	if len(emb.lastID) != 3 {
		t.Fatalf("batch received %v, want the 3 distinct ids (duplicates collapse)", emb.lastID)
	}
	if len(got) != 3 {
		t.Fatalf("resolved %d vectors, want 3: %v", len(got), got)
	}
}

func TestPrefetchBatchVectors_SingleAssetSkipsTheBatch(t *testing.T) {
	emb := &batchPrefetchEmbedder{vecs: map[string][]float32{"a": {1}}}
	w := &PostgresIndexWorker{embedder: emb}

	if got := w.prefetchBatchVectors(context.Background(), []*OutboxClaim{indexClaim("a"), indexClaim("a")}); got != nil {
		t.Fatalf("one distinct asset must not pay for a batch round-trip, got %v", got)
	}
	if emb.calls != 0 {
		t.Fatalf("batch leg called %d times for a single distinct asset, want 0", emb.calls)
	}
}

func TestPrefetchBatchVectors_NonIndexEventsAreSkipped(t *testing.T) {
	emb := &batchPrefetchEmbedder{vecs: map[string][]float32{"a": {1}, "b": {2}}}
	w := &PostgresIndexWorker{embedder: emb}

	other := &OutboxClaim{Event: OutboxEvent{EventType: "clip.render.drive_delivery.requested.v1", AggregateID: "x"}}
	if got := w.prefetchBatchVectors(context.Background(), []*OutboxClaim{other, indexClaim("a"), nil, indexClaim("b")}); len(got) != 2 {
		t.Fatalf("resolved %d vectors, want the 2 index assets only: %v", len(got), got)
	}
	if emb.calls != 1 || len(emb.lastID) != 2 {
		t.Fatalf("batch leg calls=%d ids=%v, want 1 call over the 2 index assets", emb.calls, emb.lastID)
	}
}

func TestPrefetchBatchVectors_UnparseableEnvelopeIsSkipped(t *testing.T) {
	emb := &batchPrefetchEmbedder{vecs: map[string][]float32{"b": {2}}}
	w := &PostgresIndexWorker{embedder: emb}

	broken := &OutboxClaim{Event: OutboxEvent{
		EventType:   EventAssetIndexRequested,
		AggregateID: "agg-a",
		PayloadJSON: `{not json`,
	}}
	// The unparseable claim resolves to no id, so only "b" is left — a single
	// distinct asset, which must NOT pay for a batch round-trip. The broken
	// event still falls back to the per-asset path in processClaims.
	if got := w.prefetchBatchVectors(context.Background(), []*OutboxClaim{broken, indexClaim("b")}); got != nil {
		t.Fatalf("unparseable envelope must be skipped, got %v", got)
	}
	if emb.calls != 0 {
		t.Fatalf("batch leg calls = %d, want 0", emb.calls)
	}
}

func TestPrefetchBatchVectors_EmptyAssetIDFallsBackToAggregateID(t *testing.T) {
	emb := &batchPrefetchEmbedder{vecs: map[string][]float32{"agg-a": {1}, "b": {2}}}
	w := &PostgresIndexWorker{embedder: emb}

	// A valid envelope that names no asset_id resolves through the aggregate
	// id (the same rule handleIndexEvent applies).
	noAssetID := &OutboxClaim{Event: OutboxEvent{
		EventType:   EventAssetIndexRequested,
		AggregateID: "agg-a",
		PayloadJSON: `{"source":"youtube"}`,
	}}
	got := w.prefetchBatchVectors(context.Background(), []*OutboxClaim{noAssetID, indexClaim("b")})
	if len(got) != 2 {
		t.Fatalf("resolved %d vectors, want 2 (aggregate-id fallback): %v", len(got), got)
	}
}

func TestPrefetchBatchVectors_BatchErrorFallsBackToPerAsset(t *testing.T) {
	emb := &batchPrefetchEmbedder{
		vecs:  map[string][]float32{"a": {1}, "b": {2}},
		fails: map[string]error{"b": errors.New("embedder sidecar unavailable")},
	}
	w := &PostgresIndexWorker{embedder: emb}

	if got := w.prefetchBatchVectors(context.Background(), []*OutboxClaim{indexClaim("a"), indexClaim("b")}); got != nil {
		t.Fatalf("a failed batch leg must return nil so every event retries per-asset, got %v", got)
	}
	if emb.calls != 1 {
		t.Fatalf("batch leg calls = %d, want 1 (attempted, then abandoned)", emb.calls)
	}
}

func TestPrefetchBatchVectors_EmbedderWithoutBatchSurfaceIsOptedOut(t *testing.T) {
	w := &PostgresIndexWorker{embedder: singleSurfaceEmbedder{}}
	if got := w.prefetchBatchVectors(context.Background(), []*OutboxClaim{indexClaim("a"), indexClaim("b")}); got != nil {
		t.Fatalf("an embedder without the batch surface must disable the batch leg, got %v", got)
	}
}
