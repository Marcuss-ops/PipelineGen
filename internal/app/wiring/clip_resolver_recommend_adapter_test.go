// Package app — clip_resolver_recommend_adapter_test.go: TDD coverage
// for the new Recommend-shaped adapter. The tests are organized into
// 4 groups: constructor safety, dispatch translation, scoring
// correctness, and error propagation. godlike/07 discipline: every
// godlike/07 invariant (nil receiver → sentinel, nil canonical →
// fail-closed, YouTube fan-out, empty-input short-circuit,
// MinScore filter, sort order) is locked by at least one test.
package wiring

import (
	"context"
	"errors"
	"strings"
	"testing"

	artlist "github.com/Marcuss-ops/PipelineGen/internal/api/assets/artlist"
	"github.com/Marcuss-ops/PipelineGen/internal/app/wiring"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
)

// ── Test fixtures ──────────────────────────────────────────────────

// stubCanonicalResolver is a hand-rolled stub satisfying
// ports.ClipResolver. It captures the input refs (for dispatch
// verification) and returns canned resolved/unresolved slices
// (for scoring verification). The narrow port surface here is
// intentional — tests use the same hand-rolled stub pattern as
// the canonical clip_resolver_test.go (NewClipResolverForTest
// seam); the test-only `clipResolverPortReadOnly` interface
// declared in scripts/adapters/clip_resolver.go is the
// canonical precedent.
type stubCanonicalResolver struct {
	lastRefs []ports.ClipReference

	// cannedResult is returned by Resolve. nil cannedResult +
	// cannedErr is a no-inputs / no-error shape.
	cannedResult *ports.ClipResolutionResult
	cannedErr    error
}

func (s *stubCanonicalResolver) Resolve(_ context.Context, refs []ports.ClipReference) (*ports.ClipResolutionResult, error) {
	s.lastRefs = append([]ports.ClipReference(nil), refs...) // capture a copy
	return s.cannedResult, s.cannedErr
}

// makeEvidence builds a ClipEvidence with the specified text fields
// (other fields left empty for compactness). Used to construct
// canned Resolved slices for scoring tests.
func makeEvidence(assetID, name, description, transcript string, tags []string) ports.ClipEvidence {
	ev := ports.ClipEvidence{
		AssetID:        assetID,
		ReferenceValue: assetID,
		ReferenceType:  ports.RefTypeMediaAssetID,
	}
	if name != "" {
		ev.Name = name
	}
	if description != "" {
		ev.Description = description
	}
	if transcript != "" {
		ev.TranscriptExcerpt = transcript
	}
	if len(tags) > 0 {
		ev.Tags = append(ev.Tags, tags...)
	}
	ev.DriveLink = "https://drive.google.com/file/d/" + assetID
	return ev
}

// ── Constructor safety ───────────────────────────────────────────

// TestNewClipResolverRecommendAdapter_NilCanonical verifies the
// godlike/07 fail-closed fast path: nil canonical returns nil.
// The composition root uses this to leave ArtlistBundle.ClipResolver
// as nil, preserving the prior 503 behavior when the canonical
// resolver is unavailable.
func TestNewClipResolverRecommendAdapter_NilCanonical(t *testing.T) {
	a := wiring.NewClipResolverRecommendAdapter(nil, nil)
	if a != nil {
		t.Fatalf("wiring.NewClipResolverRecommendAdapter(nil) = %v, want nil (godlike/07 fail-closed fast path)", a)
	}
}

// TestNewClipResolverRecommendAdapter_NonNil verifies the
// happy-path constructor returns a non-nil adapter.
func TestNewClipResolverRecommendAdapter_NonNil(t *testing.T) {
	canonical := &stubCanonicalResolver{}
	a := wiring.NewClipResolverRecommendAdapter(canonical, nil)
	if a == nil {
		t.Fatalf("wiring.NewClipResolverRecommendAdapter(stub) = nil, want non-nil adapter")
	}
}

// TestRecommend_NilReceiver verifies the godlike/07 sentinel
// is returned when the adapter receiver is nil. The handler-side
// nil-tolerance (`if h.clipResolver == nil { 500 }`) catches this
// in production, but the sentinel is the typed-error contract for
// any future programmatic caller.
func TestRecommend_NilReceiver(t *testing.T) {
	var a *wiring.ClipResolverRecommendAdapter // nil receiver
	resp, err := a.Recommend(context.Background(), &artlist.ClipResolverRecommendRequest{
		Topic: "anything",
	})
	if !errors.Is(err, wiring.ErrRecommendAdapterNotConfigured) {
		t.Fatalf("err = %v, want wiring.ErrRecommendAdapterNotConfigured", err)
	}
	if resp != nil {
		t.Fatalf("resp = %v, want nil", resp)
	}
}

// TestRecommend_NilCanonical verifies the same sentinel for the
// case where the receiver is non-nil but its canonical field is
// nil. This is defensive (the constructor guards against this) but
// locked here for type-discipline.
func TestRecommend_NilCanonical(t *testing.T) {
	a := &wiring.ClipResolverRecommendAdapter{Canonical: nil, Log: nil}
	resp, err := a.Recommend(context.Background(), &artlist.ClipResolverRecommendRequest{
		Topic: "anything",
	})
	if !errors.Is(err, wiring.ErrRecommendAdapterNotConfigured) {
		t.Fatalf("err = %v, want wiring.ErrRecommendAdapterNotConfigured", err)
	}
	if resp != nil {
		t.Fatalf("resp = %v, want nil", resp)
	}
}

// ── Dispatch translation (SegmentID → canonical ClipReference) ───

// TestRecommend_SegmentIDYouTubeShapeFansOut verifies that a
// YouTube-shaped SegmentID (`yt_<videoID>_<seg>_<n>`) is
// translated to RefTypeYouTubeVideoID with the 11-char videoID
// extracted. The canonical resolver then handles the
// `LIKE yt_<videoID>_%` fan-out, returning ALL segments of
// that video. This is the new behavior that the previous
// deprecation (PR-ARTLIST-SYNCSERVICE) explicitly punted on.
func TestRecommend_SegmentIDYouTubeShapeFansOut(t *testing.T) {
	stub := &stubCanonicalResolver{
		cannedResult: &ports.ClipResolutionResult{
			Resolved:   []ports.ClipEvidence{},
			Unresolved: []ports.UnresolvedReference{},
		},
	}
	a := wiring.NewClipResolverRecommendAdapter(stub, nil)
	_, err := a.Recommend(context.Background(), &artlist.ClipResolverRecommendRequest{
		SegmentID: "yt_ABCDEFGHIJK_seg3_7",
		Topic:     "anything", // non-empty so the dispatch is exercised
	})
	if err != nil {
		t.Fatalf("Recommend: %v", err)
	}
	if len(stub.lastRefs) != 1 {
		t.Fatalf("canonical refs = %d, want 1 (YouTube fan-out via RefTypeYouTubeVideoID)", len(stub.lastRefs))
	}
	if stub.lastRefs[0].Type != ports.RefTypeYouTubeVideoID {
		t.Fatalf("ref.Type = %q, want %q (RefTypeYouTubeVideoID fan-out)",
			stub.lastRefs[0].Type, ports.RefTypeYouTubeVideoID)
	}
	if stub.lastRefs[0].Value != "ABCDEFGHIJK" {
		t.Fatalf("ref.Value = %q, want %q (extracted videoID)", stub.lastRefs[0].Value, "ABCDEFGHIJK")
	}
}

// TestRecommend_SegmentIDNonYouTubeShapeUsesMediaAssetID verifies
// that a non-YouTube-shaped SegmentID is dispatched as
// RefTypeMediaAssetID. The canonical resolver does an exact-match
// lookup on media_assets.id.
func TestRecommend_SegmentIDNonYouTubeShapeUsesMediaAssetID(t *testing.T) {
	stub := &stubCanonicalResolver{
		cannedResult: &ports.ClipResolutionResult{
			Resolved:   []ports.ClipEvidence{},
			Unresolved: []ports.UnresolvedReference{},
		},
	}
	a := wiring.NewClipResolverRecommendAdapter(stub, nil)
	_, err := a.Recommend(context.Background(), &artlist.ClipResolverRecommendRequest{
		SegmentID: "some-uuid-or-other-id",
		Topic:     "anything",
	})
	if err != nil {
		t.Fatalf("Recommend: %v", err)
	}
	if len(stub.lastRefs) != 1 {
		t.Fatalf("canonical refs = %d, want 1", len(stub.lastRefs))
	}
	if stub.lastRefs[0].Type != ports.RefTypeMediaAssetID {
		t.Fatalf("ref.Type = %q, want %q",
			stub.lastRefs[0].Type, ports.RefTypeMediaAssetID)
	}
	if stub.lastRefs[0].Value != "some-uuid-or-other-id" {
		t.Fatalf("ref.Value = %q, want %q",
			stub.lastRefs[0].Value, "some-uuid-or-other-id")
	}
}

// TestRecommend_QueriesAreNotTranslatedToReferences verifies the
// godlike/07 violation flagged by the thinker review: free-form
// queries MUST NOT be translated to RefTypeExternalProviderID
// values (e.g. `artlist_query::search term`) because the canonical
// port's contract forbids auto-classification from Value. Queries
// are used ONLY for scoring (text haystack), never for resolution.
func TestRecommend_QueriesAreNotTranslatedToReferences(t *testing.T) {
	stub := &stubCanonicalResolver{
		cannedResult: &ports.ClipResolutionResult{
			Resolved:   []ports.ClipEvidence{},
			Unresolved: []ports.UnresolvedReference{},
		},
	}
	a := wiring.NewClipResolverRecommendAdapter(stub, nil)
	_, err := a.Recommend(context.Background(), &artlist.ClipResolverRecommendRequest{
		SegmentID: "abc-123", // non-YouTube, so dispatches as MediaAssetID
		Topic:     "test topic",
		Queries:   []string{"query 1", "query 2", "query 3"},
	})
	if err != nil {
		t.Fatalf("Recommend: %v", err)
	}
	// Only the SegmentID should become a canonical reference; the
	// 3 Queries must NOT become references (they are scoring-only).
	if len(stub.lastRefs) != 1 {
		t.Fatalf("canonical refs = %d, want 1 (SegmentID only; Queries must be scoring-only)",
			len(stub.lastRefs))
	}
}

// ── Input short-circuits ──────────────────────────────────────────

// TestRecommend_EmptyRequestReturnsEmptyResults verifies the
// godlike/07 no-fake-availability invariant: when there is no
// input (no SegmentID, no Topic, no Queries), the adapter
// returns 200 with empty Results rather than fabricating a
// "best of nothing" hit. This is the right semantics for
// "recommend clips for an empty query" — there are no
// recommendations to make.
func TestRecommend_EmptyRequestReturnsEmptyResults(t *testing.T) {
	stub := &stubCanonicalResolver{}
	a := wiring.NewClipResolverRecommendAdapter(stub, nil)
	resp, err := a.Recommend(context.Background(), &artlist.ClipResolverRecommendRequest{})
	if err != nil {
		t.Fatalf("Recommend: %v", err)
	}
	if resp == nil {
		t.Fatalf("resp = nil, want non-nil response with empty Results")
	}
	if resp.Results == nil {
		t.Fatalf("resp.Results = nil, want empty slice (not nil — JSON marshals nil as null)")
	}
	if len(resp.Results) != 0 {
		t.Fatalf("resp.Results len = %d, want 0", len(resp.Results))
	}
	// The canonical resolver should NOT have been called (no
	// dispatch when there are no inputs).
	if len(stub.lastRefs) != 0 {
		t.Fatalf("canonical refs = %d, want 0 (empty inputs should skip dispatch)", len(stub.lastRefs))
	}
}

// TestRecommend_NilRequestReturnsError verifies the typed-error
// contract for nil requests. The handler-side bind layer prevents
// this in production (gin's BindJSON returns false on nil), but
// the adapter must defend against direct programmatic callers.
func TestRecommend_NilRequestReturnsError(t *testing.T) {
	stub := &stubCanonicalResolver{}
	a := wiring.NewClipResolverRecommendAdapter(stub, nil)
	resp, err := a.Recommend(context.Background(), nil)
	if err == nil {
		t.Fatalf("err = nil, want error for nil request")
	}
	if resp != nil {
		t.Fatalf("resp = %v, want nil", resp)
	}
}

// TestRecommend_OnlyQueryNoSegmentIDReturnsEmptyResults verifies
// that a query-only request (no SegmentID, just Topic) does NOT
// dispatch to the canonical resolver (no candidate pool) and
// returns empty results. This is the right semantics for
// "recommend clips for a query without any anchor" — without
// a SegmentID or a real DB-side candidate generator, the
// scoring layer has nothing to score.
func TestRecommend_OnlyQueryNoSegmentIDReturnsEmptyResults(t *testing.T) {
	stub := &stubCanonicalResolver{}
	a := wiring.NewClipResolverRecommendAdapter(stub, nil)
	resp, err := a.Recommend(context.Background(), &artlist.ClipResolverRecommendRequest{
		Topic: "test topic",
	})
	if err != nil {
		t.Fatalf("Recommend: %v", err)
	}
	if len(resp.Results) != 0 {
		t.Fatalf("resp.Results len = %d, want 0", len(resp.Results))
	}
	if len(stub.lastRefs) != 0 {
		t.Fatalf("canonical refs = %d, want 0 (no SegmentID = no dispatch)", len(stub.lastRefs))
	}
}

// ── Scoring correctness ───────────────────────────────────────────

// TestRecommend_SortByScoreDescending verifies that the response
// Results are sorted by Score descending (highest first). Two
// assets with different Jaccard overlaps against the query must
// appear in descending-score order. This pins the canonical
// sort.SliceStable contract.
func TestRecommend_SortByScoreDescending(t *testing.T) {
	// Query: "mountain sunrise"
	// Asset A: "Mountain Sunrise Photography" — high overlap
	// Asset B: "Beach Sunset" — low overlap
	// Asset C: "Mountain Trail Hike" — medium overlap
	stub := &stubCanonicalResolver{
		cannedResult: &ports.ClipResolutionResult{
			Resolved: []ports.ClipEvidence{
				makeEvidence("asset-B", "Beach Sunset Photography", "", "", nil),
				makeEvidence("asset-A", "Mountain Sunrise Photography", "", "", nil),
				makeEvidence("asset-C", "Mountain Trail Hike", "", "", nil),
			},
		},
	}
	a := wiring.NewClipResolverRecommendAdapter(stub, nil)
	resp, err := a.Recommend(context.Background(), &artlist.ClipResolverRecommendRequest{
		SegmentID: "anything",
		Topic:     "mountain sunrise",
	})
	if err != nil {
		t.Fatalf("Recommend: %v", err)
	}
	if len(resp.Results) != 3 {
		t.Fatalf("resp.Results len = %d, want 3", len(resp.Results))
	}
	if resp.Results[0].ClipID != "asset-A" {
		t.Fatalf("first result ClipID = %q, want %q (highest Jaccard overlap)",
			resp.Results[0].ClipID, "asset-A")
	}
	if resp.Results[0].Score <= resp.Results[1].Score {
		t.Fatalf("first result score %f not > second %f (desc-sort broken)",
			resp.Results[0].Score, resp.Results[1].Score)
	}
	if resp.Results[1].Score <= resp.Results[2].Score {
		t.Fatalf("second result score %f not > third %f (desc-sort broken)",
			resp.Results[1].Score, resp.Results[2].Score)
	}
}

// TestRecommend_MinScoreFilterWorks verifies that results with
// score below MinScore are filtered out. The asset with zero
// overlap against the query must be excluded.
func TestRecommend_MinScoreFilterWorks(t *testing.T) {
	// Query: "kubernetes"
	// Asset A: "Kubernetes tutorial" — perfect overlap
	// Asset B: "Python snakes" — zero overlap (different domain)
	stub := &stubCanonicalResolver{
		cannedResult: &ports.ClipResolutionResult{
			Resolved: []ports.ClipEvidence{
				makeEvidence("asset-B", "Python snakes in the jungle", "", "", nil),
				makeEvidence("asset-A", "Kubernetes tutorial for beginners", "", "", nil),
			},
		},
	}
	a := wiring.NewClipResolverRecommendAdapter(stub, nil)
	// MinScore=0.5 should filter out asset-B (which would have
	// 0 overlap) but include asset-A (which has 1.0 overlap on
	// the Name field).
	resp, err := a.Recommend(context.Background(), &artlist.ClipResolverRecommendRequest{
		SegmentID: "anything",
		Topic:     "kubernetes",
		MinScore:  0.5,
	})
	if err != nil {
		t.Fatalf("Recommend: %v", err)
	}
	if len(resp.Results) != 1 {
		t.Fatalf("resp.Results len = %d, want 1 (asset-B should be filtered)", len(resp.Results))
	}
	if resp.Results[0].ClipID != "asset-A" {
		t.Fatalf("first result ClipID = %q, want asset-A", resp.Results[0].ClipID)
	}
}

// TestRecommend_FieldWeightedScoringIsMonotonic verifies that
// the field-weighted scoring is monotonic: an asset with more
// matching fields scores >= an asset with fewer matching fields
// (given the same per-field score). This pins the
// weighted-average normalization.
func TestRecommend_FieldWeightedScoringIsMonotonic(t *testing.T) {
	// Query: "kubernetes deployment"
	// Asset A: matches in Name only (weight 0.30)
	// Asset B: matches in Name + Description (weights 0.30 + 0.20)
	// Both have non-empty Name + Description fields, but only A
	// matches in Name. B's Description also matches.
	stub := &stubCanonicalResolver{
		cannedResult: &ports.ClipResolutionResult{
			Resolved: []ports.ClipEvidence{
				// Asset A: Name matches, Description does not
				makeEvidence("asset-A", "Kubernetes deployment guide",
					"unrelated content about gardening", "", nil),
				// Asset B: Name + Description both match
				makeEvidence("asset-B", "Kubernetes deployment guide",
					"All about kubernetes deployment strategies", "", nil),
			},
		},
	}
	a := wiring.NewClipResolverRecommendAdapter(stub, nil)
	resp, err := a.Recommend(context.Background(), &artlist.ClipResolverRecommendRequest{
		SegmentID: "anything",
		Topic:     "kubernetes deployment",
	})
	if err != nil {
		t.Fatalf("Recommend: %v", err)
	}
	if len(resp.Results) != 2 {
		t.Fatalf("resp.Results len = %d, want 2", len(resp.Results))
	}
	// Find each asset by ClipID (order may differ from input).
	var scoreA, scoreB float64
	for _, r := range resp.Results {
		switch r.ClipID {
		case "asset-A":
			scoreA = r.Score
		case "asset-B":
			scoreB = r.Score
		}
	}
	if scoreB <= scoreA {
		t.Fatalf("asset-B score %f (Name+Description match) should exceed asset-A score %f (Name only)",
			scoreB, scoreA)
	}
}

// TestRecommend_ResultFieldsMatchEvidence verifies the response
// DTO mapping: ClipID comes from AssetID, DriveLink comes from
// the evidence's DriveLink. This pins the canonical
// DTO-translation contract.
func TestRecommend_ResultFieldsMatchEvidence(t *testing.T) {
	stub := &stubCanonicalResolver{
		cannedResult: &ports.ClipResolutionResult{
			Resolved: []ports.ClipEvidence{
				{
					AssetID:   "asset-xyz",
					DriveLink: "https://drive.google.com/file/d/asset-xyz/view",
					Name:      "matching-name",
				},
			},
		},
	}
	a := wiring.NewClipResolverRecommendAdapter(stub, nil)
	resp, err := a.Recommend(context.Background(), &artlist.ClipResolverRecommendRequest{
		SegmentID: "asset-xyz",
		Topic:     "matching",
	})
	if err != nil {
		t.Fatalf("Recommend: %v", err)
	}
	if len(resp.Results) != 1 {
		t.Fatalf("resp.Results len = %d, want 1", len(resp.Results))
	}
	r := resp.Results[0]
	if r.ClipID != "asset-xyz" {
		t.Fatalf("ClipID = %q, want %q (from AssetID)", r.ClipID, "asset-xyz")
	}
	if r.DriveLink != "https://drive.google.com/file/d/asset-xyz/view" {
		t.Fatalf("DriveLink = %q, want %q (from evidence.DriveLink)",
			r.DriveLink, "https://drive.google.com/file/d/asset-xyz/view")
	}
	if r.Score <= 0 {
		t.Fatalf("Score = %f, want > 0 (Jaccard overlap on Name \"matching-name\")", r.Score)
	}
}

// ── Error propagation ─────────────────────────────────────────────

// TestRecommend_CanonicalErrorPropagates verifies that a DB
// error from the canonical resolver propagates to the handler
// (which returns 500). The adapter does not swallow the error;
// per-reference Unresolved entries are logged inside the
// canonical resolver and are not surfaced in the response
// (the handler returns 500 with the wrapped error message).
func TestRecommend_CanonicalErrorPropagates(t *testing.T) {
	dbErr := errors.New("simulated DB error")
	stub := &stubCanonicalResolver{
		cannedErr: dbErr,
	}
	a := wiring.NewClipResolverRecommendAdapter(stub, nil)
	resp, err := a.Recommend(context.Background(), &artlist.ClipResolverRecommendRequest{
		SegmentID: "asset-1",
		Topic:     "anything",
	})
	if err == nil {
		t.Fatalf("err = nil, want wrapped DB error")
	}
	if !errors.Is(err, dbErr) {
		t.Fatalf("err = %v, want to wrap %v (errors.Is)", err, dbErr)
	}
	if !strings.Contains(err.Error(), "canonical resolve failed") {
		t.Fatalf("err = %v, want message containing 'canonical resolve failed'", err)
	}
	if resp != nil {
		t.Fatalf("resp = %v, want nil on error", resp)
	}
}

// TestRecommend_EmptyResolvedReturnsEmptyResults verifies the
// no-candidates short-circuit: when the canonical resolver
// returns 0 resolved assets (e.g. all references were
// Unresolved with not_found), the adapter returns empty
// results without invoking the scoring layer. Saves overhead
// + produces the right semantics.
func TestRecommend_EmptyResolvedReturnsEmptyResults(t *testing.T) {
	stub := &stubCanonicalResolver{
		cannedResult: &ports.ClipResolutionResult{
			Resolved: []ports.ClipEvidence{},
			Unresolved: []ports.UnresolvedReference{
				{Reference: ports.ClipReference{Type: ports.RefTypeMediaAssetID, Value: "missing"}, Reason: "not_found"},
			},
		},
	}
	a := wiring.NewClipResolverRecommendAdapter(stub, nil)
	resp, err := a.Recommend(context.Background(), &artlist.ClipResolverRecommendRequest{
		SegmentID: "missing",
		Topic:     "anything",
	})
	if err != nil {
		t.Fatalf("Recommend: %v", err)
	}
	if len(resp.Results) != 0 {
		t.Fatalf("resp.Results len = %d, want 0 (no candidates)", len(resp.Results))
	}
}

// TestRecommend_StopwordOnlyQueryReturnsEmptyResults verifies
// that a query containing only stopwords / sub-3-char tokens
// (which similarity.TokenSet filters out) returns empty
// results rather than producing 1.0 scores for every
// candidate. Per godlike/07 no-fake-availability: a query
// with no scorable tokens cannot match anything.
func TestRecommend_StopwordOnlyQueryReturnsEmptyResults(t *testing.T) {
	stub := &stubCanonicalResolver{
		cannedResult: &ports.ClipResolutionResult{
			Resolved: []ports.ClipEvidence{
				makeEvidence("asset-1", "Kubernetes tutorial", "", "", nil),
				makeEvidence("asset-2", "Python snakes", "", "", nil),
			},
		},
	}
	a := wiring.NewClipResolverRecommendAdapter(stub, nil)
	// "a of to" — all 1-2 char tokens, filtered out by TokenSet.
	resp, err := a.Recommend(context.Background(), &artlist.ClipResolverRecommendRequest{
		SegmentID: "anything",
		Topic:     "a of to",
	})
	if err != nil {
		t.Fatalf("Recommend: %v", err)
	}
	if len(resp.Results) != 0 {
		t.Fatalf("resp.Results len = %d, want 0 (stopword-only query has no scorable tokens)",
			len(resp.Results))
	}
}
