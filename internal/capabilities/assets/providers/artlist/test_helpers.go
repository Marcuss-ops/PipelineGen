package assets

import (
	"context"
	"sync"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// fakeDetailFetcher is a test-only implementation of the DetailFetcher port.
type fakeDetailFetcher struct {
	candidate *Candidate
	err       error
	calledURL string
}

func (f *fakeDetailFetcher) FetchDetails(_ context.Context, clipPageURL string) (*Candidate, error) {
	f.calledURL = clipPageURL
	if f.err != nil {
		return nil, f.err
	}
	return f.candidate, nil
}

// blockingDetailFetcher is a test helper that blocks each fetch until the
// release channel is closed. It lets tests assert that multiple fetches are
// started concurrently.
type blockingDetailFetcher struct {
	mu             sync.Mutex
	candidateByURL map[string]*Candidate
	started        chan string
	release        <-chan struct{}
	active         int
	maxActive      int
}

func (f *blockingDetailFetcher) FetchDetails(_ context.Context, clipPageURL string) (*Candidate, error) {
	f.mu.Lock()
	f.active++
	if f.active > f.maxActive {
		f.maxActive = f.active
	}
	started := f.started
	release := f.release
	candidate := f.candidateByURL[clipPageURL]
	f.mu.Unlock()

	if started != nil {
		select {
		case started <- clipPageURL:
		default:
		}
	}
	if release != nil {
		<-release
	}

	f.mu.Lock()
	f.active--
	f.mu.Unlock()

	if candidate != nil {
		return candidate, nil
	}
	return &Candidate{ID: clipPageURL, Title: clipPageURL}, nil
}

// fakeDispatcherForImport records SaveDiscoveredAsset calls.
type fakeDispatcherForImport struct {
	mu                  sync.Mutex
	saved               *asset.Asset
	saveDiscoveredCalls int
	saveDiscoveredErr   error
}

func (f *fakeDispatcherForImport) EnqueueAndIndex(_ context.Context, _ *asset.Asset, _ string) error {
	return nil
}

func (f *fakeDispatcherForImport) SaveDiscoveredAsset(_ context.Context, clip *asset.Asset, _ asset.LifecycleState, _ asset.IndexState) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.saved = clip
	f.saveDiscoveredCalls++
	return f.saveDiscoveredErr
}

func (f *fakeDispatcherForImport) EnqueueAndRestore(_ context.Context, _ string) error { return nil }
func (f *fakeDispatcherForImport) EnqueueAndDelete(_ context.Context, _ string) error  { return nil }
