// Package mediamemory — discovery_worker_test.go is the Fase 3.1
// unit-test surface for defaultDiscoveryWorker.
//
// godlike/06 SSOT (no-download contract pin): the canonical Cold-
// tier invariant is that AssetID is "" on every persisted row.
// A regression here would let candidates jump from Cold to Hot
// without a discover→materialize pipeline.
package mediamemory

import (
	"context"
	"errors"
	"testing"
)

// ── Fake SearchFanOut (port-level) ──────────────────────────────

// fakeSearchFanOutDiscovery mirrors the existing resolver_test.go
// pattern but lets the test seed per-(provider, query) results so
// the catalog_only fan-out assertions stay deterministic.
type fakeSearchFanOutDiscovery struct {
	results map[string]SearchFanOutResult // keyed by "provider|query"
	err     error
	calls   int
}

func (f *fakeSearchFanOutDiscovery) Search(_ context.Context, q SearchFanOutQuery) (SearchFanOutResult, error) {
	f.calls++
	if f.err != nil {
		return SearchFanOutResult{}, f.err
	}
	if len(q.Sources) == 0 {
		return SearchFanOutResult{}, nil
	}
	key := q.Sources[0] + "|" + q.Text
	if r, ok := f.results[key]; ok {
		return r, nil
	}
	return SearchFanOutResult{}, nil
}

// ── Fake CandidateRepository (port-level) ────────────────────────

// fakeCandidateRepoDiscovery is an in-memory CandidateRepository
// that records persisted rows + a canary on AssetID to assert
// the no-download invariant.
type fakeCandidateRepoDiscovery struct {
	byID    map[string]MediaCandidate
	byUID   map[string]MediaCandidate // keyed by (provider, provider_asset_id)
	failSet error                     // injected failure for UpsertInsert
}

func newFakeCandidateRepoDiscovery() *fakeCandidateRepoDiscovery {
	return &fakeCandidateRepoDiscovery{
		byID:  make(map[string]MediaCandidate),
		byUID: make(map[string]MediaCandidate),
	}
}

func (r *fakeCandidateRepoDiscovery) UpsertInsert(_ context.Context, c MediaCandidate) (MediaCandidate, error) {
	if r.failSet != nil {
		return MediaCandidate{}, r.failSet
	}
	uid := c.Provider + "|" + c.ProviderAssetID
	if existing, ok := r.byUID[uid]; ok {
		return existing, errors.Join(ErrDuplicateBinding,
			errors.New("uniqueness conflict on "+uid))
	}
	r.byID[c.ID] = c
	r.byUID[uid] = c
	return c, nil
}

func (r *fakeCandidateRepoDiscovery) FindByID(_ context.Context, id string) (MediaCandidate, error) {
	c, ok := r.byID[id]
	if !ok {
		return MediaCandidate{}, errors.Join(ErrCandidateNotFound,
			errors.New("no row id="+id))
	}
	return c, nil
}

func (r *fakeCandidateRepoDiscovery) ListByProvider(_ context.Context, _ string, _ int) ([]MediaCandidate, error) {
	return nil, nil
}
func (r *fakeCandidateRepoDiscovery) ListPendingMaterialization(_ context.Context, _ int) ([]MediaCandidate, error) {
	return nil, nil
}
func (r *fakeCandidateRepoDiscovery) UpdateStatus(_ context.Context, _ string, _ DiscoveryStatus, _ MaterializationStatus) error {
	return nil
}

// ── no-download invariant ─────────────────────────────────

func TestDiscoveryWorker_DoesNotSetAssetIDOnPersistedCandidates(t *testing.T) {
	t.Parallel()

	// The SearchFanOut mock tries to set AssetID on the candidate;
	// the worker MUST reject that row (canonical no-download
	// invariant) and the persisted rows MUST all have AssetID=""".
	repo := newFakeCandidateRepoDiscovery()
	fo := &fakeSearchFanOutDiscovery{
		results: map[string]SearchFanOutResult{
			"artlist|Maya pyramids": {
				Candidates: []MediaCandidate{
					{
						Provider:        "artlist",
						ProviderAssetID: "asset-1",
						SourceURL:       "https://artlist.example.com/asset-1",
						Title:           "Maya pyramid 1",
						DurationMs:      8000,
						// AssetID intentionally set here to test the worker
						// rejects it. The worker MUST drop this row.
						AssetID:               "asset-already-materialized", // NO-DOWNLOAD VIOLATION
						RightsStatus:          RightsVerified,
						DiscoveryStatus:       DiscoveryQueued,     // worker MUST overwrite to DiscoverySearched
						MaterializationStatus: MaterializationWarm, // worker MUST overwrite to MaterializationCold
					},
					{
						Provider:        "artlist",
						ProviderAssetID: "asset-2",
						SourceURL:       "https://artlist.example.com/asset-2",
						Title:           "Maya pyramid 2",
						DurationMs:      12000,
						RightsStatus:    RightsUnknown,
						// AssetID correctly empty; RightsUnknown is still
						// persisted by the discovery worker (which only
						// drops RightsDenied/RightsExpired at the gate).
						// At the ranker.Filter level (godlike/07 Fase 1.5
						// fail-closed) the resolver flags MissingRights=true
						// and PopulateRightsPenalty assigns 1.0, so the
						// candidate is excluded from top-K auto-promotion.
					},
				},
			},
		},
	}
	w := NewDefaultDiscoveryWorker(repo, fo, NoopLogger(), nil)

	res, err := w.Discover(context.Background(), DiscoveryRequest{
		Query:      "Maya pyramids",
		Provider:   "artlist",
		Language:   "it",
		MediaTypes: []string{"video"},
		Limit:      10,
	})
	if err != nil {
		t.Fatalf("Discover returned error: %v", err)
	}
	if len(res.PersistedCandidateIDs) != 1 {
		t.Fatalf("want exactly 1 persisted candidate (the AssetID-empty one); got %d IDs=%v",
			len(res.PersistedCandidateIDs), res.PersistedCandidateIDs)
	}
	persisted, ok := repo.byID[res.PersistedCandidateIDs[0]]
	if !ok {
		t.Fatalf("persisted ID %v not in repo", res.PersistedCandidateIDs)
	}
	if persisted.AssetID != "" {
		t.Fatalf("CANONICAL NO-DOWNLOAD INVARIANT BROKEN: persisted AssetID=%q (must be \"\" on Cold tier)",
			persisted.AssetID)
	}
	if persisted.DiscoveryStatus != DiscoverySearched {
		t.Fatalf("persisted DiscoveryStatus = %q, want DiscoverySearched (canonical initial state)",
			string(persisted.DiscoveryStatus))
	}
	if persisted.MaterializationStatus != MaterializationCold {
		t.Fatalf("persisted MaterializationStatus = %q, want MaterializationCold (canonical Cold tier on discovery)",
			string(persisted.MaterializationStatus))
	}
	// The failure pipeline must have recorded the rejected AssetID row.
	if len(res.Failures) == 0 {
		t.Fatalf("rejected AssetID row MUST surface as a typed failure; got 0 failures")
	}
}

// ── rights gate drop ─────────────────────────────────────────

func TestDiscoveryWorker_DropsDeniedAndExpiredRights(t *testing.T) {
	t.Parallel()

	repo := newFakeCandidateRepoDiscovery()
	fo := &fakeSearchFanOutDiscovery{
		results: map[string]SearchFanOutResult{
			"youtube|Maya calendar": {
				Candidates: []MediaCandidate{
					{Provider: "youtube", ProviderAssetID: "yt-1", SourceURL: "https://yt/1",
						RightsStatus: RightsVerified},
					{Provider: "youtube", ProviderAssetID: "yt-2", SourceURL: "https://yt/2",
						RightsStatus: RightsDenied}, // dropped
					{Provider: "youtube", ProviderAssetID: "yt-3", SourceURL: "https://yt/3",
						RightsStatus: RightsExpired}, // dropped
					{Provider: "youtube", ProviderAssetID: "yt-4", SourceURL: "https://yt/4",
						RightsStatus: RightsUnknown}, // discovery worker persists; ranker.Filter drops (godlike/07 Fase 1.5)
				},
			},
		},
	}
	w := NewDefaultDiscoveryWorker(repo, fo, NoopLogger(), nil)
	res, err := w.Discover(context.Background(), DiscoveryRequest{
		Query: "Maya calendar", Provider: "youtube",
	})
	if err != nil {
		t.Fatalf("Discover error: %v", err)
	}
	if len(res.PersistedCandidateIDs) != 2 {
		t.Fatalf("want 2 persisted (verified + unknown); got %d", len(res.PersistedCandidateIDs))
	}
	if res.DroppedByRightsCount != 2 {
		t.Fatalf("DroppedByRightsCount = %d, want 2 (denied + expired)", res.DroppedByRightsCount)
	}
}

// ── fail-closed envelopes ─────────────────────────────────────

func TestDiscoveryWorker_EmptyQueryFailsClosedWithErrInvalidPhrase(t *testing.T) {
	t.Parallel()

	repo := newFakeCandidateRepoDiscovery()
	fo := &fakeSearchFanOutDiscovery{}
	w := NewDefaultDiscoveryWorker(repo, fo, NoopLogger(), nil)

	_, err := w.Discover(context.Background(), DiscoveryRequest{
		Query: "   ", Provider: "youtube", // empty / whitespace
	})
	if err == nil {
		t.Fatalf("Discover accepted whitespace-only query")
	}
	if !errors.Is(err, ErrInvalidPhrase) {
		t.Fatalf("error = %v, want wrapped ErrInvalidPhrase", err)
	}
}

func TestDiscoveryWorker_EmptyProviderFailsClosedWithErrInvalidPhrase(t *testing.T) {
	t.Parallel()

	repo := newFakeCandidateRepoDiscovery()
	fo := &fakeSearchFanOutDiscovery{}
	w := NewDefaultDiscoveryWorker(repo, fo, NoopLogger(), nil)

	_, err := w.Discover(context.Background(), DiscoveryRequest{
		Query: "Maya", Provider: "", // empty
	})
	if err == nil {
		t.Fatalf("Discover accepted empty provider")
	}
	if !errors.Is(err, ErrInvalidPhrase) {
		t.Fatalf("error = %v, want wrapped ErrInvalidPhrase", err)
	}
}

// ── search-fanout error surfaces as wrapped ErrSemanticBackendFailed ─

func TestDiscoveryWorker_SearchFanOutErrorSurfacesAsErrSemanticBackendFailed(t *testing.T) {
	t.Parallel()

	repo := newFakeCandidateRepoDiscovery()
	fo := &fakeSearchFanOutDiscovery{
		err: errors.New("upstream timeout"),
	}
	w := NewDefaultDiscoveryWorker(repo, fo, NoopLogger(), nil)
	_, err := w.Discover(context.Background(), DiscoveryRequest{
		Query: "Maya", Provider: "artlist",
	})
	if err == nil {
		t.Fatalf("Discover accepted a fanout error envelope")
	}
	if !errors.Is(err, ErrSemanticBackendFailed) {
		t.Fatalf("error = %v, want wrapped ErrSemanticBackendFailed (canonical envelope)", err)
	}
}

// Compile-time pin.
var _ DiscoveryWorker = (*defaultDiscoveryWorker)(nil)
