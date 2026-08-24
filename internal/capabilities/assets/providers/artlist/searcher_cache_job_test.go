package assets

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs/queue"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
	"go.uber.org/zap"
)

type cacheRefreshEnqueuerStub struct {
	requests []*job.EnqueueRequest
	err      error
}

func (s *cacheRefreshEnqueuerStub) Enqueue(_ context.Context, req *job.EnqueueRequest) (*job.Job, error) {
	s.requests = append(s.requests, req)
	if s.err != nil {
		return nil, s.err
	}
	return &job.Job{ID: "refresh-job-1", Type: req.Type, Status: job.StatusQueued}, nil
}

type cacheRefreshSearcherStub struct {
	candidates []Candidate
	err        error
	requests   []SearchRequest
}

func (s *cacheRefreshSearcherStub) Search(_ context.Context, req SearchRequest) ([]Candidate, error) {
	s.requests = append(s.requests, req)
	return s.candidates, s.err
}

type cacheRefreshJobBrokerStub struct{ job.JobBroker }

func TestCachedSearcher_StaleEntryEnqueuesDurableRefresh(t *testing.T) {
	cache := newTestCache()
	cache.mu.Lock()
	cache.items["mountain river"] = liveSearchCacheEntry{
		Clips:    []Candidate{{ID: "cached"}},
		CachedAt: time.Now().Add(-50 * time.Minute),
	}
	cache.mu.Unlock()

	provider := &cacheRefreshSearcherStub{candidates: []Candidate{{ID: "live"}}}
	enqueuer := &cacheRefreshEnqueuerStub{}
	searcher := NewCachedSearcher(provider, cache, 1, zap.NewNop(), enqueuer)

	got, err := searcher.Search(context.Background(), SearchRequest{Term: "mountain river", Limit: 4})
	if err != nil {
		t.Fatalf("Search returned error: %v", err)
	}
	if len(got) != 1 || got[0].ID != "cached" {
		t.Fatalf("expected stale cached result, got %#v", got)
	}
	if len(provider.requests) != 0 {
		t.Fatalf("stale HTTP request must not call provider on the request path, got %d calls", len(provider.requests))
	}
	if len(enqueuer.requests) != 1 {
		t.Fatalf("expected one durable refresh enqueue, got %d", len(enqueuer.requests))
	}

	req := enqueuer.requests[0]
	if req.Type != media.TypeArtlistCacheRefresh {
		t.Fatalf("refresh type = %q, want %q", req.Type, media.TypeArtlistCacheRefresh)
	}
	if req.ActiveKey != "artlist-cache-refresh:mountain river" {
		t.Fatalf("active key = %q, want canonical term key", req.ActiveKey)
	}
	payload, ok := req.Payload.(appjobs.ArtlistCacheRefreshPayload)
	if !ok {
		t.Fatalf("payload type = %T, want ArtlistCacheRefreshPayload", req.Payload)
	}
	if payload.Term != "mountain river" || payload.Limit != 50 || !payload.PreferRemote {
		t.Fatalf("unexpected refresh payload: %#v", payload)
	}
}

func TestCachedSearcher_EnqueueFailureDoesNotBreakStaleHit(t *testing.T) {
	cache := newTestCache()
	cache.mu.Lock()
	cache.items["term"] = liveSearchCacheEntry{
		Clips:    []Candidate{{ID: "cached"}},
		CachedAt: time.Now().Add(-50 * time.Minute),
	}
	cache.mu.Unlock()

	enqueuer := &cacheRefreshEnqueuerStub{err: errors.New("queue unavailable")}
	searcher := NewCachedSearcher(&cacheRefreshSearcherStub{}, cache, 1, zap.NewNop(), enqueuer)
	got, err := searcher.Search(context.Background(), SearchRequest{Term: "term"})
	if err != nil {
		t.Fatalf("stale cache hit should remain available when enqueue fails: %v", err)
	}
	if len(got) != 1 || got[0].ID != "cached" {
		t.Fatalf("expected cached result after enqueue failure, got %#v", got)
	}
}

func TestJobAdapter_HandleCacheRefresh_UpdatesCache(t *testing.T) {
	provider := &cacheRefreshSearcherStub{candidates: []Candidate{{ID: "fresh-1"}, {ID: "fresh-2"}}}
	cache := newTestCache()
	adapter := NewJobAdapter(&Service{
		scraperSearcher: provider,
		liveCache:       cache,
		log:             zap.NewNop(),
	})
	payload, err := json.Marshal(appjobs.ArtlistCacheRefreshPayload{Term: "  Mountain River ", Limit: 2, PreferRemote: false})
	if err != nil {
		t.Fatal(err)
	}

	result, err := adapter.HandleCacheRefresh(context.Background(), &job.Job{
		ID:      "refresh-job-1",
		Type:    media.TypeArtlistCacheRefresh,
		Payload: payload,
	}, nil)
	if err != nil {
		t.Fatalf("HandleCacheRefresh returned error: %v", err)
	}
	if result["term"] != "mountain river" || result["clips"] != 2 {
		t.Fatalf("unexpected result: %#v", result)
	}
	if len(provider.requests) != 1 {
		t.Fatalf("provider calls = %d, want 1", len(provider.requests))
	}
	if !provider.requests[0].ForceRefresh || provider.requests[0].PreferRemote {
		t.Fatalf("worker must force refresh while honoring payload PreferRemote=false: %#v", provider.requests[0])
	}
	got, ok := cache.get("MOUNTAIN RIVER")
	if !ok || len(got) != 2 || got[0].ID != "fresh-1" {
		t.Fatalf("cache was not updated by worker: %#v, %t", got, ok)
	}
}

func TestJobAdapter_HandleCacheRefresh_ReturnsRetryableError(t *testing.T) {
	provider := &cacheRefreshSearcherStub{err: errors.New("provider unavailable")}
	adapter := NewJobAdapter(&Service{
		scraperSearcher: provider,
		liveCache:       newTestCache(),
		log:             zap.NewNop(),
	})
	payload, err := json.Marshal(appjobs.ArtlistCacheRefreshPayload{Term: "mountain"})
	if err != nil {
		t.Fatal(err)
	}

	_, err = adapter.HandleCacheRefresh(context.Background(), &job.Job{
		ID:      "refresh-job-1",
		Type:    media.TypeArtlistCacheRefresh,
		Payload: payload,
	}, nil)
	if err == nil {
		t.Fatal("expected provider error so the job runner can apply retry policy")
	}
	if !strings.Contains(err.Error(), "provider unavailable") {
		t.Fatalf("expected a descriptive provider error, got %v", err)
	}
}

func TestJobAdapter_RegisterHandler_BindsCacheRefreshAndPolicy(t *testing.T) {
	registry := appjobs.Compose()
	dispatcher := appjobs.NewDispatcher()
	jobsService, err := appjobs.NewService(cacheRefreshJobBrokerStub{}, dispatcher, zap.NewNop(), registry)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	service := &Service{jobAdapter: nil}
	service.jobAdapter = NewJobAdapter(service)
	if err := service.RegisterHandler(jobsService); err != nil {
		t.Fatalf("RegisterHandler: %v", err)
	}
	if !jobsService.HasHandler(media.TypeArtlistRun) {
		t.Fatal("media.artlist handler was not registered")
	}
	if !jobsService.HasHandler(media.TypeArtlistCacheRefresh) {
		t.Fatal("media.artlist_cache_refresh handler was not registered")
	}
	policy, ok := registry.Get(media.TypeArtlistCacheRefresh)
	if !ok {
		t.Fatal("cache refresh policy was not registered")
	}
	if policy.DefaultMaxRetries != 3 {
		t.Fatalf("cache refresh max retries = %d, want 3", policy.DefaultMaxRetries)
	}

	// Dispatch through the same dispatcher surface used by workers. A
	// malformed payload must return an error, which the worker translates
	// into retry/dead-letter lifecycle transitions according to the policy.
	badJob := &job.Job{ID: "refresh-job-bad", Type: media.TypeArtlistCacheRefresh, Payload: []byte("{")}
	if _, err := dispatcher.Dispatch(context.Background(), badJob, nil); err == nil {
		t.Fatal("dispatcher must invoke the cache refresh handler and surface malformed payload errors")
	}
}
