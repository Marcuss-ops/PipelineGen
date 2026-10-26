// Package stockintelligence — cache_test.go pins the content-addressed
// resolve replay cache (cache.go): entity/query reuse is served without
// repeating discovery I/O, the key separates policy/thresholds/query, forced
// refresh bypasses and rewrites the entry, empty observations are never
// cached, cache errors degrade to live resolution, provider ownership is
// re-stamped per sampling segment, and concurrent identical resolves share
// exactly one live pass.
package stockintelligence

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

// countingLocalSearch counts SearchLocal invocations and can block them to
// exercise the coalescer.
type countingLocalSearch struct {
	mu      sync.Mutex
	calls   int
	cands   []Candidate
	release chan struct{}
}

func (s *countingLocalSearch) SearchLocal(_ context.Context, _ string, _ int) ([]Candidate, error) {
	s.mu.Lock()
	s.calls++
	release := s.release
	cands := append([]Candidate(nil), s.cands...)
	s.mu.Unlock()
	if release != nil {
		<-release
	}
	return cands, nil
}

func (s *countingLocalSearch) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

// countingProvider counts SearchProvider invocations.
type countingProvider struct {
	mu    sync.Mutex
	calls int
	cands []Candidate
}

func (s *countingProvider) SearchProvider(_ context.Context, _ string, _ int) ([]Candidate, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	return append([]Candidate(nil), s.cands...), nil
}

func (s *countingProvider) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

// recordingSampler records the segment each sampling call was made for and
// returns the first candidate asset id ("" for an empty set).
type recordingSampler struct {
	mu         sync.Mutex
	segments   []string
	subjects   []string
	perSegment map[string]string
}

func (s *recordingSampler) sample(cands []Candidate, segmentID, subject string, _ []string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.segments = append(s.segments, segmentID)
	s.subjects = append(s.subjects, subject)
	winner := ""
	if len(cands) > 0 {
		winner = cands[0].AssetID
	}
	if s.perSegment == nil {
		s.perSegment = map[string]string{}
	}
	s.perSegment[segmentID] = winner
	return winner, nil
}

// memoryResolveCache is a map-backed ResolveCache with fault injection.
type memoryResolveCache struct {
	mu     sync.Mutex
	data   map[string][]byte
	getErr error
	putErr error
}

func newMemoryResolveCache() *memoryResolveCache {
	return &memoryResolveCache{data: map[string][]byte{}}
}

func (c *memoryResolveCache) Get(_ context.Context, key string) ([]byte, bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.getErr != nil {
		return nil, false, c.getErr
	}
	raw, ok := c.data[key]
	return raw, ok, nil
}

func (c *memoryResolveCache) Put(_ context.Context, key string, payload []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.putErr != nil {
		return c.putErr
	}
	c.data[key] = append([]byte(nil), payload...)
	return nil
}

func newCachedResolver(t *testing.T, local LocalSearchPort, provider ProviderClientPort, sampler func([]Candidate, string, string, []string) (string, error), cache ResolveCache) *Resolver {
	t.Helper()
	resolver, err := NewResolver(local, stubHydrator{labels: map[string]string{}}, provider, sampler)
	require.NoError(t, err)
	return resolver.WithCache(cache)
}

// TestResolve_EntityReuseServedFromCacheWithoutRepeatedDiscovery pins the
// core promise of the resolve cache: the same entity/query context resolves
// with ~0 discovery work on reuse, while the winner is still sampled fresh
// for every sampling segment.
func TestResolve_EntityReuseServedFromCacheWithoutRepeatedDiscovery(t *testing.T) {
	local := &countingLocalSearch{cands: []Candidate{
		{AssetID: "asset-1", Label: "Giulia Conti", GenericSimilarity: 0.9},
		{AssetID: "asset-2", Label: "Giulia Conti", GenericSimilarity: 0.8},
	}}
	provider := &countingProvider{}
	sampler := &recordingSampler{}
	resolver := newCachedResolver(t, local, provider, sampler.sample, newMemoryResolveCache())

	first, err := resolver.Resolve(context.Background(), ResolveRequest{
		SegmentID: "seg-A", Subject: "Giulia Conti", Query: "giulia conti",
	})
	require.NoError(t, err)
	require.False(t, first.CacheHit)
	require.Equal(t, 1, local.callCount())
	require.Equal(t, []string{"asset-1", "asset-2"}, assetIDs(first.Candidates))
	require.Equal(t, "asset-1", first.WinnerAssetID)

	// Same entity/query context in ANOTHER segment: zero discovery I/O.
	second, err := resolver.Resolve(context.Background(), ResolveRequest{
		SegmentID: "seg-B", Subject: "Giulia Conti", Query: "giulia conti",
	})
	require.NoError(t, err)
	require.True(t, second.CacheHit, "entity reuse must be served from the cache")
	require.Equal(t, 1, local.callCount(), "discovery must not repeat on entity reuse")
	require.Equal(t, 0, second.ProviderLiveRequests)
	require.Equal(t, []string{"asset-1", "asset-2"}, assetIDs(second.Candidates))

	// The winner is sampled per request against the request's segment.
	require.Equal(t, []string{"seg-A", "seg-B"}, sampler.segments)
	require.Equal(t, "seg-A", first.SegmentID)
	require.Equal(t, "seg-B", second.SegmentID)
}

// TestResolve_CacheSeparatesPolicyThresholdsAndQuery pins the key's
// content-addressed boundaries: query text (normalized), provider policy and
// effective thresholds each partition the cache.
func TestResolve_CacheSeparatesPolicyThresholdsAndQuery(t *testing.T) {
	local := &countingLocalSearch{cands: []Candidate{
		{AssetID: "asset-1", Label: "subject", GenericSimilarity: 0.9},
	}}
	resolver := newCachedResolver(t, local, &countingProvider{}, stubHighestSimilarity, newMemoryResolveCache())

	base := ResolveRequest{SegmentID: "seg-1", Subject: "subject", Query: "subject", PolicySignature: "policy-1"}
	_, err := resolver.Resolve(context.Background(), base)
	require.NoError(t, err)
	require.Equal(t, 1, local.callCount())

	// Whitespace/case variants of the same query are the same cache entry.
	same := base
	same.SegmentID = "seg-2"
	same.Query = "  Subject "
	_, err = resolver.Resolve(context.Background(), same)
	require.NoError(t, err)
	require.Equal(t, 1, local.callCount(), "normalized query variants must share one entry")

	otherPolicy := base
	otherPolicy.PolicySignature = "policy-2"
	_, err = resolver.Resolve(context.Background(), otherPolicy)
	require.NoError(t, err)
	require.Equal(t, 2, local.callCount(), "a different provider policy must miss")

	otherThreshold := base
	otherThreshold.LocalCandidateThreshold = 5
	_, err = resolver.Resolve(context.Background(), otherThreshold)
	require.NoError(t, err)
	require.Equal(t, 3, local.callCount(), "different effective thresholds must miss")

	otherQuery := base
	otherQuery.Query = "another subject"
	_, err = resolver.Resolve(context.Background(), otherQuery)
	require.NoError(t, err)
	require.Equal(t, 4, local.callCount(), "a different query must miss")
}

// TestResolve_ForceRefreshBypassesCacheAndRewritesEntry pins the refresh
// contract shared with the image caches: skip the cached read, run live, and
// leave the refreshed entry behind for later replays.
func TestResolve_ForceRefreshBypassesCacheAndRewritesEntry(t *testing.T) {
	local := &countingLocalSearch{cands: []Candidate{
		{AssetID: "asset-v1", Label: "subject", GenericSimilarity: 0.9},
	}}
	resolver := newCachedResolver(t, local, &countingProvider{}, stubHighestSimilarity, newMemoryResolveCache())

	req := ResolveRequest{SegmentID: "seg-1", Subject: "subject", Query: "subject"}
	first, err := resolver.Resolve(context.Background(), req)
	require.NoError(t, err)
	require.Equal(t, "asset-v1", first.WinnerAssetID)

	local.mu.Lock()
	local.cands = []Candidate{{AssetID: "asset-v2", Label: "subject", GenericSimilarity: 0.95}}
	local.mu.Unlock()

	refresh := req
	refresh.ForceRefresh = true
	refetched, err := resolver.Resolve(context.Background(), refresh)
	require.NoError(t, err)
	require.False(t, refetched.CacheHit)
	require.Equal(t, 2, local.callCount(), "a forced refresh must run live discovery")
	require.Equal(t, "asset-v2", refetched.WinnerAssetID)

	replay, err := resolver.Resolve(context.Background(), req)
	require.NoError(t, err)
	require.True(t, replay.CacheHit)
	require.Equal(t, 2, local.callCount())
	require.Equal(t, "asset-v2", replay.WinnerAssetID, "the refreshed entry must serve later replays")
}

// TestResolve_EmptyCandidateSetsAreNotCached pins the no-negative-caching
// rule: an empty discovery is a temporary provider observation, so later
// resolves must retry live discovery instead of replaying emptiness.
func TestResolve_EmptyCandidateSetsAreNotCached(t *testing.T) {
	local := &countingLocalSearch{}
	provider := &countingProvider{}
	resolver := newCachedResolver(t, local, provider, stubHighestSimilarity, newMemoryResolveCache())

	req := ResolveRequest{SegmentID: "seg-1", Query: "nothing", Subject: "nothing"}
	first, err := resolver.Resolve(context.Background(), req)
	require.NoError(t, err)
	require.Empty(t, first.Candidates)

	second, err := resolver.Resolve(context.Background(), req)
	require.NoError(t, err)
	require.False(t, second.CacheHit, "an empty observation must not become a cached hit")
	require.Equal(t, 2, local.callCount(), "discovery must retry after an empty result")
	require.Equal(t, 2, provider.callCount())
}

// TestResolve_CacheErrorsDegradeToLiveResolution pins the projection
// contract: cache read/write failures never fail or shortcut a resolution.
func TestResolve_CacheErrorsDegradeToLiveResolution(t *testing.T) {
	local := &countingLocalSearch{cands: []Candidate{
		{AssetID: "asset-1", Label: "subject", GenericSimilarity: 0.9},
	}}

	brokenRead := newMemoryResolveCache()
	brokenRead.getErr = errors.New("l2 read failed")
	resolver := newCachedResolver(t, local, &countingProvider{}, stubHighestSimilarity, brokenRead)
	got, err := resolver.Resolve(context.Background(), ResolveRequest{SegmentID: "seg-1", Query: "subject", Subject: "subject"})
	require.NoError(t, err)
	require.False(t, got.CacheHit)
	require.Equal(t, []string{"asset-1"}, assetIDs(got.Candidates))
	require.Equal(t, 1, local.callCount())

	brokenWrite := newMemoryResolveCache()
	brokenWrite.putErr = errors.New("l2 write failed")
	resolver = newCachedResolver(t, local, &countingProvider{}, stubHighestSimilarity, brokenWrite)
	got, err = resolver.Resolve(context.Background(), ResolveRequest{SegmentID: "seg-2", Query: "subject", Subject: "subject"})
	require.NoError(t, err, "a cache write failure must not fail a live resolution")
	require.Equal(t, []string{"asset-1"}, assetIDs(got.Candidates))
}

// TestResolve_CacheHitStampsProviderOwnersToSamplingSegment pins the owner
// contract on replay: provider candidates discovered without an owner are
// stamped with the segment currently being sampled, so a cached pass can
// never trip the MediaSampler's owner_mismatch gate for another scene.
func TestResolve_CacheHitStampsProviderOwnersToSamplingSegment(t *testing.T) {
	provider := &countingProvider{cands: []Candidate{
		{AssetID: "prov-1", Label: "subject", GenericSimilarity: 0.9},
		{AssetID: "prov-2", Label: "subject", GenericSimilarity: 0.8},
	}}
	resolver := newCachedResolver(t, &countingLocalSearch{}, provider, stubHighestSimilarity, newMemoryResolveCache())

	first, err := resolver.Resolve(context.Background(), ResolveRequest{SegmentID: "seg-A", Query: "subject", Subject: "subject"})
	require.NoError(t, err)
	require.Equal(t, 1, provider.callCount())
	for _, candidate := range first.Candidates {
		require.Equal(t, "seg-A", candidate.OwnerSegmentID)
	}

	second, err := resolver.Resolve(context.Background(), ResolveRequest{SegmentID: "seg-B", Query: "subject", Subject: "subject"})
	require.NoError(t, err)
	require.True(t, second.CacheHit)
	require.Equal(t, 1, provider.callCount(), "the cached pass must not re-query the provider")
	for _, candidate := range second.Candidates {
		require.Equal(t, "seg-B", candidate.OwnerSegmentID, "owners must follow the sampling segment on replay")
	}
}

// TestResolve_ConcurrentIdenticalResolvesShareOneLivePass pins the
// coalescer: N scenes resolving the same entity in parallel issue exactly ONE
// discovery pass, and every caller still gets its own sampled winner.
func TestResolve_ConcurrentIdenticalResolvesShareOneLivePass(t *testing.T) {
	const concurrency = 8
	local := &countingLocalSearch{
		cands:   []Candidate{{AssetID: "asset-1", Label: "subject", GenericSimilarity: 0.9}},
		release: make(chan struct{}),
	}
	sampler := &recordingSampler{}
	resolver := newCachedResolver(t, local, &countingProvider{}, sampler.sample, newMemoryResolveCache())

	var ready, done sync.WaitGroup
	ready.Add(concurrency)
	done.Add(concurrency)
	results := make([]ResolveResult, concurrency)
	errs := make([]error, concurrency)
	for i := 0; i < concurrency; i++ {
		go func(i int) {
			defer done.Done()
			ready.Done()
			results[i], errs[i] = resolver.Resolve(context.Background(), ResolveRequest{
				SegmentID: "seg-" + string(rune('A'+i)), Query: "subject", Subject: "subject",
			})
		}(i)
	}
	ready.Wait()
	close(local.release)
	done.Wait()
	for i := 0; i < concurrency; i++ {
		require.NoError(t, errs[i])
		require.Equal(t, []string{"asset-1"}, assetIDs(results[i].Candidates))
		require.Equal(t, "asset-1", results[i].WinnerAssetID)
		require.Equal(t, "seg-"+string(rune('A'+i)), results[i].SegmentID)
	}
	require.Equal(t, 1, local.callCount(), "identical concurrent resolves must share one live discovery pass")
	require.Len(t, sampler.segments, concurrency, "the winner is still sampled once per request")
}

// TestResolveCacheKey_NormalizesQueryAndSeparatesContext pins the key
// derivation itself: normalized query text collapses, while policy and
// thresholds partition.
func TestResolveCacheKey_NormalizesQueryAndSeparatesContext(t *testing.T) {
	base := ResolveRequest{Query: "Giulia Conti", PolicySignature: "p1"}
	require.Equal(t, resolveCacheKey(base), resolveCacheKey(ResolveRequest{Query: "  giulia   CONTI ", PolicySignature: "p1"}))
	require.NotEqual(t, resolveCacheKey(base), resolveCacheKey(ResolveRequest{Query: "Giulia Conti", PolicySignature: "p2"}))
	require.NotEqual(t, resolveCacheKey(base), resolveCacheKey(ResolveRequest{Query: "Giulia Conti", PolicySignature: "p1", LocalCandidateThreshold: 5}))
	// Explicit default thresholds share the implicit-default entry.
	require.Equal(t, resolveCacheKey(base), resolveCacheKey(ResolveRequest{
		Query: "Giulia Conti", PolicySignature: "p1",
		LocalCandidateThreshold: defaultLocalCandidateThreshold, MinimumQuality: defaultMinimumQuality,
	}))
}

// TestStampProviderCandidateOwners pins the live-path stamping rule used on
// every serve: only empty provider owners are bound to the sampling segment.
func TestStampProviderCandidateOwners(t *testing.T) {
	candidates := []Candidate{
		{AssetID: "loc", Source: "local", OwnerSegmentID: ""},
		{AssetID: "prov-empty", Source: "provider", OwnerSegmentID: ""},
		{AssetID: "prov-owned", Source: "provider", OwnerSegmentID: "seg-original"},
	}
	stampProviderCandidateOwners(candidates, "seg-now")
	require.Equal(t, "", candidates[0].OwnerSegmentID, "local candidates keep their index owner")
	require.Equal(t, "seg-now", candidates[1].OwnerSegmentID)
	require.Equal(t, "seg-original", candidates[2].OwnerSegmentID, "a discovered owner must survive replay")
}

func assetIDs(candidates []Candidate) []string {
	out := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		out = append(out, candidate.AssetID)
	}
	return out
}

// stubHighestSimilarity is a deterministic sampler returning the highest
// similarity candidate ("" when none), independent of the request.
func stubHighestSimilarity(cands []Candidate, _, _ string, _ []string) (string, error) {
	var best *Candidate
	for i := range cands {
		if best == nil || cands[i].GenericSimilarity > best.GenericSimilarity {
			best = &cands[i]
		}
	}
	if best == nil {
		return "", nil
	}
	return best.AssetID, nil
}
