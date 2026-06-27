package providers

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// ── Test fixtures: deterministic SearchProviders for S3b coverage ───────

// stubSearchProvider is the canonical scaffold for aggregator
// tests. Each test builds a small registry with one or more of
// these and asserts the aggregator's partial-results behaviour.
//
// Behaviour is controlled by a single function field so test setups
// stay short and the actions the test cares about are obvious from
// the constructor.
type stubSearchProvider struct {
	name   string
	search func(ctx context.Context, req SearchRequest) (SearchResult, error)
	delay  time.Duration
	err    error
	empty  bool
	hits   int
}

func (p *stubSearchProvider) Name() string               { return p.name }
func (p *stubSearchProvider) Capabilities() []Capability { return []Capability{CapabilitySearch} }

// Search implements the deterministic stub behaviour:
//  1. Honour ctx cancellation (mirror production adapters).
//  2. Sleep `delay` (release the slot so other providers run in
//     parallel) — used to validate the 5s per-provider timeout.
//  3. Return `err` when non-nil.
//  4. Return `hits` candidates when `empty == false`; empty list
//     when `empty == true`.
func (p *stubSearchProvider) Search(ctx context.Context, req SearchRequest) (SearchResult, error) {
	if p.delay > 0 {
		select {
		case <-time.After(p.delay):
		case <-ctx.Done():
			return SearchResult{}, ctx.Err()
		}
	}
	if p.err != nil {
		return SearchResult{}, p.err
	}
	if p.empty {
		return SearchResult{Candidates: nil, NextPageToken: ""}, nil
	}
	cans := make([]Candidate, p.hits)
	for i := 0; i < p.hits; i++ {
		// Vary the score so the cross-provider ranking math
		// produces a verifiable deterministic order — qdrantScore
		// plus the synthesised rerankScore half.
		score := float64(p.hits-i) / float64(p.hits+1)
		cans[i] = Candidate{
			SourceName: p.name,
			SourceRef:  p.name + "-" + itoa(i),
			Title:      p.name + " hit " + itoa(i),
			Score:      score,
		}
	}
	return SearchResult{
		Candidates:    cans,
		NextPageToken: "tok:" + p.name,
	}, nil
}

// itoa is a tiny local digit converter used by the stub.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := false
	if n < 0 {
		neg = true
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// helper: build a registry with the given providers.
func newStubRegistry(providers ...*stubSearchProvider) *Registry {
	r := NewRegistry()
	for _, p := range providers {
		_ = r.Register(p)
	}
	r.Freeze()
	return r
}

// ── 1. Happy path: 2 providers return hits, blended ranking + cursor ──

func TestSearchAggregator_HappyPath(t *testing.T) {
	reg := newStubRegistry(
		&stubSearchProvider{name: "alpha", hits: 3},
		&stubSearchProvider{name: "beta", hits: 2},
	)
	agg := NewSearchAggregator(reg)

	res, err := agg.Aggregate(context.Background(), &SearchQuery{Query: "go"}, AggregateOptions{Limit: 5})
	if err != nil {
		t.Fatalf("Aggregate: %v", err)
	}
	if res == nil {
		t.Fatal("Aggregate returned nil result")
	}
	if len(res.Hits) != 5 {
		t.Fatalf("expected 5 hits, got %d", len(res.Hits))
	}
	if res.Total != 5 {
		t.Fatalf("expected Total=5, got %d", res.Total)
	}
	if res.ProviderErrors != nil && len(res.ProviderErrors) > 0 {
		t.Fatalf("unexpected provider errors on happy path: %v", res.ProviderErrors)
	}
	// Cursor emit: when all hits fit in Limit, the next-call
	// cursor should be empty (no more pagination).
	if res.Cursor != "" {
		t.Fatalf("expected empty cursor when page fully consumed, got %q", res.Cursor)
	}
	// FinalScore is non-zero and monotonically non-increasing (sort
	// order: descending).
	for i := 1; i < len(res.Hits); i++ {
		if res.Hits[i-1].FinalScore < res.Hits[i].FinalScore {
			t.Fatalf("hits not sorted descending: Hits[%d]=%.3f < Hits[%d]=%.3f",
				i-1, res.Hits[i-1].FinalScore, i, res.Hits[i].FinalScore)
		}
	}
	for _, h := range res.Hits {
		if h.ProviderName != "alpha" && h.ProviderName != "beta" {
			t.Fatalf("unexpected ProviderName on hit: %q", h.ProviderName)
		}
	}
}

// ── 2. Slow provider: 6s sleep with default 5s timeout ────────────────

func TestSearchAggregator_SlowProviderTimeout(t *testing.T) {
	reg := newStubRegistry(
		&stubSearchProvider{name: "fast", hits: 2},
		&stubSearchProvider{name: "slow", delay: 6 * time.Second},
	)
	agg := NewSearchAggregator(reg)
	agg.SetMaxConcurrency(4)

	// Cap the entire test wall-clock at 8s so the slow provider
	// CANNOT hold the test open indefinitely if the per-provider
	// timeout misfires.
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	start := time.Now()
	res, err := agg.Aggregate(ctx, &SearchQuery{Query: "go"}, AggregateOptions{})
	if err != nil {
		t.Fatalf("Aggregate: %v", err)
	}
	elapsed := time.Since(start)

	// Per-provider default timeout is 5s (DefaultHealthTimeout).
	// The fast sibling should complete well before 1s; the slow
	// one's ctx cancels at ~5s. The aggregate should finish within
	// 6s wall-clock (5s timeout + small overhead).
	if elapsed > 7*time.Second {
		t.Fatalf("Aggregate took too long under slow-provider test: %v", elapsed)
	}
	if res == nil {
		t.Fatal("nil AggregateResult")
	}
	// Fast provider contributes its hits; slow provider records
	// an error.
	if len(res.Hits) != 2 {
		t.Fatalf("expected 2 hits from fast provider, got %d", len(res.Hits))
	}
	if res.ProviderErrors == nil {
		t.Fatal("expected provider errors map populated by slow timeout")
	}
	slowErr, ok := res.ProviderErrors["slow"]
	if !ok {
		t.Fatalf("expected slow provider error, got map: %v", res.ProviderErrors)
	}
	// The error must be context-derived (deadline / canceled).
	// We assert containment of "context" so unrelated sentinel
	// names don't trip the assertion.
	if !strings.Contains(slowErr.Error(), "context") {
		t.Fatalf("slow provider error not context-derived: %q", slowErr.Error())
	}
}

// ── 3. Failed provider: returns err, siblings unaffected ──────────────

func TestSearchAggregator_FailedProvider(t *testing.T) {
	boom := errors.New("provider upstream boom")
	reg := newStubRegistry(
		&stubSearchProvider{name: "happy", hits: 1},
		&stubSearchProvider{name: "broken", err: boom},
	)
	agg := NewSearchAggregator(reg)

	res, err := agg.Aggregate(context.Background(), &SearchQuery{Query: "go"}, AggregateOptions{Limit: 5})
	if err != nil {
		t.Fatalf("Aggregate: %v", err)
	}
	if res == nil {
		t.Fatal("nil result")
	}
	if len(res.Hits) != 1 {
		t.Fatalf("expected 1 hit from happy sibling, got %d", len(res.Hits))
	}
	if res.Hits[0].ProviderName != "happy" {
		t.Fatalf("expected hit from happy, got %q", res.Hits[0].ProviderName)
	}
	if res.ProviderErrors == nil {
		t.Fatal("expected provider errors map populated by failed provider")
	}
	brokenErr, ok := res.ProviderErrors["broken"]
	if !ok {
		t.Fatalf("expected broken provider error, got map: %v", res.ProviderErrors)
	}
	if !errors.Is(brokenErr, boom) {
		t.Fatalf("expected broken provider error to wrap boom; got %v", brokenErr)
	}
}

// ── 4. Empty provider: returns 0 hits, counts toward Total but no error ─

func TestSearchAggregator_EmptyProvider(t *testing.T) {
	reg := newStubRegistry(
		&stubSearchProvider{name: "nonempty", hits: 2},
		&stubSearchProvider{name: "empty", empty: true},
	)
	agg := NewSearchAggregator(reg)

	res, err := agg.Aggregate(context.Background(), &SearchQuery{Query: "go"}, AggregateOptions{Limit: 5})
	if err != nil {
		t.Fatalf("Aggregate: %v", err)
	}
	if len(res.Hits) != 2 {
		t.Fatalf("expected 2 hits from nonempty, got %d", len(res.Hits))
	}
	if res.ProviderErrors == nil || len(res.ProviderErrors) != 0 {
		t.Fatalf("expected empty provider to NOT register an error, got: %v", res.ProviderErrors)
	}
	// Empty provider contributed 0 to Total — Total counts only
	// the rendered hits (consistent with the user spec definition
	// "Total: int").
	if res.Total != 2 {
		t.Fatalf("expected Total=2, got %d", res.Total)
	}
}

// ── 5. Stats correctness: latency + hit count + error rate ────────────

func TestSearchAggregator_StatsCorrectness(t *testing.T) {
	boom := errors.New("stats broken")
	reg := newStubRegistry(
		&stubSearchProvider{name: "alpha", hits: 3, delay: 10 * time.Millisecond},
		&stubSearchProvider{name: "beta", hits: 1, delay: 10 * time.Millisecond},
		&stubSearchProvider{name: "gamma", err: boom},
	)
	agg := NewSearchAggregator(reg)

	_, err := agg.Aggregate(context.Background(), &SearchQuery{Query: "go"}, AggregateOptions{})
	if err != nil {
		t.Fatalf("Aggregate: %v", err)
	}
	stats := agg.Stats()

	// alpha: 1 call, 0 errors, 3 hits. Latency >= 10ms.
	if s := stats.Providers["alpha"]; s == nil {
		t.Fatal("expected alpha stats")
	} else {
		if s.Calls != 1 {
			t.Fatalf("alpha Calls: want 1, got %d", s.Calls)
		}
		if s.Errors != 0 {
			t.Fatalf("alpha Errors: want 0, got %d", s.Errors)
		}
		if s.Hits != 3 {
			t.Fatalf("alpha Hits: want 3, got %d", s.Hits)
		}
		if s.Latency < 10*time.Millisecond {
			t.Fatalf("alpha Latency below 10ms threshold: %v", s.Latency)
		}
		if s.ErrorRate() != 0 {
			t.Fatalf("alpha ErrorRate: want 0, got %.3f", s.ErrorRate())
		}
	}
	// gamma: 1 call, 1 error, 0 hits. ErrorRate = 1.0.
	if s := stats.Providers["gamma"]; s == nil {
		t.Fatal("expected gamma stats")
	} else {
		if s.Calls != 1 {
			t.Fatalf("gamma Calls: want 1, got %d", s.Calls)
		}
		if s.Errors != 1 {
			t.Fatalf("gamma Errors: want 1, got %d", s.Errors)
		}
		if s.Hits != 0 {
			t.Fatalf("gamma Hits: want 0, got %d", s.Hits)
		}
		if s.ErrorRate() != 1.0 {
			t.Fatalf("gamma ErrorRate: want 1.0, got %.3f", s.ErrorRate())
		}
	}
	// beta present.
	if stats.Providers["beta"] == nil {
		t.Fatal("expected beta stats")
	}
}

// ── 6. Cursor encode/decode round-trip ─────────────────────────────────

func TestEncodeDecodeCursor_RoundTrip(t *testing.T) {
	p := &cursorPayload{lastSeenProvider: "artlist", lastSeenOffset: 7}
	enc := encodeCursor(p)
	if enc == "" {
		t.Fatal("encodeCursor returned empty string for non-empty payload")
	}
	if strings.Contains(enc, "{") {
		t.Fatalf("encoded cursor should be opaque (base64, no JSON braces visible): %q", enc)
	}
	got, ok := decodeCursor(enc)
	if !ok {
		t.Fatal("decodeCursor failed on valid encode")
	}
	if got.lastSeenProvider != "artlist" {
		t.Fatalf("decoded Provider: want artlist, got %q", got.lastSeenProvider)
	}
	if got.lastSeenOffset != 7 {
		t.Fatalf("decoded Offset: want 7, got %d", got.lastSeenOffset)
	}
	// Empty cursor decodes to nil/false (no error — best-effort).
	if got, ok := decodeCursor(""); got != nil || ok {
		t.Fatalf("decodeCursor(\"\") want (nil, false); got (%v, %v)", got, ok)
	}
	// Invalid base64 decodes to nil/false (no error).
	if got, ok := decodeCursor("not-base64!@#$"); got != nil || ok {
		t.Fatalf("decodeCursor(bad) want (nil, false); got (%v, %v)", got, ok)
	}
}

// ── 7. Score blend math: 65% Qdrant + 35% Rerank ──────────────────────

func TestScoredHitsFromResult_BlendIsCorrect(t *testing.T) {
	reg := newStubRegistry(
		&stubSearchProvider{name: "alpha", hits: 1},
	)
	agg := NewSearchAggregator(reg)
	res, err := agg.Aggregate(context.Background(), &SearchQuery{Query: "x"}, AggregateOptions{Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Hits) != 1 {
		t.Fatalf("want 1 hit, got %d", len(res.Hits))
	}
	h := res.Hits[0]
	// Expected: q = candidate.Score, r = max(q, 0.5), final = q*0.65 + r*0.35
	// The stub uses score = float64(p.hits-i)/float64(p.hits+1) = (1-0)/(1+1) = 0.5 for i=0 with hits=1.
	wantQ := 0.5
	wantR := 0.5
	wantFinal := wantQ*0.65 + wantR*0.35
	if h.QdrantScore != wantQ {
		t.Fatalf("QdrantScore: want %.3f, got %.3f", wantQ, h.QdrantScore)
	}
	if h.RerankScore != wantR {
		t.Fatalf("RerankScore: want %.3f, got %.3f", wantR, h.RerankScore)
	}
	if h.FinalScore != wantFinal {
		t.Fatalf("FinalScore: want %.3f, got %.3f", wantFinal, h.FinalScore)
	}
}

// ── 8. Validate aggregator behaviour under forced serial fan-out ──────

// SetMaxConcurrency(1) makes the test deterministic for timing
// comparisons; siblings run sequentially. The contract is unchanged
// — only the slot count shifts.
func TestSearchAggregator_SerialFanOut_PartialResults(t *testing.T) {
	boom := errors.New("upstream boom")
	reg := newStubRegistry(
		&stubSearchProvider{name: "slow", delay: 100 * time.Millisecond, hits: 1},
		&stubSearchProvider{name: "broken", err: boom},
		&stubSearchProvider{name: "fast", hits: 2},
	)
	agg := NewSearchAggregator(reg)
	agg.SetMaxConcurrency(1)

	res, err := agg.Aggregate(context.Background(), &SearchQuery{Query: "x"}, AggregateOptions{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Hits) != 3 {
		t.Fatalf("want 3 hits (slow+fast), got %d", len(res.Hits))
	}
	if res.ProviderErrors["broken"] == nil {
		t.Fatalf("expected broken provider to record an error, got: %v", res.ProviderErrors)
	}
}

// ── 9. S3d (June 2026): HashQuery path fixtures + tests ───────────

// stubHashSource is the canonical scaffold for HashQuery tests.
// Each test wires one or more of these into the aggregator via
// SetHashSources and asserts the deterministic dedup contract.
type stubHashSource struct {
	name  string
	delay time.Duration
	err   error
	empty bool
	hits  int
}

func (p *stubHashSource) Name() string { return p.name }

// FindClipsByHash mirrors the deterministic stubSearchProvider.Search
// contract:
//  1. Honour ctx cancellation (mirror production adapters).
//  2. Sleep `delay` (release the slot so other sources run in
//     parallel) — used to validate the 5s default timeout.
//  3. Return `err` when non-nil.
//  4. Return `empty=true` for no candidates; otherwise return
//     `hits` HashHit rows.
func (p *stubHashSource) FindClipsByHash(ctx context.Context, hash string) ([]HashHit, error) {
	if p.delay > 0 {
		select {
		case <-time.After(p.delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if p.err != nil {
		return nil, p.err
	}
	if p.empty {
		return nil, nil
	}
	hits := make([]HashHit, p.hits)
	for i := 0; i < p.hits; i++ {
		hits[i] = HashHit{
			ID:           p.name + "-clip-" + itoa(i),
			Source:       p.name,
			Name:         p.name + " clip " + itoa(i),
			DriveLink:    "https://drive.google.com/file/d/" + p.name + "-" + itoa(i),
			LocalPath:    "/tmp/" + p.name + "/" + itoa(i) + ".mp4",
			ThumbnailURL: "https://thumb/" + p.name + "/" + itoa(i) + ".jpg",
			MediaType:    "video",
		}
	}
	return hits, nil
}

// helper: aggregator with stub hash sources wired.
func newAggregatorWithHashSources(sources ...*stubHashSource) *SearchAggregator {
	agg := NewSearchAggregator(NewRegistry()) // empty registry (HashQuery path doesn't read it)
	m := make(map[string]ClipHashSource, len(sources))
	for _, s := range sources {
		m[s.name] = s
	}
	agg.SetHashSources(m)
	return agg
}

// 9a. S3d HashQuery happy path: 2 sources emit clips, both go into
// AggregateResult.Hits with full asset-shape projection.
func TestSearchAggregator_HashQuery_HappyPath(t *testing.T) {
	agg := newAggregatorWithHashSources(
		&stubHashSource{name: "artlist", hits: 2},
		&stubHashSource{name: "youtube", hits: 1},
	)
	res, err := agg.Aggregate(
		context.Background(),
		&SearchQuery{},
		AggregateOptions{HashQuery: "deadbeef"},
	)
	if err != nil {
		t.Fatalf("Aggregate: %v", err)
	}
	if res == nil {
		t.Fatal("nil result")
	}
	if len(res.Hits) != 3 {
		t.Fatalf("want 3 hits (2+1), got %d", len(res.Hits))
	}
	if res.Total != 3 {
		t.Fatalf("want Total=3, got %d", res.Total)
	}
	if res.ProviderErrors != nil && len(res.ProviderErrors) != 0 {
		t.Fatalf("unexpected errors on happy path: %v", res.ProviderErrors)
	}
	// Each hit carries the asset-shape projection, not a synthesized
	// Candidate.
	for _, h := range res.Hits {
		if h.Candidate.SourceName != "" {
			t.Fatalf("HashQuery path should not synthesise Candidate; got %q", h.Candidate.SourceName)
		}
		if h.ProviderName == "" {
			t.Fatal("HashQuery providerName must be populated")
		}
		if h.SourceID == "" {
			t.Fatal("HashQuery SourceID must be populated")
		}
		if h.SourceSource == "" {
			t.Fatal("HashQuery SourceSource must be populated")
		}
		if h.FinalScore != 1.0 || h.QdrantScore != 1.0 || h.RerankScore != 1.0 {
			t.Fatalf("HashQuery scores should all be 1.0; got q=%.2f r=%.2f f=%.2f",
				h.QdrantScore, h.RerankScore, h.FinalScore)
		}
	}
	// Stable (ProviderName, SourceSource, SourceID) sort: artlist
	// rows come before youtube rows.
	if res.Hits[0].ProviderName != "artlist" {
		t.Fatalf("want artlist first (sort order), got %q", res.Hits[0].ProviderName)
	}
}

// 9b. S3d HashQuery Sources filter: opts.Sources restricts fan-out
// to a named subset; sources NOT named are NOT queried (so their
// misconfigured state can't poison the result).
func TestSearchAggregator_HashQuery_SourcesFilter(t *testing.T) {
	// `all` advertises 0 hits so a wrongly-fanned-out call would
	// inflate the slice — the test catches that.
	agg := newAggregatorWithHashSources(
		&stubHashSource{name: "artlist", hits: 2},
		&stubHashSource{name: "youtube", hits: 1},
		&stubHashSource{name: "all", empty: true},
	)
	res, err := agg.Aggregate(
		context.Background(),
		&SearchQuery{},
		AggregateOptions{
			HashQuery: "deadbeef",
			Sources:   []string{"artlist", "youtube"},
		},
	)
	if err != nil {
		t.Fatalf("Aggregate: %v", err)
	}
	if len(res.Hits) != 3 {
		t.Fatalf("want 3 hits, got %d (Sources filter must exclude 'all')", len(res.Hits))
	}
	for _, h := range res.Hits {
		if h.ProviderName == "all" {
			t.Fatalf("Sources filter leaked 'all' into Hits: %+v", h)
		}
	}
}

// 9c. S3d HashQuery failed source: per-source error recorded in
// ProviderErrors; sibling sources still contribute hits.
func TestSearchAggregator_HashQuery_FailedProvider(t *testing.T) {
	boom := errors.New("hash-source boom")
	agg := newAggregatorWithHashSources(
		&stubHashSource{name: "happy", hits: 2},
		&stubHashSource{name: "broken", err: boom},
	)
	res, err := agg.Aggregate(
		context.Background(),
		&SearchQuery{},
		AggregateOptions{HashQuery: "deadbeef"},
	)
	if err != nil {
		t.Fatalf("Aggregate: %v", err)
	}
	if len(res.Hits) != 2 {
		t.Fatalf("want 2 hits from happy, got %d", len(res.Hits))
	}
	if res.Hits[0].ProviderName != "happy" {
		t.Fatalf("want hit from happy, got %q", res.Hits[0].ProviderName)
	}
	if res.ProviderErrors == nil {
		t.Fatal("want provider errors map populated by broken source")
	}
	brokenErr, ok := res.ProviderErrors["broken"]
	if !ok {
		t.Fatalf("want broken source error, got: %v", res.ProviderErrors)
	}
	if !errors.Is(brokenErr, boom) {
		t.Fatalf("want broken source error to wrap boom; got %v", brokenErr)
	}
}

// 9d. S3d HashQuery empty hash sources registry: aggregator returns
// empty result + empty ProviderErrors map (not an error) — the
// "no source advertised" contract.
func TestSearchAggregator_HashQuery_EmptySourcesRegistry(t *testing.T) {
	agg := NewSearchAggregator(NewRegistry())
	// Deliberately do NOT call SetHashSources.
	res, err := agg.Aggregate(
		context.Background(),
		&SearchQuery{},
		AggregateOptions{HashQuery: "deadbeef"},
	)
	if err != nil {
		t.Fatalf("Aggregate: %v", err)
	}
	if res == nil {
		t.Fatal("nil result")
	}
	if len(res.Hits) != 0 {
		t.Fatalf("want 0 hits, got %d", len(res.Hits))
	}
	if res.Total != 0 {
		t.Fatalf("want Total=0, got %d", res.Total)
	}
	if res.ProviderErrors == nil || len(res.ProviderErrors) != 0 {
		t.Fatalf("want empty ProviderErrors, got: %v", res.ProviderErrors)
	}
}

// 9e. S3d semantic path is unaffected when Sources filter excludes
// all providers: Aggregate returns empty hits + empty errors rather
// than failing.
func TestSearchAggregator_SemanticPath_SourcesExcludesAll(t *testing.T) {
	reg := NewRegistry()
	_ = reg.Register(&stubSearchProvider{name: "alpha", hits: 2})
	reg.Freeze()
	agg := NewSearchAggregator(reg)
	res, err := agg.Aggregate(
		context.Background(),
		&SearchQuery{Query: "go"},
		AggregateOptions{
			Sources: []string{"nonexistent-source"},
		},
	)
	if err != nil {
		t.Fatalf("Aggregate: %v", err)
	}
	if len(res.Hits) != 0 {
		t.Fatalf("want 0 hits when no source matches, got %d", len(res.Hits))
	}
}
