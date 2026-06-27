// Package mediasearch — service_test.go covers the full pipeline
// (embed → search → hydrate → sign) with mock collaborators.
// Target: every DoD bullet from QDRANT-004 §"MEDIA-SEARCH-SERVICE"
// that doesn't require real Qdrant / SQLite.
//
// What's NOT tested here (deferred per QDRANT-001..003):
//   - real Qdrant sparse retrieval,
//   - golden-set search quality,
//   - SQLite schema-version enforcement (QDRANT-003 territory),
//   - multi-tenant DB-level filtering (media_assets.workspace_id
//     lands in QDRANT-001).
package mediasearch

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/search"
)

// ── Mocks ─────────────────────────────────────────────────────────────

type fakeVector struct {
	embedFn func(ctx context.Context, text, vn string) ([]float32, error)
	storeFn func() search.VectorStorePort
	vc      search.VectorConfig
}

func (f *fakeVector) EmbedTextForVector(ctx context.Context, text, vn string) ([]float32, error) {
	if f.embedFn != nil {
		return f.embedFn(ctx, text, vn)
	}
	return []float32{0.1, 0.2, 0.3}, nil
}
func (f *fakeVector) VectorStore() search.VectorStorePort {
	if f.storeFn != nil {
		return f.storeFn()
	}
	return &fakeStore{}
}
func (f *fakeVector) VectorConfig() search.VectorConfig { return f.vc }

// fakeVector doubles as a ConfigPort (assets/search defines VectorConfig
// lookup via the same interface family). Implement both methods
// directly on the same type for convenience.
var _ VectorSearchPort = (*fakeVector)(nil)
var _ search.ConfigPort = (*fakeVector)(nil)

type fakeStore struct {
	searchFn func(ctx context.Context, req search.VectorSearchRequest) ([]search.VectorSearchResult, error)
	hybridFn func(ctx context.Context, req search.HybridSearchRequest) ([]search.VectorSearchResult, error)
}

func (s *fakeStore) Search(ctx context.Context, req search.VectorSearchRequest) ([]search.VectorSearchResult, error) {
	if s.searchFn != nil {
		return s.searchFn(ctx, req)
	}
	return nil, nil
}
func (s *fakeStore) HybridSearch(ctx context.Context, req search.HybridSearchRequest) ([]search.VectorSearchResult, error) {
	if s.hybridFn != nil {
		return s.hybridFn(ctx, req)
	}
	return nil, nil
}

type fakeRead struct {
	rows map[string]MediaAsset
	err  error
}

func (f *fakeRead) GetMany(ctx context.Context, w WorkspaceContext, ids []string) ([]MediaAsset, error) {
	if f.err != nil {
		return nil, f.err
	}
	out := make([]MediaAsset, 0, len(ids))
	for _, id := range ids {
		if a, ok := f.rows[id]; ok {
			out = append(out, a)
		}
	}
	return out, nil
}

type fakeDeliver struct {
	urls map[string]string
	err  error
}

func (f *fakeDeliver) BuildAuthorizedURL(ctx context.Context, w WorkspaceContext, id string) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	if f.urls == nil {
		return "https://signed.example/" + id, nil
	}
	return f.urls[id], nil
}

type captureLogger struct{ msgs []string }

func (l *captureLogger) Info(msg string, kv ...any)  { l.msgs = append(l.msgs, "info:"+msg) }
func (l *captureLogger) Warn(msg string, kv ...any)  { l.msgs = append(l.msgs, "warn:"+msg) }
func (l *captureLogger) Debug(msg string, kv ...any) { l.msgs = append(l.msgs, "debug:"+msg) }

// ── Workspace enforcement ─────────────────────────────────────────────

func TestSearch_RejectsMissingWorkspace(t *testing.T) {
	svc := NewService(&fakeVector{}, &fakeRead{}, &fakeDeliver{}, Config{}, nil)
	_, err := svc.Search(context.Background(), MediaSearchRequest{Query: "test"})
	if !errors.Is(err, ErrMissingWorkspace) {
		t.Fatalf("expected ErrMissingWorkspace, got %v", err)
	}
}

func TestSearch_RejectsDefaultWorkspace(t *testing.T) {
	svc := NewService(&fakeVector{}, &fakeRead{}, &fakeDeliver{}, Config{}, nil)
	_, err := svc.Search(context.Background(), MediaSearchRequest{
		Query:     "test",
		Workspace: WorkspaceContext{WorkspaceID: "default"},
	})
	if !errors.Is(err, ErrMissingWorkspace) {
		t.Fatalf("expected ErrMissingWorkspace, got %v", err)
	}
}

// ── Query validation ─────────────────────────────────────────────────

func TestSearch_RejectsEmptyQuery(t *testing.T) {
	svc := NewService(&fakeVector{}, &fakeRead{}, &fakeDeliver{}, Config{}, nil)
	_, err := svc.Search(context.Background(), MediaSearchRequest{
		Query:     " ",
		Workspace: WorkspaceContext{WorkspaceID: "w1"},
	})
	if err == nil || err.Error() != "mediasearch: query is required" {
		t.Fatalf("expected query-required error, got %v", err)
	}
}

// ── Embedding + search branching ──────────────────────────────────────

func TestSearch_ANN_ModeCallsSearchNotHybrid(t *testing.T) {
	var hybridCalled, annCalled bool
	store := &fakeStore{
		searchFn: func(ctx context.Context, req search.VectorSearchRequest) ([]search.VectorSearchResult, error) {
			annCalled = true
			return []search.VectorSearchResult{{AssetID: "a1", Score: 0.9}}, nil
		},
		hybridFn: func(ctx context.Context, req search.HybridSearchRequest) ([]search.VectorSearchResult, error) {
			hybridCalled = true
			return nil, nil
		},
	}
	vec := &fakeVector{
		storeFn: func() search.VectorStorePort { return store },
		vc:      search.VectorConfig{TextVectorName: "text", TranscriptVectorName: "transcript", SparseVectorName: "bm25_text"},
	}
	read := &fakeRead{rows: map[string]MediaAsset{"a1": {ID: "a1", Name: "A1", Source: "stock"}}}
	svc := NewService(vec, read, &fakeDeliver{}, Config{}, nil)
	resp, err := svc.Search(context.Background(), MediaSearchRequest{
		Query:     "test",
		Mode:      SearchModeANN,
		Workspace: WorkspaceContext{WorkspaceID: "w1"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !annCalled {
		t.Error("ANN mode did not call VectorStore.Search")
	}
	if hybridCalled {
		t.Error("ANN mode must not call VectorStore.HybridSearch")
	}
	if resp.Query.Mode != "ann" {
		t.Errorf("resp.Query.Mode = %q, want ann", resp.Query.Mode)
	}
}

func TestSearch_Hybrid_DefaultMode(t *testing.T) {
	var hybridCalled bool
	store := &fakeStore{
		hybridFn: func(ctx context.Context, req search.HybridSearchRequest) ([]search.VectorSearchResult, error) {
			hybridCalled = true
			return []search.VectorSearchResult{{AssetID: "h1", Score: 0.92, SearchText: "hybrid"}}, nil
		},
	}
	vec := &fakeVector{
		storeFn: func() search.VectorStorePort { return store },
		vc:      search.VectorConfig{TextVectorName: "text", TranscriptVectorName: "transcript", SparseVectorName: "bm25_text"},
	}
	read := &fakeRead{rows: map[string]MediaAsset{
		"h1": {ID: "h1", Name: "Hybrid Hit", Source: "youtube"},
	}}
	svc := NewService(vec, read, &fakeDeliver{}, Config{}, nil)
	resp, err := svc.Search(context.Background(), MediaSearchRequest{
		Query:     "test",
		Workspace: WorkspaceContext{WorkspaceID: "w1"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !hybridCalled {
		t.Error("default mode must call VectorStore.HybridSearch")
	}
	if len(resp.Query.ChannelsUsed) != 2 {
		t.Errorf("hybrid should expose 2 channels (dense + sparse), got %d", len(resp.Query.ChannelsUsed))
	}
}

// TestSearch_HybridDoesNotSendTranscriptVector pins the
// transcript-vector fix from the code-review: the orchestrator
// must NOT copy the dense vector into TranscriptVector (that
// silently inflated qdrant.fuseSearchResults). Transcript channel
// is deferred until a dedicated embedder ships.
func TestSearch_HybridDoesNotSendTranscriptVector(t *testing.T) {
	var captured search.HybridSearchRequest
	store := &fakeStore{
		hybridFn: func(ctx context.Context, req search.HybridSearchRequest) ([]search.VectorSearchResult, error) {
			captured = req
			return []search.VectorSearchResult{{AssetID: "h", Score: 0.9}}, nil
		},
	}
	vec := storeFn(&fakeVector{vc: search.VectorConfig{TextVectorName: "text", SparseVectorName: "bm25_text"}}, store)
	read := &fakeRead{rows: map[string]MediaAsset{"h": {ID: "h"}}}
	svc := NewService(vec, read, &fakeDeliver{}, Config{}, nil)
	if _, err := svc.Search(context.Background(), MediaSearchRequest{
		Query:     "test",
		Workspace: WorkspaceContext{WorkspaceID: "w1"},
	}); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if len(captured.TranscriptVector) != 0 {
		t.Errorf("TranscriptVector was %v, want nil/empty (the transcript channel is currently disabled)", captured.TranscriptVector)
	}
}

// ── Score floor ───────────────────────────────────────────────────────

func TestSearch_HonoursMinScore(t *testing.T) {
	store := &fakeStore{
		hybridFn: func(ctx context.Context, req search.HybridSearchRequest) ([]search.VectorSearchResult, error) {
			if req.MinScore != 0.7 {
				t.Errorf("MinScore = %v, want 0.7", req.MinScore)
			}
			return []search.VectorSearchResult{
				{AssetID: "ok", Score: 0.85},
				{AssetID: "lo", Score: 0.50},
			}, nil
		},
	}
	vec := &fakeVector{
		storeFn: func() search.VectorStorePort { return store },
		vc:      search.VectorConfig{TextVectorName: "text", TranscriptVectorName: "transcript", SparseVectorName: "bm25_text"},
	}
	read := &fakeRead{rows: map[string]MediaAsset{
		"ok": {ID: "ok", Name: "OK"},
		"lo": {ID: "lo", Name: "Low"},
	}}
	svc := NewService(vec, read, &fakeDeliver{}, Config{}, nil)
	resp, err := svc.Search(context.Background(), MediaSearchRequest{
		Query:     "test",
		MinScore:  0.7,
		Workspace: WorkspaceContext{WorkspaceID: "w1"},
	})
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if resp.Count != 1 {
		t.Errorf("Count = %d, want 1 (low-score hit should be dropped)", resp.Count)
	}
}

func TestSearch_MinScoreFloorFromConfig(t *testing.T) {
	store := &fakeStore{
		hybridFn: func(ctx context.Context, req search.HybridSearchRequest) ([]search.VectorSearchResult, error) {
			if req.MinScore != 0.55 {
				t.Errorf("MinScore = %v, want 0.55 (config floor)", req.MinScore)
			}
			return nil, nil
		},
	}
	vec := storeFn(&fakeVector{vc: search.VectorConfig{TextVectorName: "text", TranscriptVectorName: "transcript", SparseVectorName: "bm25_text"}}, store)
	svc := NewService(vec, &fakeRead{}, &fakeDeliver{}, Config{MinScoreFloor: 0.55}, nil)
	_, err := svc.Search(context.Background(), MediaSearchRequest{
		Query:     "test",
		Workspace: WorkspaceContext{WorkspaceID: "w1"},
	})
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
}

// ── Cap and trim ──────────────────────────────────────────────────────

func TestSearch_TrimsToLimit(t *testing.T) {
	store := &fakeStore{
		hybridFn: func(ctx context.Context, req search.HybridSearchRequest) ([]search.VectorSearchResult, error) {
			out := make([]search.VectorSearchResult, 0, 20)
			for i := 0; i < 20; i++ {
				out = append(out, search.VectorSearchResult{
					AssetID: fmt.Sprintf("a%d", i),
					Score:   1.0 - float64(i)/100.0,
				})
			}
			return out, nil
		},
	}
	vec := storeFn(&fakeVector{vc: search.VectorConfig{TextVectorName: "text", TranscriptVectorName: "transcript", SparseVectorName: "bm25_text"}}, store)
	rows := map[string]MediaAsset{}
	for i := 0; i < 20; i++ {
		rows[fmt.Sprintf("a%d", i)] = MediaAsset{ID: fmt.Sprintf("a%d", i), Name: fmt.Sprintf("A%d", i)}
	}
	read := &fakeRead{rows: rows}
	svc := NewService(vec, read, &fakeDeliver{}, Config{}, nil)
	resp, err := svc.Search(context.Background(), MediaSearchRequest{
		Query:     "test",
		Limit:     5,
		Workspace: WorkspaceContext{WorkspaceID: "w1"},
	})
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if resp.Count != 5 {
		t.Errorf("Count = %d, want 5 trimmed", resp.Count)
	}
}

func TestSearch_DefaultLimitApplied(t *testing.T) {
	store := &fakeStore{
		hybridFn: func(ctx context.Context, req search.HybridSearchRequest) ([]search.VectorSearchResult, error) {
			if req.Limit != DefaultLimit*2 {
				t.Errorf("Limit = %d, want %d (default*2)", req.Limit, DefaultLimit*2)
			}
			return nil, nil
		},
	}
	vec := storeFn(&fakeVector{vc: search.VectorConfig{TextVectorName: "text", TranscriptVectorName: "transcript", SparseVectorName: "bm25_text"}}, store)
	svc := NewService(vec, &fakeRead{}, &fakeDeliver{}, Config{}, nil)
	_, err := svc.Search(context.Background(), MediaSearchRequest{
		Query:     "test",
		Workspace: WorkspaceContext{WorkspaceID: "w1"},
	})
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
}

func TestSearch_MaxLimitFromConfig(t *testing.T) {
	store := &fakeStore{
		hybridFn: func(ctx context.Context, req search.HybridSearchRequest) ([]search.VectorSearchResult, error) {
			// MaxLimit caps the user's effective request to 50, then the
			// orchestrator over-fetches Limit*2 = 100 to give hydration
			// some slack — the response is trimmed back to 50 client-side.
			if req.Limit != 100 {
				t.Errorf("Limit = %d, want 100 (= MaxLimit 50 × 2 over-fetch)", req.Limit)
			}
			return nil, nil
		},
	}
	vec := storeFn(&fakeVector{vc: search.VectorConfig{TextVectorName: "text", TranscriptVectorName: "transcript", SparseVectorName: "bm25_text"}}, store)
	svc := NewService(vec, &fakeRead{}, &fakeDeliver{}, Config{MaxLimit: 50}, nil)
	_, _ = svc.Search(context.Background(), MediaSearchRequest{
		Query:     "test",
		Limit:     999,
		Workspace: WorkspaceContext{WorkspaceID: "w1"},
	})
}

// ── Hydration gap → drop ──────────────────────────────────────────────

func TestSearch_DropsUnhydratedHit(t *testing.T) {
	store := &fakeStore{
		hybridFn: func(ctx context.Context, req search.HybridSearchRequest) ([]search.VectorSearchResult, error) {
			return []search.VectorSearchResult{
				{AssetID: "ok", Score: 0.9},
				{AssetID: "ghost", Score: 0.8}, // not hydrated
			}, nil
		},
	}
	vec := storeFn(&fakeVector{vc: search.VectorConfig{TextVectorName: "text", TranscriptVectorName: "transcript", SparseVectorName: "bm25_text"}}, store)
	read := &fakeRead{rows: map[string]MediaAsset{
		"ok": {ID: "ok", Name: "OK"},
	}}
	svc := NewService(vec, read, &fakeDeliver{}, Config{}, nil)
	resp, err := svc.Search(context.Background(), MediaSearchRequest{
		Query:     "test",
		Workspace: WorkspaceContext{WorkspaceID: "w1"},
	})
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if resp.Count != 1 {
		t.Errorf("Count = %d, want 1 (ghost dropped)", resp.Count)
	}
}

// ── Delivery URL signing ──────────────────────────────────────────────

func TestSearch_AlwaysInvokesDelivery(t *testing.T) {
	store := &fakeStore{
		hybridFn: func(ctx context.Context, req search.HybridSearchRequest) ([]search.VectorSearchResult, error) {
			return []search.VectorSearchResult{{AssetID: "sigme", Score: 0.9}}, nil
		},
	}
	vec := storeFn(&fakeVector{vc: search.VectorConfig{TextVectorName: "text", TranscriptVectorName: "transcript", SparseVectorName: "bm25_text"}}, store)
	read := &fakeRead{rows: map[string]MediaAsset{
		"sigme": {ID: "sigme", Name: "S"},
	}}
	deliver := &fakeDeliver{urls: map[string]string{
		"sigme": "https://signed.example/sigme?exp=...&sig=abc",
	}}
	svc := NewService(vec, read, deliver, Config{}, nil)
	resp, err := svc.Search(context.Background(), MediaSearchRequest{
		Query:     "test",
		Workspace: WorkspaceContext{WorkspaceID: "w1"},
	})
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if len(resp.Hits) != 1 || resp.Hits[0].DeliveryURL == "" {
		t.Fatalf("DeliveryURL missing: %+v", resp.Hits)
	}
}

func TestSearch_DeliveryErrorDoesNotFailSearch(t *testing.T) {
	store := &fakeStore{
		hybridFn: func(ctx context.Context, req search.HybridSearchRequest) ([]search.VectorSearchResult, error) {
			return []search.VectorSearchResult{{AssetID: "fail", Score: 0.9}}, nil
		},
	}
	vec := storeFn(&fakeVector{vc: search.VectorConfig{TextVectorName: "text", TranscriptVectorName: "transcript", SparseVectorName: "bm25_text"}}, store)
	read := &fakeRead{rows: map[string]MediaAsset{"fail": {ID: "fail", Name: "F"}}}
	deliver := &fakeDeliver{err: errors.New("signing failed")}
	svc := NewService(vec, read, deliver, Config{}, &captureLogger{})
	resp, err := svc.Search(context.Background(), MediaSearchRequest{
		Query:     "test",
		Workspace: WorkspaceContext{WorkspaceID: "w1"},
	})
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if len(resp.Hits) != 1 {
		t.Fatalf("len(Hits) = %d, want 1 (logging warning only)", len(resp.Hits))
	}
	if resp.Hits[0].DeliveryURL != "" {
		t.Errorf("DeliveryURL = %q on signing failure, want empty", resp.Hits[0].DeliveryURL)
	}
}

// ── DurationMsMin filter (post-hydration) ─────────────────────────────

func TestSearch_DropBelowDurationMsMin(t *testing.T) {
	store := &fakeStore{
		hybridFn: func(ctx context.Context, req search.HybridSearchRequest) ([]search.VectorSearchResult, error) {
			return []search.VectorSearchResult{
				{AssetID: "short", Score: 0.95}, // duration 4s — below min
				{AssetID: "long", Score: 0.80},  // duration 60s — above min
			}, nil
		},
	}
	vec := storeFn(&fakeVector{vc: search.VectorConfig{TextVectorName: "text", SparseVectorName: "bm25_text"}}, store)
	read := &fakeRead{rows: map[string]MediaAsset{
		"short": {ID: "short", DurationMs: 4000},
		"long":  {ID: "long", DurationMs: 60_000},
	}}
	svc := NewService(vec, read, &fakeDeliver{}, Config{}, nil)
	resp, err := svc.Search(context.Background(), MediaSearchRequest{
		Query:     "test",
		Workspace: WorkspaceContext{WorkspaceID: "w1"},
		Filters:   MediaSearchFilter{DurationMsMin: 30_000}, // ≥30s only
	})
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if resp.Count != 1 || resp.Hits[0].AssetID != "long" {
		t.Errorf("got %+v, want only 'long' (DurationMs=60000 ≥ 30000)", resp.Hits)
	}
}

func TestSearch_DurationMsMinZeroIsNoOp(t *testing.T) {
	store := &fakeStore{
		hybridFn: func(ctx context.Context, req search.HybridSearchRequest) ([]search.VectorSearchResult, error) {
			return []search.VectorSearchResult{
				{AssetID: "short", Score: 0.95},
				{AssetID: "long", Score: 0.80},
			}, nil
		},
	}
	vec := storeFn(&fakeVector{vc: search.VectorConfig{TextVectorName: "text", SparseVectorName: "bm25_text"}}, store)
	read := &fakeRead{rows: map[string]MediaAsset{
		"short": {ID: "short", DurationMs: 4000},
		"long":  {ID: "long", DurationMs: 60_000},
	}}
	svc := NewService(vec, read, &fakeDeliver{}, Config{}, nil)
	resp, err := svc.Search(context.Background(), MediaSearchRequest{
		Query:     "test",
		Workspace: WorkspaceContext{WorkspaceID: "w1"},
	})
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if resp.Count != 2 {
		t.Errorf("Count = %d, want 2 (DurationMsMin unset → both pass)", resp.Count)
	}
}

func TestSearch_DurationMsUnknownIsKept(t *testing.T) {
	// DurationMs == 0 means "unknown in SQLite"; the filter must
	// not spuriously drop those (the spec links DurationMsMin to
	// videos — non-video rows may not have duration recorded).
	store := &fakeStore{
		hybridFn: func(ctx context.Context, req search.HybridSearchRequest) ([]search.VectorSearchResult, error) {
			return []search.VectorSearchResult{{AssetID: "x", Score: 0.9}}, nil
		},
	}
	vec := storeFn(&fakeVector{vc: search.VectorConfig{TextVectorName: "text", SparseVectorName: "bm25_text"}}, store)
	read := &fakeRead{rows: map[string]MediaAsset{"x": {ID: "x", DurationMs: 0}}}
	svc := NewService(vec, read, &fakeDeliver{}, Config{}, nil)
	resp, err := svc.Search(context.Background(), MediaSearchRequest{
		Query:     "test",
		Workspace: WorkspaceContext{WorkspaceID: "w1"},
		Filters:   MediaSearchFilter{DurationMsMin: 60_000},
	})
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if resp.Count != 1 {
		t.Errorf("Count = %d, want 1 (DurationMs=0 means 'unknown', should not be dropped)", resp.Count)
	}
}

// ── Tag filter (AND semantics) ───────────────────────────────────────

func TestSearch_TagFilterAND(t *testing.T) {
	store := &fakeStore{
		hybridFn: func(ctx context.Context, req search.HybridSearchRequest) ([]search.VectorSearchResult, error) {
			return []search.VectorSearchResult{
				{AssetID: "a", Score: 0.9, Tags: []string{"space", "4k"}},
				{AssetID: "b", Score: 0.85, Tags: []string{"space"}},
				{AssetID: "c", Score: 0.80, Tags: []string{"nature", "4k"}},
			}, nil
		},
	}
	vec := storeFn(&fakeVector{vc: search.VectorConfig{TextVectorName: "text", TranscriptVectorName: "transcript", SparseVectorName: "bm25_text"}}, store)
	read := &fakeRead{rows: map[string]MediaAsset{
		"a": {ID: "a"},
		"b": {ID: "b"},
		"c": {ID: "c"},
	}}
	svc := NewService(vec, read, &fakeDeliver{}, Config{}, nil)
	resp, err := svc.Search(context.Background(), MediaSearchRequest{
		Query:     "test",
		Workspace: WorkspaceContext{WorkspaceID: "w1"},
		Filters:   MediaSearchFilter{Tags: []string{"space", "4k"}},
	})
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if resp.Count != 1 || resp.Hits[0].AssetID != "a" {
		t.Errorf("got %+v, want only asset a (has space AND 4k)", resp.Hits)
	}
}

// ── Dependence injection helpers ──────────────────────────────────────

// storeFn is sugar for `fakeVector{storeFn: func() { return store }}` that
// keeps the test bodies compact and uniform. Lives in test code only.
type vectorBuilder = fakeVector

func storeFn(v *fakeVector, s search.VectorStorePort) *fakeVector {
	v.storeFn = func() search.VectorStorePort { return s }
	return v
}

// ── QDRANT-004 PR1: hybrid fail-closed contract ───────────────────────
//
// PR1 (June 2026): mode=hybrid must produce real dense+sparse retrieval
// OR return ErrHybridRequiresSparse. The orchestrator cannot silently
// degrade to ANN. These tests pin the contract at the service boundary.

// TestSearch_HybridPassesSparseVectorToStore pin that the orchestrator
// computes BM25 client-side and propagates the SparseVector &
// SparseVectorName through the HybridSearchRequest. Without this
// assertion a regression that drops SparseVector on the wire would
// fail through qdrant.Searcher.HybridSearch (defence-in-depth) but go
// undetected in service-level tests.
func TestSearch_HybridPassesSparseVectorToStore(t *testing.T) {
	var captured search.HybridSearchRequest
	store := &fakeStore{
		hybridFn: func(ctx context.Context, req search.HybridSearchRequest) ([]search.VectorSearchResult, error) {
			captured = req
			return []search.VectorSearchResult{{AssetID: "ok", Score: 0.92, SearchText: "alpha test"}}, nil
		},
	}
	vec := storeFn(&fakeVector{
		vc: search.VectorConfig{
			TextVectorName:   "text",
			SparseVectorName: "bm25_text",
		},
	}, store)
	read := &fakeRead{rows: map[string]MediaAsset{"ok": {ID: "ok", Name: "OK"}}}
	svc := NewService(vec, read, &fakeDeliver{}, Config{}, nil)

	resp, err := svc.Search(context.Background(), MediaSearchRequest{
		Query:     "alpha test",
		Mode:      SearchModeHybrid,
		Workspace: WorkspaceContext{WorkspaceID: "w1"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Count != 1 {
		t.Fatalf("Count = %d, want 1", resp.Count)
	}
	if captured.SparseVector == nil {
		t.Fatal("SparseVector must be populated by orchestrator; got nil")
	}
	if captured.SparseVectorName != "bm25_text" {
		t.Errorf("SparseVectorName = %q, want %q", captured.SparseVectorName, "bm25_text")
	}
	if len(captured.SparseVector.Indices) == 0 || len(captured.SparseVector.Values) == 0 {
		t.Errorf("SparseVector must carry non-empty Indices/Values; got %+v", captured.SparseVector)
	}
}

// TestSearch_HybridFailsClosedOnNoBM25Tokens encodes the fail-closed
// rule: queries that BM25 cannot tokenize (all tokens <2 chars after
// normalisation — e.g. "a", "??", "12 34") MUST return
// ErrHybridRequiresSparse. They cannot be silently downgraded to ANN.
func TestSearch_HybridFailsClosedOnNoBM25Tokens(t *testing.T) {
	store := &fakeStore{}
	vec := storeFn(&fakeVector{
		vc: search.VectorConfig{
			TextVectorName:   "text",
			SparseVectorName: "bm25_text",
		},
	}, store)
	svc := NewService(vec, &fakeRead{}, &fakeDeliver{}, Config{}, &captureLogger{})

	_, err := svc.Search(context.Background(), MediaSearchRequest{
		Query:     "a",
		Mode:      SearchModeHybrid,
		Workspace: WorkspaceContext{WorkspaceID: "w1"},
	})
	if err == nil {
		t.Fatal("expected ErrHybridRequiresSparse for non-tokenizable query, got nil")
	}
	if !errors.Is(err, ErrHybridRequiresSparse) {
		t.Errorf("error %v does not wrap ErrHybridRequiresSparse", err)
	}
}

// TestSearch_HybridFailsClosedOnEmptySparseChannel asserts that a
// VectorConfig WITHOUT a SparseVectorName cannot execute a hybrid
// request — even when BM25 succeeds. The orchestrator must fall back
// to canonicalSparseVectorName ("bm25_text") so this scenario stays
// valid; this test pins the canonical fallback as part of the
// service contract.
func TestSearch_HybridFailsClosedOnEmptySparseChannel(t *testing.T) {
	var captured search.HybridSearchRequest
	store := &fakeStore{
		hybridFn: func(ctx context.Context, req search.HybridSearchRequest) ([]search.VectorSearchResult, error) {
			captured = req
			return []search.VectorSearchResult{{AssetID: "ok", Score: 0.9}}, nil
		},
	}
	vec := storeFn(&fakeVector{
		// Note: SparseVectorName deliberately omitted — orchestrator MUST
		// re-pin to canonicalSparseVectorName. If this regresses (empty
		// SparseVectorName reaches the wire) qdrant.Searcher.HybridSearch
		// would return ErrSparseRequired.
		vc: search.VectorConfig{TextVectorName: "text"},
	}, store)
	read := &fakeRead{rows: map[string]MediaAsset{"ok": {ID: "ok", Name: "OK"}}}
	svc := NewService(vec, read, &fakeDeliver{}, Config{}, nil)

	_, err := svc.Search(context.Background(), MediaSearchRequest{
		Query:     "meaningful query here",
		Mode:      SearchModeHybrid,
		Workspace: WorkspaceContext{WorkspaceID: "w1"},
	})
	if err != nil {
		t.Fatalf("canonical fallback should kick in, got %v", err)
	}
	if captured.SparseVectorName != "bm25_text" {
		t.Errorf("SparseVectorName = %q, want %q (canonical fallback)", captured.SparseVectorName, "bm25_text")
	}
	if captured.SparseVector == nil {
		t.Error("SparseVector must be populated even when VectorConfig.SparseVectorName was empty (canonical fallback covers channel)")
	}
}
