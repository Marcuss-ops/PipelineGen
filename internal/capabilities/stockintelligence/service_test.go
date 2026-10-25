package stockintelligence

import (
	"context"
	"errors"
	"fmt"
	"testing"

	scriptports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"github.com/stretchr/testify/require"
)

// stubLocalSearch is a configurable LocalSearchPort stub. It returns a
// fixed candidate set and (optionally) an error.
type stubLocalSearch struct {
	cands []Candidate
	err   error
}

func (s stubLocalSearch) SearchLocal(_ context.Context, _ string, _ int) ([]Candidate, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.cands, nil
}

// stubHydrator is a configurable AssetHydratorPort stub. It returns a
// fixed id→label map.
type stubHydrator struct {
	labels map[string]string
	err    error
}

func (s stubHydrator) Hydrate(_ context.Context, ids []string) (map[string]string, error) {
	if s.err != nil {
		return nil, s.err
	}
	out := make(map[string]string, len(ids))
	for _, id := range ids {
		if label, ok := s.labels[id]; ok {
			out[id] = label
		}
	}
	return out, nil
}

// stubProvider is a configurable ProviderClientPort stub. It counts how
// many times SearchProvider was called (the metric MediaCert asserts).
type stubProvider struct {
	cands []Candidate
	err   error
	calls int
}

func (s *stubProvider) SearchProvider(_ context.Context, _ string, _ int) ([]Candidate, error) {
	s.calls++
	if s.err != nil {
		return nil, s.err
	}
	return s.cands, nil
}

// stubSampler picks the highest-generic-similarity candidate whose label
// references the subject (a deterministic stand-in for the Rust
// MediaSampler). Returns "" when no candidate is acceptable.
func stubSampler(cands []Candidate, segmentID, subject string, _ []string) (string, error) {
	var best *Candidate
	for i := range cands {
		c := &cands[i]
		if subject != "" && !contains(c.Label, subject) {
			continue
		}
		if best == nil || c.GenericSimilarity > best.GenericSimilarity {
			best = c
		}
	}
	if best == nil {
		return "", nil
	}
	return best.AssetID, nil
}

func contains(hay, needle string) bool {
	return len(hay) >= len(needle) && indexOf(hay, needle) >= 0
}

func indexOf(hay, needle string) int {
	for i := 0; i+len(needle) <= len(hay); i++ {
		if hay[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}

// TestLocalFirstServesValidWinnerWithZeroProviderRequests pins the
// canonical LOCAL FIRST success: a local catalog with 50 relevant hummus
// stub asserts its call counter stays at 0.
func TestLocalFirstServesValidWinnerWithZeroProviderRequests(t *testing.T) {
	// 50 local hummus candidates, each with a strong similarity.
	localCands := make([]Candidate, 50)
	for i := range localCands {
		localCands[i] = Candidate{
			AssetID:           assetID(i),
			Label:             "hummus",
			GenericSimilarity: 0.9,
			OwnerSegmentID:    "mediterranean-02-hummus",
		}
	}
	provider := &stubProvider{}
	resolver, err := NewResolver(
		stubLocalSearch{cands: localCands},
		stubHydrator{labels: map[string]string{assetID(0): "hummus"}},
		provider,
		stubSampler,
	)
	require.NoError(t, err)

	res, err := resolver.Resolve(context.Background(), ResolveRequest{
		SegmentID: "mediterranean-02-hummus",
		Subject:   "hummus",
		Query:     "hummus",
	})
	require.NoError(t, err)
	require.Equal(t, 50, res.LocalCandidateCount)
	require.Equal(t, 0, res.ProviderLiveRequests, "local-first must make 0 provider requests")
	require.NotEmpty(t, res.WinnerAssetID, "local-first must produce a valid winner")
	require.Equal(t, 0, provider.calls, "the provider stub must not be called")
}

// TestFallbackMakesExactlyOneProviderRequest pins the fallback path:
// when the local catalog returns 0 candidates, the resolver makes exactly
// is exactly 1.
func TestFallbackMakesExactlyOneProviderRequest(t *testing.T) {
	provider := &stubProvider{
		cands: []Candidate{
			{
				AssetID:           "artlist-hummus-001",
				Label:             "hummus",
				GenericSimilarity: 0.85,
			},
		},
	}
	resolver, err := NewResolver(
		stubLocalSearch{cands: nil}, // 0 local candidates
		stubHydrator{},
		provider,
		stubSampler,
	)
	require.NoError(t, err)

	res, err := resolver.Resolve(context.Background(), ResolveRequest{
		SegmentID: "mediterranean-02-hummus",
		Subject:   "hummus",
		Query:     "hummus",
	})
	require.NoError(t, err)
	require.Equal(t, 0, res.LocalCandidateCount)
	require.Equal(t, 1, res.ProviderLiveRequests, "fallback must make exactly 1 provider request")
	require.NotEmpty(t, res.WinnerAssetID)
	require.Equal(t, 1, provider.calls, "the provider stub must be called exactly once")
	require.NotEmpty(t, res.FallbackReason, "fallback reason must be recorded")
}

// TestLowScoreTriggersFallback pins the best_score < minimum_quality
// fallback path: when the local candidates are below the quality floor,
// the resolver consults the provider even though the local count is
// above the threshold.
func TestLowScoreTriggersFallback(t *testing.T) {
	// 15 local candidates (above the threshold of 10) but each with a
	// weak 0.3 similarity, below the default 0.6 minimum quality.
	localCands := make([]Candidate, 15)
	for i := range localCands {
		localCands[i] = Candidate{
			AssetID:           assetID(i),
			Label:             "hummus",
			GenericSimilarity: 0.3,
			OwnerSegmentID:    "mediterranean-02-hummus",
		}
	}
	provider := &stubProvider{
		cands: []Candidate{
			{
				AssetID:           "artlist-hummus-001",
				Label:             "hummus",
				GenericSimilarity: 0.88,
			},
		},
	}
	resolver, err := NewResolver(
		stubLocalSearch{cands: localCands},
		stubHydrator{},
		provider,
		stubSampler,
	)
	require.NoError(t, err)

	res, err := resolver.Resolve(context.Background(), ResolveRequest{
		SegmentID: "mediterranean-02-hummus",
		Subject:   "hummus",
		Query:     "hummus",
	})
	require.NoError(t, err)
	require.Equal(t, 1, res.ProviderLiveRequests, "low-score must trigger fallback")
	require.Equal(t, 1, provider.calls)
}

// TestBatchAggregatesProviderRequestCount pins the batch aggregation:
// a 5-segment run where one segment falls back must report
// TotalProviderLiveRequests=1 and LocalFirstServedCount=4.
func TestBatchAggregatesProviderRequestCount(t *testing.T) {
	// 4 local-first segments return enough candidates (15) to clear the
	// default threshold of 10, with strong scores above the 0.6 floor —
	// so they are served locally with 0 provider requests. The 5th
	// segment returns 0 candidates and triggers exactly 1 fallback.
	localCands := make([]Candidate, 15)
	for i := range localCands {
		localCands[i] = Candidate{
			AssetID:           "local-" + itoa(i),
			Label:             "hummus",
			GenericSimilarity: 0.9,
			OwnerSegmentID:    "seg-0",
		}
	}
	provider := &stubProvider{
		cands: []Candidate{{AssetID: "artlist-4", Label: "hummus", GenericSimilarity: 0.85}},
	}
	resolver, err := NewResolver(
		stubLocalSearch{cands: localCands},
		stubHydrator{},
		provider,
		stubSampler,
	)
	require.NoError(t, err)
	svc, err := NewService(resolver)
	require.NoError(t, err)

	reqs := make([]ResolveRequest, 5)
	for i := range reqs {
		reqs[i] = ResolveRequest{
			SegmentID: "seg-" + itoa(i),
			Subject:   "hummus",
			Query:     "hummus",
		}
	}
	// The 5th request returns 0 local candidates via a dedicated stub
	// resolver: re-wire the resolver's local port to return empty for
	// the 5th call. Simplest: use a counting local port.
	counting := &countingLocal{cands: localCands, emptyForNth: 4}
	resolver.local = counting

	batch, err := svc.ResolveBatch(context.Background(), reqs)
	require.NoError(t, err)
	require.Equal(t, 1, batch.TotalProviderLiveRequests, "batch must aggregate to 1 provider request")
	require.Equal(t, 4, batch.LocalFirstServedCount, "4 of 5 served locally")
}

// countingLocal is a LocalSearchPort stub that returns `cands` for the
// first N-1 calls and an empty slice for the Nth call (emptyForNth).
type countingLocal struct {
	cands       []Candidate
	emptyForNth int
	calls       int
}

func (c *countingLocal) SearchLocal(_ context.Context, _ string, _ int) ([]Candidate, error) {
	c.calls++
	if c.calls-1 == c.emptyForNth {
		return nil, nil
	}
	return c.cands, nil
}

func assetID(i int) string {
	return "local-" + itoa(i)
}

// TestFallbackSkippedWhenProviderUnavailable pins the waste-cut guard: when
// the provider fallback is STRUCTURALLY unavailable (no query can ever
// answer), Resolve must skip the fallback and complete local-first with an
// honest result (0 live requests + the skip recorded in FallbackReason)
// instead of failing the segment on a doomed query.
func TestFallbackSkippedWhenProviderUnavailable(t *testing.T) {
	localCands := []Candidate{
		{AssetID: "local-0", Label: "hummus", GenericSimilarity: 0.4, OwnerSegmentID: "seg-0"},
		{AssetID: "local-1", Label: "hummus", GenericSimilarity: 0.5, OwnerSegmentID: "seg-0"},
	}
	provider := &stubProvider{err: fmt.Errorf("registry probe: %w", ErrProviderUnavailable)}
	resolver, err := NewResolver(stubLocalSearch{cands: localCands}, stubHydrator{}, provider, stubSampler)
	require.NoError(t, err)

	res, err := resolver.Resolve(context.Background(), ResolveRequest{
		SegmentID: "seg-0", Subject: "hummus", Query: "hummus",
	})
	require.NoError(t, err, "a structurally unavailable provider must not fail the resolve")
	require.Equal(t, 0, res.ProviderLiveRequests)
	require.Equal(t, 2, res.LocalCandidateCount, "local truth must survive the skipped fallback")
	require.Contains(t, res.FallbackReason, "provider fallback skipped", "the skip must be recorded, never silent")
	require.Equal(t, "local-1", res.WinnerAssetID, "the winner must be picked from the local set")
	require.Equal(t, 1, provider.calls, "exactly one classified attempt; no retry of a doomed query")
}

// TestFallbackSkippedWhenProviderNotRegistered pins the classification of
// the registry's own not-registered sentinel as structural unavailability,
// even when it reaches the resolver wrapped by intermediate adapters.
func TestFallbackSkippedWhenProviderNotRegistered(t *testing.T) {
	provider := &stubProvider{err: fmt.Errorf("stockintelligence: provider search: %w", scriptports.ErrVidRushProviderNotFound)}
	resolver, err := NewResolver(stubLocalSearch{}, stubHydrator{}, provider, stubSampler)
	require.NoError(t, err)

	res, err := resolver.Resolve(context.Background(), ResolveRequest{
		SegmentID: "seg-0", Subject: "hummus", Query: "hummus",
	})
	require.NoError(t, err)
	require.Equal(t, 0, res.ProviderLiveRequests)
	require.Contains(t, res.FallbackReason, "provider fallback skipped")
	require.Empty(t, res.WinnerAssetID, "no candidates means no winner — never fabricated")
}

// TestTransientProviderFailureStillFailsClosed pins the boundary of the
// guard: a REGULAR provider failure (network, rate limit) is not structural
// unavailability and must keep failing the resolve closed.
func TestTransientProviderFailureStillFailsClosed(t *testing.T) {
	provider := &stubProvider{err: errors.New("artlist: network down")}
	resolver, err := NewResolver(stubLocalSearch{}, stubHydrator{}, provider, stubSampler)
	require.NoError(t, err)

	_, err = resolver.Resolve(context.Background(), ResolveRequest{
		SegmentID: "seg-0", Subject: "hummus", Query: "hummus",
	})
	require.Error(t, err, "a transient provider failure must stay fail-closed")
	require.Contains(t, err.Error(), "provider search")
}

// availabilityRegistryStub is a RegistrySearch that also exposes the
// optional Provider lookup: its Search must never be reached when the
// provider is structurally unregistered.
type availabilityRegistryStub struct {
	providerErr error
	searchCalls int
}

func (s *availabilityRegistryStub) Search(context.Context, string, scriptports.VidRushSearchRequest) ([]scriptpkg.SegmentAssetCandidate, error) {
	s.searchCalls++
	return nil, nil
}

func (s *availabilityRegistryStub) Provider(string) (scriptports.VidRushAssetProvider, error) {
	return nil, s.providerErr
}

// registrySearchOnlyStub satisfies RegistrySearch alone (no availability
// probe), pinning the legacy dispatch path for fakes and future adapters.
type registrySearchOnlyStub struct{ searchCalls int }

func (s *registrySearchOnlyStub) Search(context.Context, string, scriptports.VidRushSearchRequest) ([]scriptpkg.SegmentAssetCandidate, error) {
	s.searchCalls++
	return nil, nil
}

// TestRegistryProviderClientProbesAvailabilityBeforeQuery pins the quick
// guard on the registry client: when the provider is not registered the
// typed ErrProviderUnavailable comes back WITHOUT any query being issued.
func TestRegistryProviderClientProbesAvailabilityBeforeQuery(t *testing.T) {
	registry := &availabilityRegistryStub{providerErr: scriptports.ErrVidRushProviderNotFound}
	client := RegistryProviderClient{Registry: registry}

	_, err := client.SearchProvider(context.Background(), "hummus", 5)
	require.Error(t, err)
	require.ErrorIs(t, err, ErrProviderUnavailable)
	require.Equal(t, 0, registry.searchCalls, "an unregistered provider must never receive a query")
}

// TestRegistryProviderClientWithoutProbeStillDispatches pins that a
// RegistrySearch without the optional availability capability keeps the
// legacy dispatch path (no behavior change for existing fakes/adapters).
func TestRegistryProviderClientWithoutProbeStillDispatches(t *testing.T) {
	registry := &registrySearchOnlyStub{}
	client := RegistryProviderClient{Registry: registry}

	_, err := client.SearchProvider(context.Background(), "hummus", 5)
	require.NoError(t, err)
	require.Equal(t, 1, registry.searchCalls)
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(buf[pos:])
}
