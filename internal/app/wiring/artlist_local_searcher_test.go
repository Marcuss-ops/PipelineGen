package wiring

import (
	"context"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers/artlist"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	pgmedia "github.com/Marcuss-ops/PipelineGen/internal/platform/postgres/media"
)

type fakeArtlistLocalStore struct {
	got  pgmedia.LocalMediaSearchRequest
	recs []pgmedia.MediaAssetRecord
}

func (f *fakeArtlistLocalStore) SearchLocal(_ context.Context, req pgmedia.LocalMediaSearchRequest) ([]pgmedia.MediaAssetRecord, error) {
	f.got = req
	return f.recs, nil
}

// TestArtlistLocalSearcher_ReadsPostgresMediaSSOT pins MEDIA-SSOT P1-5: the
// Artlist local catalog searcher must query the PostgreSQL media SSOT with the
// canonical source filter, and must map a media record onto the provider
// candidate shape the search chain consumes.
func TestArtlistLocalSearcher_ReadsPostgresMediaSSOT(t *testing.T) {
	store := &fakeArtlistLocalStore{recs: []pgmedia.MediaAssetRecord{{
		ID: "al-1", Name: "Skyline", Title: "Skyline dawn", Source: "artlist",
		MediaType: "video", DurationMS: 4200, ThumbnailURL: "https://thumb",
		SourceURL: "https://artlist.io/clip/1", Tags: []string{"city", "dawn"},
		MetadataJSON: `{"description":"a skyline","creator":"jane","preview_url":"https://preview","provider_categories":["urban"]}`,
	}}}
	searcher := newArtlistLocalSearcher(store)
	if searcher == nil {
		t.Fatal("expected a wired searcher for a non-nil store")
	}

	cands, err := searcher.Search(context.Background(), artlist.SearchRequest{Term: "  skyline "})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if store.got.Text != "skyline" || store.got.Source != "artlist" || !store.got.ExcludeUnclassified {
		t.Fatalf("unexpected SearchLocal request: %+v", store.got)
	}
	if store.got.Limit != artlistLocalSearchDefaultLimit {
		t.Fatalf("default limit = %d, want %d", store.got.Limit, artlistLocalSearchDefaultLimit)
	}
	if len(cands) != 1 {
		t.Fatalf("candidates = %d, want 1", len(cands))
	}
	got := cands[0]
	if got.ID != "al-1" || got.ExternalID != "al-1" || got.Provider != "artlist" {
		t.Fatalf("identity mapping wrong: %+v", got)
	}
	if got.Title != "Skyline dawn" || got.Description != "a skyline" || got.Creator != "jane" {
		t.Fatalf("text mapping wrong: %+v", got)
	}
	if got.PageURL != "https://artlist.io/clip/1" || got.PreviewURL != "https://preview" {
		t.Fatalf("url mapping wrong: %+v", got)
	}
	if got.DurationMs != 4200 || got.Duration.Milliseconds() != 4200 {
		t.Fatalf("duration mapping wrong: %+v", got)
	}
	if len(got.Keywords) != 2 || len(got.Categories) != 1 || got.Categories[0] != "urban" {
		t.Fatalf("keyword/category mapping wrong: %+v", got)
	}
}

// fakeArtlistAssetStore is a complete operational store double. It records the
// split-brain surface (SearchClips/SearchByTerms — the ones the decorator must
// take over) so a test can assert they are never reached, and answers the
// remaining methods so delegation is observable.
type fakeArtlistAssetStore struct {
	searchClipsCalls int
	searchTermsCalls int
	countClipsCalls  int
}

var _ artlist.AssetStore = (*fakeArtlistAssetStore)(nil)

func (f *fakeArtlistAssetStore) Get(context.Context, string) (*asset.Asset, error) {
	return nil, nil
}

func (f *fakeArtlistAssetStore) Upsert(context.Context, *asset.Asset) error { return nil }

func (f *fakeArtlistAssetStore) SearchByTerms(_ context.Context, _ string, _ []string, _ int) ([]*asset.Asset, error) {
	f.searchTermsCalls++
	return nil, nil
}

func (f *fakeArtlistAssetStore) SearchClips(_ context.Context, _, _ string) ([]*asset.Asset, error) {
	f.searchClipsCalls++
	return nil, nil
}

func (f *fakeArtlistAssetStore) CountClips(context.Context) (int, error) {
	f.countClipsCalls++
	return 7, nil
}

func (f *fakeArtlistAssetStore) CountBySource(context.Context, string) (int, error) { return 7, nil }

func (f *fakeArtlistAssetStore) LastUpdatedAtForTerm(context.Context, string) (*string, error) {
	return nil, nil
}

func (f *fakeArtlistAssetStore) UpdateSearchTerms(context.Context, string, string, string, []string, string) error {
	return nil
}

// TestArtlistMediaSSOTAssetStore_SearchClipsReadsPostgres pins MEDIA-SSOT P2-9
// unblock step 1: the Artlist DB-only search must resolve against the
// PostgreSQL media SSOT (media_assets.search_terms) instead of the operational
// clip_search_terms index, so a clip committed by the canonical PG committer
// is actually visible to /api/artlist/search.
func TestArtlistMediaSSOTAssetStore_SearchClipsReadsPostgres(t *testing.T) {
	delegate := &fakeArtlistAssetStore{}
	store := &fakeArtlistLocalStore{recs: []pgmedia.MediaAssetRecord{{
		ID: "al-9", Name: "Harbour", Source: "artlist", MediaType: "video",
	}}}
	sut := newArtlistMediaSSOTAssetStore(delegate, store)
	if sut == nil {
		t.Fatal("expected a decorator for non-nil handles")
	}

	clips, err := sut.SearchClips(context.Background(), "artlist", "harbour night")
	if err != nil {
		t.Fatalf("SearchClips: %v", err)
	}
	if delegate.searchClipsCalls != 0 || delegate.searchTermsCalls != 0 {
		t.Fatalf("the operational store's search surface must NOT be reached: %+v", delegate)
	}
	if got := store.got.AllTerms; len(got) != 2 || got[0] != "harbour" || got[1] != "night" {
		t.Fatalf("AllTerms = %v, want the split keywords (AND semantics)", got)
	}
	if store.got.Source != "artlist" || !store.got.ExcludeUnclassified {
		t.Fatalf("unexpected search request: %+v", store.got)
	}
	if store.got.Limit != artlistLocalSearchDefaultLimit {
		t.Fatalf("limit = %d, want the retired adapter's page size %d", store.got.Limit, artlistLocalSearchDefaultLimit)
	}
	if len(clips) != 1 || clips[0] == nil || clips[0].ID != "al-9" {
		t.Fatalf("clips = %+v, want the hydrated PostgreSQL record", clips)
	}
}

// TestArtlistMediaSSOTAssetStore_SearchByTermsHonorsLimit pins that the
// multi-keyword entry point keeps the caller's limit and the AND semantics.
func TestArtlistMediaSSOTAssetStore_SearchByTermsHonorsLimit(t *testing.T) {
	store := &fakeArtlistLocalStore{}
	sut := newArtlistMediaSSOTAssetStore(&fakeArtlistAssetStore{}, store)
	if _, err := sut.SearchByTerms(context.Background(), "artlist", []string{" one ", "", "two"}, 5); err != nil {
		t.Fatalf("SearchByTerms: %v", err)
	}
	if store.got.Limit != 5 {
		t.Errorf("limit = %d, want 5", store.got.Limit)
	}
	if len(store.got.AllTerms) != 2 || store.got.AllTerms[0] != "one" || store.got.AllTerms[1] != "two" {
		t.Errorf("AllTerms = %v, want the trimmed non-empty keywords", store.got.AllTerms)
	}
}

// TestArtlistMediaSSOTAssetStore_DelegatesRemainingMethods pins that the
// decorator is surgical: methods outside the DB-only search surface still go
// to the operational store.
func TestArtlistMediaSSOTAssetStore_DelegatesRemainingMethods(t *testing.T) {
	delegate := &fakeArtlistAssetStore{}
	sut := newArtlistMediaSSOTAssetStore(delegate, &fakeArtlistLocalStore{})
	got, err := sut.CountClips(context.Background())
	if err != nil {
		t.Fatalf("CountClips: %v", err)
	}
	if got != 7 || delegate.countClipsCalls != 1 {
		t.Fatalf("CountClips = %d (delegate calls %d), want the delegate's answer", got, delegate.countClipsCalls)
	}
}

// TestArtlistMediaSSOTAssetStore_NoStoreKeepsDelegate pins the fail-closed
// boundary: without the PostgreSQL media handle the decorator is a no-op, so
// the SQLite-only degrade mode keeps its documented behaviour rather than
// starting to return empty results.
func TestArtlistMediaSSOTAssetStore_NoStoreKeepsDelegate(t *testing.T) {
	delegate := &fakeArtlistAssetStore{}
	if got := newArtlistMediaSSOTAssetStore(delegate, nil); got != artlist.AssetStore(delegate) {
		t.Fatalf("newArtlistMediaSSOTAssetStore(delegate, nil) = %T, want the delegate unchanged", got)
	}
	if got := newArtlistMediaSSOTAssetStore(nil, &fakeArtlistLocalStore{}); got != nil {
		t.Fatalf("newArtlistMediaSSOTAssetStore(nil, store) = %T, want nil", got)
	}
}

// TestArtlistLocalSearcher_FailsClosedWithoutStore pins the fail-closed
// contract: no PostgreSQL store means no local searcher, never a SQLite
// fallback.
func TestArtlistLocalSearcher_FailsClosedWithoutStore(t *testing.T) {
	if got := newArtlistLocalSearcher(nil); got != nil {
		t.Fatalf("newArtlistLocalSearcher(nil) = %T, want nil (fail-closed)", got)
	}
}

// TestArtlistLocalSearcher_EmptyTermSkipsQuery keeps the retired SQLite
// adapter's empty-term short circuit, so a blank query cannot fan out a
// full-catalog scan.
func TestArtlistLocalSearcher_EmptyTermSkipsQuery(t *testing.T) {
	store := &fakeArtlistLocalStore{}
	searcher := newArtlistLocalSearcher(store)
	cands, err := searcher.Search(context.Background(), artlist.SearchRequest{Term: "   "})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if cands != nil {
		t.Fatalf("candidates = %+v, want nil", cands)
	}
	if store.got.Text != "" {
		t.Fatalf("empty term must not query the store, got %+v", store.got)
	}
}
