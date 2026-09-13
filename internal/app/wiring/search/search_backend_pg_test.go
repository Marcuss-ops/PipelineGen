package search

import (
	"context"
	"testing"

	search "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/search"
	pgmedia "github.com/Marcuss-ops/PipelineGen/internal/platform/postgres/media"
	"go.uber.org/zap"
)

type fakeLocalStore struct {
	textReq  pgmedia.LocalMediaSearchRequest
	hashArg  string
	hashLim  int
	records  []pgmedia.MediaAssetRecord
	hashRecs []pgmedia.MediaAssetRecord
}

func (f *fakeLocalStore) SearchLocal(_ context.Context, req pgmedia.LocalMediaSearchRequest) ([]pgmedia.MediaAssetRecord, error) {
	f.textReq = req
	return f.records, nil
}

func (f *fakeLocalStore) FindAllByHash(_ context.Context, hash string, limit int) ([]pgmedia.MediaAssetRecord, error) {
	f.hashArg = hash
	f.hashLim = limit
	return f.hashRecs, nil
}

func TestPgLocalSearchBackend_Text(t *testing.T) {
	store := &fakeLocalStore{records: []pgmedia.MediaAssetRecord{{
		ID: "a1", Name: "boxing training", Title: "Boxing", Source: "artlist",
		MediaType: "video", DurationMS: 12000, Tags: []string{"boxing"},
		ThumbnailURL: "https://thumb", DriveLink: "https://drive/a1",
	}}}
	b := &pgLocalSearchBackend{store: store, log: zap.NewNop()}

	cands, err := b.Search(context.Background(), search.Query{
		Text:  "boxing",
		Limit: 5,
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if store.textReq.Text != "boxing" || !store.textReq.ExcludeUnclassified {
		t.Fatalf("unexpected SearchLocal request: %+v", store.textReq)
	}
	if store.textReq.Source != "all" {
		t.Fatalf("empty filter source must normalise to all, got %q", store.textReq.Source)
	}
	if len(cands) != 1 || cands[0].AssetID != "a1" || cands[0].Title != "Boxing" {
		t.Fatalf("unexpected candidates: %+v", cands)
	}
	if cands[0].Score <= 0 {
		t.Fatalf("expected a positive local score, got %v", cands[0].Score)
	}
}

func TestPgLocalSearchBackend_Hash(t *testing.T) {
	store := &fakeLocalStore{hashRecs: []pgmedia.MediaAssetRecord{{ID: "h1", Source: "stock", MediaType: "video", Name: "dup"}}}
	b := &pgLocalSearchBackend{store: store, log: zap.NewNop()}

	cands, err := b.Search(context.Background(), search.Query{Hash: "sha256:abc", Limit: 7})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if store.hashArg != "sha256:abc" || store.hashLim != 7 {
		t.Fatalf("unexpected hash call: arg=%q lim=%d", store.hashArg, store.hashLim)
	}
	if len(cands) != 1 || cands[0].AssetID != "h1" || cands[0].Hash != "sha256:abc" || cands[0].Score != 1.0 {
		t.Fatalf("unexpected hash candidates: %+v", cands)
	}
}

func TestBuildSearchBackends_PrefersPostgresLocalStore(t *testing.T) {
	reg, err := BuildSearchBackends(SearchBackendBuildOpts{
		Logger:          zap.NewNop(),
		MediaLocalStore: &fakeLocalStore{},
	})
	if err != nil {
		t.Fatalf("BuildSearchBackends: %v", err)
	}
	var found bool
	for _, b := range reg.All() {
		if b.Name() != "local" {
			continue
		}
		found = true
		if _, ok := b.(*pgLocalSearchBackend); !ok {
			t.Fatalf("local backend = %T, want *pgLocalSearchBackend", b)
		}
	}
	if !found {
		t.Fatal("expected a local backend to be registered")
	}
}

// TestBuildSearchBackends_FailsClosedWithoutPostgresLocalStore pins MEDIA-SSOT
// P1-6: when the PostgreSQL local media store is unavailable the local media
// capability must NOT be registered from any other source. The retired legacy
// SQLite backend (which read media_assets through sqassets.ClipsRepository)
// would have answered local/hash/keyword queries from a mirror that a
// PostgreSQL media write can never update — a read split-brain.
func TestBuildSearchBackends_FailsClosedWithoutPostgresLocalStore(t *testing.T) {
	reg, err := BuildSearchBackends(SearchBackendBuildOpts{Logger: zap.NewNop()})
	if err != nil {
		t.Fatalf("BuildSearchBackends: %v", err)
	}
	for _, b := range reg.All() {
		if b.Name() == "local" {
			t.Fatalf("local media backend %T must not be registered without the PostgreSQL local store", b)
		}
	}
}
