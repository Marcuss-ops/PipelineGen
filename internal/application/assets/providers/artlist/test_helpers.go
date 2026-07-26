package artlist

import (
	"context"
	"sync"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
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
