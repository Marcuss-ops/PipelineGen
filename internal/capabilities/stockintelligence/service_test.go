package stockintelligence

import (
	"context"
	"testing"

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
// videos serves the winner with 0 Artlist browser requests. The provider
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
// 1 Artlist browser request. The provider stub asserts its call counter
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
