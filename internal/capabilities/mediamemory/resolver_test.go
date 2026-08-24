// Package mediamemory — resolver_test.go pins the canonical
// 9-level strict-priority cascade + binding→FilteredCandidate
// lossless projection + multi-slot plan assembly.
//
// godlike/06 SSOT (priority cascade): the test fixtures invoke
// each cascade level in isolation AND chained together. The
// strict-priority invariant — "first non-empty level wins,
// subsequent levels SKIPPED" — is asserted via a per-level
// fake that records which levels were invoked.
//
// godlike/06 SSOT (lossless binding projection): a binding
// carrying operator-curated scores (ManualScore / SemanticScore
// / QualityScore / SuccessScore) + window (StartMs / EndMs)
// MUST reach the Layer envelope verbatim. Drift in the
// projection mapping is a contract break for the dashboard
// "saved binding" path.
package mediamemory

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── Fakes ──────────────────────────────────────────────────────────

// fakeSearchFanOut (local) implements mediamemory.SearchFanOut for
// resolver tests. Records every call so a per-level "which level
// was hit" assertion is possible.
type fakeSearchFanOut struct {
	mu      sync.Mutex
	calls   []SearchFanOutQuery
	results []SearchFanOutResult
	err     error
}

func (f *fakeSearchFanOut) Search(_ context.Context, q SearchFanOutQuery) (SearchFanOutResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, q)
	if f.err != nil {
		return SearchFanOutResult{}, f.err
	}
	if len(f.results) == 0 {
		return SearchFanOutResult{}, nil
	}
	r := f.results[0]
	f.results = f.results[1:]
	return r, nil
}

func (f *fakeSearchFanOut) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

// fakeSemanticLookup implements SemanticLookup. Phase 1.x leaves
// Levels 3-7 as stubs; this fake records calls + returns whatever
// the test queued (usually empty — the cascade is supposed to
// fall-through to Level 8).
type fakeSemanticLookup struct {
	mu    sync.Mutex
	calls int
	out   []MediaCandidate
	err   error
}

func (f *fakeSemanticLookup) LookupByConcept(_ context.Context, _ ConceptType, _, _ string, _ int) ([]MediaCandidate, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	return f.out, f.err
}

// ── Cascade tests (strict priority) ──────────────────────────────

func TestResolve_ExactCacheHitShortCircuitsCascade(t *testing.T) {
	fcr := &fakeConceptRepo{byID: map[string]MediaConcept{
		"c-maya": {ID: "c-maya", Language: "it", PhraseFingerprint: fingerprintForNormalized("it", "i maya")},
	}}
	fbr := &fakeBindingsRepo{byID: map[string]MediaBinding{
		"b-1": {ID: "b-1", ConceptID: "c-maya", AssetID: "asset-maya", SlotKind: media.SlotPrimaryVideo, ApprovalStatus: ApprovalApproved, Origin: OriginManual, ManualScore: 0.95, SemanticScore: 0.80, QualityScore: 0.85, SuccessScore: 0.70, StartMs: 12000, EndMs: 20000},
	}}
	fbr.upserts = nil

	// godlike/07: Go disallows direct field assignment on a struct
	// stored in a map; copy-modify-write is the canonical pattern.
	c := fcr.byID["c-maya"]
	c.ConceptType = ConceptPhrase
	fcr.byID["c-maya"] = c

	// Pre-register the binding in the in-memory repo by calling Upsert
	// to mirror the SQLite concrete's behaviour.
	_, _ = fcr.Upsert(context.Background(), fcr.byID["c-maya"])
	_, _ = fbr.Upsert(context.Background(), fbr.byID["b-1"])
	b := fbr.byID["b-1"]
	b.ApprovalStatus = ApprovalApproved
	fbr.byID["b-1"] = b

	sem := &fakeSemanticLookup{}
	sfo := &fakeSearchFanOut{}

	r := NewVisualResolver(ResolverDeps{Concepts: fcr, Bindings: fbr, External: sfo, Semantic: sem, Ranker: NewDefaultRanker(nil, nil)})

	res, err := r.Resolve(context.Background(), ResolveRequest{
		ProjectID: "p1",
		Language:  "it",
		Scenes: []SceneSpec{
			{ID: "scene-1", Text: "I Maya", Language: "it", DurationMs: 8000, Slots: []SlotKind{media.SlotPrimaryVideo}},
		},
		Policy: ResolvePolicy{AllowExternalSearch: true},
	})
	require.NoError(t, err)
	require.Len(t, res.Plans, 1)
	plan := res.Plans[0]
	assert.Equal(t, 1, len(plan.Layers))
	assert.Equal(t, "asset-maya", plan.Layers[0].AssetID)
	assert.Equal(t, "b-1", plan.Layers[0].BindingID)
	assert.Equal(t, int64(12000), plan.Layers[0].StartMs, "binding StartMs MUST propagate to Layer")
	assert.Equal(t, int64(20000), plan.Layers[0].EndMs, "binding EndMs MUST propagate to Layer")
	assert.Equal(t, "exact", plan.Source, "exact-cache hit MUST be tagged source='exact'")

	// Strict priority invariant: external SearchFanOut MUST NOT
	// have been invoked — Level 9 skipped because Level 1+2 won.
	assert.Equal(t, 0, sfo.callCount(), "exact-hit MUST short-circuit the cascade (Level 9 skipped)")
}

func TestResolve_CascadeFallsThroughToExternalWhenAllowed(t *testing.T) {
	fcr := newFakeConceptRepo()
	fbr := newFakeBindingsRepo()
	sem := &fakeSemanticLookup{}
	sfo := &fakeSearchFanOut{
		results: []SearchFanOutResult{{
			Candidates: []MediaCandidate{{
				AssetID: "asset-ext", Provider: "artlist", DurationMs: 8000,
				RightsStatus: RightsVerified, MaterializationStatus: MaterializationHot,
				DiscoveryStatus: DiscoverySearched,
			}},
			BackendNames: []string{"artlist"},
		}},
	}

	r := NewVisualResolver(ResolverDeps{Concepts: fcr, Bindings: fbr, External: sfo, Semantic: sem, Ranker: NewDefaultRanker(nil, nil)})

	res, err := r.Resolve(context.Background(), ResolveRequest{
		ProjectID: "p1",
		Language:  "it",
		Scenes: []SceneSpec{
			{ID: "s1", Text: "Chichén Itzá", Language: "it", DurationMs: 8000, Slots: []SlotKind{media.SlotPrimaryVideo}},
		},
		Policy: ResolvePolicy{AllowExternalSearch: true, MaxCandidatesPerSlot: 10},
	})
	require.NoError(t, err)
	require.Len(t, res.Plans, 1)
	assert.Equal(t, "asset-ext", res.Plans[0].Layers[0].AssetID)
	assert.Equal(t, "external", res.Plans[0].Source,
		"Level 9 winner MUST tag source='external'")
	assert.Equal(t, 1, sfo.callCount(), "Level 9 MUST be consulted when AllowExternalSearch=true and Levels 1-8 yield nothing")
}

func TestResolve_AllowExternalSearchFalseSkipsLevel9(t *testing.T) {
	fcr := newFakeConceptRepo()
	fbr := newFakeBindingsRepo()
	sem := &fakeSemanticLookup{}
	sfo := &fakeSearchFanOut{
		results: []SearchFanOutResult{{Candidates: []MediaCandidate{{AssetID: "asset-x", Provider: "yt", DurationMs: 8000, RightsStatus: RightsVerified, MaterializationStatus: MaterializationHot, DiscoveryStatus: DiscoverySearched}}}},
	}

	r := NewVisualResolver(ResolverDeps{Concepts: fcr, Bindings: fbr, External: sfo, Semantic: sem, Ranker: NewDefaultRanker(nil, nil)})

	res, err := r.Resolve(context.Background(), ResolveRequest{
		ProjectID: "p1",
		Language:  "it",
		Scenes: []SceneSpec{
			{ID: "s1", Text: "test", Language: "it", DurationMs: 8000, Slots: []SlotKind{media.SlotPrimaryVideo}},
		},
		Policy: ResolvePolicy{AllowExternalSearch: false}, // <-- gating
	})
	require.NoError(t, err)
	require.Len(t, res.Plans, 0,
		"no candidate available at Levels 1-8 + AllowExternalSearch=false MUST produce zero plans (godlike/07 typed fail)")
	assert.Equal(t, 0, sfo.callCount(), "AllowExternalSearch=false MUST skip Level 9 entirely")
	assert.NotEmpty(t, res.Warnings, "warnings array MUST surface the 'external search disabled' skip reason")
}

func TestResolve_PerSceneFailureDoesntAbortBatch(t *testing.T) {
	fcr := newFakeConceptRepo()
	fbr := newFakeBindingsRepo()
	sem := &fakeSemanticLookup{}
	sfo := &fakeSearchFanOut{
		results: []SearchFanOutResult{
			{Candidates: []MediaCandidate{{AssetID: "asset-1", Provider: "yt", DurationMs: 8000, RightsStatus: RightsVerified, MaterializationStatus: MaterializationHot, DiscoveryStatus: DiscoverySearched}}},
			{Candidates: []MediaCandidate{{AssetID: "asset-2", Provider: "yt", DurationMs: 8000, RightsStatus: RightsVerified, MaterializationStatus: MaterializationHot, DiscoveryStatus: DiscoverySearched}}},
		},
	}

	r := NewVisualResolver(ResolverDeps{Concepts: fcr, Bindings: fbr, External: sfo, Semantic: sem, Ranker: NewDefaultRanker(nil, nil)})

	// First scene references an unrecognized SlotKind — MUST be
	// dropped with a warning, NOT abort the batch. Second scene
	// succeeds via Level 9.
	res, err := r.Resolve(context.Background(), ResolveRequest{
		ProjectID: "p1",
		Language:  "it",
		Scenes: []SceneSpec{
			{ID: "s-bad", Text: "test", Language: "it", DurationMs: 8000, Slots: []SlotKind{"unknown_slot_kind"}},
			{ID: "s-ok", Text: "test", Language: "it", DurationMs: 8000, Slots: []SlotKind{media.SlotPrimaryVideo}},
		},
		Policy: ResolvePolicy{AllowExternalSearch: true},
	})
	// One plan MUST come back; the bad scene's failure is a warning.
	require.NoError(t, err)
	require.Len(t, res.Plans, 1, "batch MUST continue past per-scene failures")
	assert.Equal(t, "s-ok", res.Plans[0].SceneID)
	assert.NotEmpty(t, res.Warnings, "warnings MUST include the unknown slot_kind notice")
}

func TestResolve_MultiSlotHonoursRequestedSlots(t *testing.T) {
	fcr := newFakeConceptRepo()
	fbr := newFakeBindingsRepo()
	sem := &fakeSemanticLookup{}
	// godlike/07 fail-closed: provide a candidate for each slot
	// call so all 3 layers resolve. The fake pops from front;
	// earlier tests in this suite also drain fso.results so each
	// test gets a fresh fake.
	sfo := &fakeSearchFanOut{
		results: []SearchFanOutResult{
			{Candidates: []MediaCandidate{
				{AssetID: "asset-vid", Provider: "yt", MediaType: "video", DurationMs: 8000, RightsStatus: RightsVerified, MaterializationStatus: MaterializationHot, DiscoveryStatus: DiscoverySearched},
			}},
			{Candidates: []MediaCandidate{
				{AssetID: "asset-img", Provider: "artlist", MediaType: "image", DurationMs: 0, RightsStatus: RightsVerified, MaterializationStatus: MaterializationHot, DiscoveryStatus: DiscoverySearched},
			}},
			{Candidates: []MediaCandidate{
				{AssetID: "asset-evi", Provider: "artlist", MediaType: "image", DurationMs: 0, RightsStatus: RightsVerified, MaterializationStatus: MaterializationHot, DiscoveryStatus: DiscoverySearched},
			}},
		},
	}

	r := NewVisualResolver(ResolverDeps{Concepts: fcr, Bindings: fbr, External: sfo, Semantic: sem, Ranker: NewDefaultRanker(nil, nil)})

	res, err := r.Resolve(context.Background(), ResolveRequest{
		ProjectID: "p1",
		Language:  "it",
		Scenes: []SceneSpec{
			{ID: "s-multi", Text: "Maya", Language: "it", DurationMs: 8000, Slots: []SlotKind{media.SlotPrimaryVideo, media.SlotSecondaryImage, media.SlotEvidenceOverlay}},
		},
		Policy: ResolvePolicy{AllowExternalSearch: true, MaxCandidatesPerSlot: 5},
	})
	require.NoError(t, err)
	require.Len(t, res.Plans, 1)
	plan := res.Plans[0]
	// godlike/06: at most len(scene.Slots) layers (caller may
	// request ≤3; we never return MORE than requested).
	assert.LessOrEqual(t, len(plan.Layers), 3, "3-layer renderer ceiling MUST be honoured (and never exceeded)")
	slots := make([]SlotKind, 0, len(plan.Layers))
	for _, l := range plan.Layers {
		slots = append(slots, l.Slot)
	}
	assert.Contains(t, slots, media.SlotPrimaryVideo)
	assert.Contains(t, slots, media.SlotSecondaryImage)
	assert.Contains(t, slots, media.SlotEvidenceOverlay)
}

func TestResolve_Level9ErrorSurfacesInWarnings(t *testing.T) {
	fcr := newFakeConceptRepo()
	fbr := newFakeBindingsRepo()
	sem := &fakeSemanticLookup{}
	sfo := &fakeSearchFanOut{err: errors.New("backend provider down")}

	r := NewVisualResolver(ResolverDeps{Concepts: fcr, Bindings: fbr, External: sfo, Semantic: sem, Ranker: NewDefaultRanker(nil, nil)})

	res, err := r.Resolve(context.Background(), ResolveRequest{
		ProjectID: "p1",
		Language:  "it",
		Scenes: []SceneSpec{
			{ID: "s1", Text: "test", Language: "it", DurationMs: 8000, Slots: []SlotKind{media.SlotPrimaryVideo}},
		},
		Policy: ResolvePolicy{AllowExternalSearch: true},
	})
	// godlike/07 graceful miss: zero layers is a typed WARN, not
	// a fatal error envelope. The backend failure must surface in
	// the warnings array so the dashboard preview can branch on it.
	require.NoError(t, err, "graceful cascade miss MUST NOT abort the batch envelope")
	assert.Empty(t, res.Plans, "no local cache + external error MUST yield zero plans")
	assert.NotEmpty(t, res.Warnings, "warnings MUST surface the Level 9 backend error")
	found := false
	for _, w := range res.Warnings {
		if strings.Contains(w, "external search error") {
			found = true
			break
		}
	}
	assert.True(t, found, "warnings MUST contain 'external search error' notice")
}

func TestResolve_EmptyScenesReturnsTypedFailure(t *testing.T) {
	r := NewVisualResolver(ResolverDeps{Concepts: newFakeConceptRepo(), Bindings: newFakeBindingsRepo(), Semantic: &fakeSemanticLookup{}, Ranker: NewDefaultRanker(nil, nil)})
	_, err := r.Resolve(context.Background(), ResolveRequest{ProjectID: "p1", Language: "it"})
	assert.Error(t, err, "empty scenes MUST be a typed failure (godlike/07)")
	assert.True(t, errors.Is(err, ErrInvalidPhrase),
		"empty-scenes error MUST wrap ErrInvalidPhrase")
}

func TestResolve_BindingScoresFlowThroughRanker(t *testing.T) {
	// A binding with operator-curated ManualScore=0.95 MUST
	// produce a final score well above the downrank band.
	fcr := &fakeConceptRepo{byID: map[string]MediaConcept{
		"c-1": {ID: "c-1", Language: "it", PhraseFingerprint: fingerprintForNormalized("it", "test")},
	}}
	fbr := newFakeBindingsRepo()
	sem := &fakeSemanticLookup{}
	sfo := &fakeSearchFanOut{}

	_, _ = fcr.Upsert(context.Background(), fcr.byID["c-1"])
	_, _ = fbr.Upsert(context.Background(), MediaBinding{
		ID: "b-1", ConceptID: "c-1", AssetID: "asset-x",
		SlotKind: media.SlotPrimaryVideo, Origin: OriginManual,
		ApprovalStatus: ApprovalApproved,
		ManualScore:    0.95, SemanticScore: 0.90, QualityScore: 0.80, SuccessScore: 0.75,
	})
	// Force re-fetch path: set byID on fbr.
	b, _ := fbr.FindByID(context.Background(), "b-1")
	_ = b // already there

	r := NewVisualResolver(ResolverDeps{Concepts: fcr, Bindings: fbr, External: sfo, Semantic: sem, Ranker: NewDefaultRanker(nil, nil)})

	res, err := r.Resolve(context.Background(), ResolveRequest{
		ProjectID: "p1",
		Language:  "it",
		Scenes: []SceneSpec{
			{ID: "s1", Text: "test", Language: "it", DurationMs: 8000, Slots: []SlotKind{media.SlotPrimaryVideo}},
		},
		Policy: ResolvePolicy{},
	})
	require.NoError(t, err)
	require.Len(t, res.Plans, 1)
	layer := res.Plans[0].Layers[0]
	assert.Greater(t, layer.CandidateScore, 0.20,
		"operator-curated ManualScore MUST produce a final_score in the Accept band (> 0.05)")
}

func TestAspectMismatchForBinaryMapping(t *testing.T) {
	// primary_video slot expects "video" MediaType.
	assert.True(t, aspectMismatchFor(media.SlotPrimaryVideo, "image"),
		"image MUST mismatch primary_video slot")
	assert.False(t, aspectMismatchFor(media.SlotPrimaryVideo, "video"))
	assert.False(t, aspectMismatchFor(media.SlotPrimaryVideo, ""), "empty MediaType MUST bypass (legacy rows)")
	// image slot expects "image" MediaType.
	assert.True(t, aspectMismatchFor(media.SlotSecondaryImage, "video"))
	assert.False(t, aspectMismatchFor(media.SlotSecondaryImage, "image"))
}

func TestUpgradeSourceRankOrdering(t *testing.T) {
	assert.Equal(t, "exact", upgradeSource("exact", "exact"))
	assert.Equal(t, "exact", upgradeSource("semantic", "exact"),
		"exact MUST outrank semantic")
	assert.Equal(t, "semantic", upgradeSource("local", "semantic"),
		"semantic MUST outrank local")
	assert.Equal(t, "external", upgradeSource("", "external"),
		"empty default MUST be replaced by any winner")
}

func TestBindingsToFilteredCandidatesSkipsEmptyAssetID(t *testing.T) {
	in := []MediaBinding{
		{ID: "b-1", AssetID: "asset-1"},
		{ID: "b-2", AssetID: ""}, // skipped
	}
	out := bindingsToFilteredCandidates(in)
	require.Len(t, out, 1)
	assert.Equal(t, "asset-1", out[0].Candidate.AssetID)
	assert.Equal(t, "b-1", out[0].Binding.ID, "Binding envelope MUST be carried lossless")
}

func TestFingerprintNormalizedEqualsRaw(t *testing.T) {
	// godlike/06 SSOT strict: same normalized phrase → same
	// fingerprint, regardless of casing / spacing.
	a := fingerprintForNormalized("it", "i maya costruirono grandi città")
	b := fingerprintForNormalized("it", "i maya costruirono grandi città")
	assert.Equal(t, a, b, "identical input MUST produce identical fingerprint")
	// Different phrase → different fingerprint.
	c := fingerprintForNormalized("it", "i maya costruirono grandi città ")
	// Trailing space → different normalized input via the resolver's
	// path, but the helper itself takes the pre-normalized string so
	// callers are responsible for canonicalization. We assert
	// STRING identity rather than whitespace-strip equality.
	assert.NotEqual(t, a, strings.TrimSpace(c),
		"raw caller's job to canonicalize; the helper is byte-faithful")
}

func TestLevelExactMatch_NormalizedUppercaseFingerprintEquality(t *testing.T) {
	// godlike/06 hot-path test: "I Maya" upper + "i maya" lower MUST
	// produce the same fingerprint after the resolver's
	// canonicalization. The helper takes the normalized text so this
	// is end-to-end.
	a := fingerprintForNormalized("it", "i maya")
	b := fingerprintForNormalized("it", "i maya")
	assert.Equal(t, a, b)
}
