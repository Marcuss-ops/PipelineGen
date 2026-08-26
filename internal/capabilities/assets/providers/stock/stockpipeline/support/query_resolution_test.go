package support

import (
	stockpipeline "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers/stock/stockpipeline"
	"context"
	"errors"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

type queryResolutionLister struct {
	mu        sync.Mutex
	active    int
	maxActive int
	calls     []string
	completed []string
	results   map[string][]stockpipeline.VideoInfo
	errors    map[string]error
	delays    map[string]time.Duration
	delay     time.Duration
	wait      bool
}

func (f *queryResolutionLister) ListChannel(ctx context.Context, channelURL string, _ int) ([]stockpipeline.VideoInfo, error) {
	f.mu.Lock()
	f.active++
	if f.active > f.maxActive {
		f.maxActive = f.active
	}
	f.calls = append(f.calls, channelURL)
	f.mu.Unlock()
	defer func() {
		f.mu.Lock()
		f.active--
		f.mu.Unlock()
	}()

	if f.wait {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	query := channelURL
	if len(query) >= len("ytsearch25:") && query[:len("ytsearch25:")] == "ytsearch25:" {
		query = query[len("ytsearch25:"):]
	}
	waitFor := f.delay
	if specific, ok := f.delays[query]; ok {
		waitFor = specific
	}
	if waitFor > 0 {
		timer := time.NewTimer(waitFor)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	f.mu.Lock()
	f.completed = append(f.completed, query)
	f.mu.Unlock()
	if err := f.errors[query]; err != nil {
		return nil, err
	}
	return f.results[query], nil
}

func (f *queryResolutionLister) snapshot() (maxActive int, calls, completed []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.maxActive, append([]string(nil), f.calls...), append([]string(nil), f.completed...)
}

func newQueryResolutionService(lister stockpipeline.ChannelLister) *stockpipeline.Service {
	return &stockpipeline.Service{
		runtime:       &stockpipeline.RuntimeConfig{MaxResults: 25},
		log:           zap.NewNop(),
		channelLister: lister,
	}
}

func videoInfo(id string) stockpipeline.VideoInfo {
	return stockpipeline.VideoInfo{ID: id, Title: id}
}

func TestResolveInputQueries_BoundedDeterministicAndDeduplicated(t *testing.T) {
	lister := &queryResolutionLister{
		results: map[string][]stockpipeline.VideoInfo{
			"query-a": {videoInfo("a"), videoInfo("shared")},
			"query-b": {videoInfo("b"), videoInfo("shared")},
			"query-c": {videoInfo("c")},
			"query-d": {videoInfo("d")},
			"query-e": {videoInfo("e")},
		},
		errors: make(map[string]error),
		delays: map[string]time.Duration{
			"query-a": 50 * time.Millisecond,
			"query-b": 5 * time.Millisecond,
			"query-c": 10 * time.Millisecond,
		},
	}
	svc := newQueryResolutionService(lister)
	input := &stockpipeline.RunInput{
		SearchQueries: []string{"query-a", "query-b", "query-c", "query-d", "query-e"},
		DirectURLs:    []string{" https://direct.example/video.mp4 ", "https://www.youtube.com/watch?v=shared"},
	}

	require.NoError(t, svc.resolveInputQueries(context.Background(), input))
	require.Empty(t, input.SearchQueries)
	require.Equal(t, []string{
		"https://direct.example/video.mp4",
		"https://www.youtube.com/watch?v=shared",
		"https://www.youtube.com/watch?v=a",
		"https://www.youtube.com/watch?v=b",
		"https://www.youtube.com/watch?v=c",
		"https://www.youtube.com/watch?v=d",
		"https://www.youtube.com/watch?v=e",
	}, input.DirectURLs)

	maxActive, calls, completed := lister.snapshot()
	require.Greater(t, maxActive, 1, "multiple search queries must run concurrently")
	require.LessOrEqual(t, maxActive, maxSearchQueryWorkers)
	require.Len(t, calls, 5)
	require.NotEqual(t, []string{"query-a", "query-b", "query-c", "query-d", "query-e"}, completed,
		"provider completion should be out of query order in this bounded-concurrency test")
	// Provider completion order is intentionally unconstrained, while the
	// resolved output above must remain in query order. Calls only assert
	// that every query was submitted once (the provider receives ytsearchN).
	sort.Strings(calls)
	for _, query := range []string{"query-a", "query-b", "query-c", "query-d", "query-e"} {
		require.Contains(t, calls, "ytsearch25:"+query)
	}
}

func TestResolveInputQueries_PropagatesParentCancellation(t *testing.T) {
	lister := &queryResolutionLister{
		results: make(map[string][]stockpipeline.VideoInfo),
		errors:  make(map[string]error),
		wait:    true,
	}
	svc := newQueryResolutionService(lister)
	input := &stockpipeline.RunInput{SearchQueries: []string{"a", "b", "c", "d"}}
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() { done <- svc.resolveInputQueries(ctx, input) }()
	deadline := time.After(2 * time.Second)
	for {
		_, calls, _ := lister.snapshot()
		if len(calls) > 0 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("search worker did not start")
		case <-time.After(time.Millisecond):
		}
	}
	cancel()

	select {
	case err := <-done:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(2 * time.Second):
		t.Fatal("resolveInputQueries did not return after cancellation")
	}
	maxActive, _, _ := lister.snapshot()
	require.LessOrEqual(t, maxActive, maxSearchQueryWorkers)
}

func TestResolveInputQueries_PropagatesContextProviderErrorWithoutCancellingSiblings(t *testing.T) {
	providerErr := context.DeadlineExceeded
	lister := &queryResolutionLister{
		results: map[string][]stockpipeline.VideoInfo{"good": {videoInfo("good")}},
		errors:  map[string]error{"bad": providerErr},
		delay:   5 * time.Millisecond,
	}
	svc := newQueryResolutionService(lister)
	input := &stockpipeline.RunInput{SearchQueries: []string{"bad", "good"}}

	require.NoError(t, svc.resolveInputQueries(context.Background(), input))
	require.Equal(t, []string{"https://www.youtube.com/watch?v=good"}, input.DirectURLs)
}

func TestResolveInputQueries_AllFailuresWithDirectURLRemainUsable(t *testing.T) {
	providerErr := errors.New("provider unavailable")
	lister := &queryResolutionLister{
		results: make(map[string][]stockpipeline.VideoInfo),
		errors:  map[string]error{"bad": providerErr},
	}
	svc := newQueryResolutionService(lister)
	input := &stockpipeline.RunInput{
		SearchQueries: []string{"bad"},
		DirectURLs:    []string{" https://direct.example/video.mp4 ", "https://direct.example/video.mp4"},
	}

	require.NoError(t, svc.resolveInputQueries(context.Background(), input))
	require.Equal(t, []string{"https://direct.example/video.mp4"}, input.DirectURLs)
}

func TestResolveInputQueries_AllFailuresReturnTypedError(t *testing.T) {
	providerErr := errors.New("provider unavailable")
	lister := &queryResolutionLister{
		results: make(map[string][]stockpipeline.VideoInfo),
		errors:  map[string]error{"bad-a": providerErr, "bad-b": providerErr},
	}
	svc := newQueryResolutionService(lister)
	input := &stockpipeline.RunInput{SearchQueries: []string{"bad-a", "bad-b"}}

	err := svc.resolveInputQueries(context.Background(), input)
	require.ErrorIs(t, err, ErrStockPipelineAllQueriesFailed)
	require.Empty(t, input.DirectURLs)
	require.Empty(t, input.SearchQueries)
}

func TestResolveInputQueries_NilInputsAreNoOp(t *testing.T) {
	var svc *stockpipeline.Service
	require.NoError(t, svc.resolveInputQueries(context.Background(), nil))
}

func TestResolveInputQueries_HonorsPerQueryLimit(t *testing.T) {
	lister := &queryResolutionLister{
		results: make(map[string][]stockpipeline.VideoInfo),
		errors:  make(map[string]error),
	}
	svc := newQueryResolutionService(lister)
	input := &stockpipeline.RunInput{
		SearchQueries:     []string{"a", "b"},
		SearchQueryLimits: []int{1, 3},
	}

	require.NoError(t, svc.resolveInputQueries(context.Background(), input))
	_, calls, _ := lister.snapshot()
	require.Len(t, calls, 2)
	require.Contains(t, calls, "ytsearch1:a", "limit:1 must win over the runtime default")
	require.Contains(t, calls, "ytsearch3:b", "limit:3 must win over the runtime default")
}

func TestResolveInputQueries_DefaultsWhenLimitsMissing(t *testing.T) {
	// A shorter limits slice (or no limits at all) must fall back to the
	// runtime default (25) per query rather than zeroing the fan-out.
	lister := &queryResolutionLister{
		results: map[string][]stockpipeline.VideoInfo{"a": {videoInfo("a")}},
		errors:  make(map[string]error),
	}
	svc := newQueryResolutionService(lister)
	input := &stockpipeline.RunInput{
		SearchQueries:     []string{"a", "b"},
		SearchQueryLimits: []int{1}, // shorter than queries
	}

	require.NoError(t, svc.resolveInputQueries(context.Background(), input))
	_, calls, _ := lister.snapshot()
	require.Contains(t, calls, "ytsearch1:a", "explicit limit honoured")
	require.Contains(t, calls, "ytsearch25:b", "missing limit falls back to runtime default")
}
