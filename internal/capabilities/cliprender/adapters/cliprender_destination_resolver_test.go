package adapters

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/delivery"
	"go.uber.org/zap"
)

// fakeDeliveryPublisher is a minimal delivery.Publisher double recording
// every Publish/ResolveFolder request. resolveOut controls ResolveFolder's
// return value; resolveErr injects a resolution failure.
type fakeDeliveryPublisher struct {
	mu         sync.Mutex
	requests   []delivery.PublishRequest
	resolved   []delivery.PublishRequest
	resolveOut string
	resolveErr error
	// resolveGate, when non-nil, holds ResolveFolder open until the channel is
	// closed — forcing concurrent callers to overlap so dedup must be real.
	resolveGate chan struct{}
}

func (f *fakeDeliveryPublisher) Publish(_ context.Context, req delivery.PublishRequest) (*delivery.PublishResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, req)
	return &delivery.PublishResult{
		FileID:       "file-001",
		WebViewLink:  "https://drive.google.com/file/d/file-001/view",
		DownloadLink: "https://drive.google.com/uc?id=file-001",
		MD5Checksum:  "md5-001",
		FolderID:     req.DestinationFolderID,
		Destination:  req.Destination,
		Action:       delivery.PublishActionCreated,
	}, nil
}

func (f *fakeDeliveryPublisher) ResolveFolder(_ context.Context, req delivery.PublishRequest) (string, error) {
	f.mu.Lock()
	f.resolved = append(f.resolved, req)
	out, err := f.resolveOut, f.resolveErr
	gate := f.resolveGate
	f.mu.Unlock()
	if gate != nil {
		<-gate // resolution stays open until the test releases it
	}
	if err != nil {
		return "", err
	}
	return out, nil
}

func (f *fakeDeliveryPublisher) publishRequests() []delivery.PublishRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]delivery.PublishRequest(nil), f.requests...)
}

func (f *fakeDeliveryPublisher) resolveRequests() []delivery.PublishRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]delivery.PublishRequest(nil), f.resolved...)
}

// TestClipRenderDestinationFolderResolver_SanitizesAndResolves verifies the
// adapter routes root + subfolder name through delivery.Publisher.ResolveFolder
// with the folder name pre-sanitised via textutil.SafeName (the canonical
// form the FolderManager matches exactly) and returns the resolved leaf ID.
func TestClipRenderDestinationFolderResolver_SanitizesAndResolves(t *testing.T) {
	drive := &fakeDeliveryPublisher{resolveOut: "leaf-folder-abc123"}
	r, err := NewClipRenderDestinationFolderResolver(drive, zap.NewNop())
	if err != nil {
		t.Fatalf("NewClipRenderDestinationFolderResolver() error = %v", err)
	}

	folderID, err := r.ResolveDestinationFolder(context.Background(), cliprender.DestinationFolderResolveInput{
		RootFolderID:  "root-folder-xyz",
		SubfolderName: "Matt Damon 5 Clips Verification",
	})
	if err != nil {
		t.Fatalf("ResolveDestinationFolder() error = %v", err)
	}
	if folderID != "leaf-folder-abc123" {
		t.Fatalf("folder ID = %q, want leaf-folder-abc123", folderID)
	}
	reqs := drive.resolveRequests()
	if len(reqs) != 1 {
		t.Fatalf("ResolveFolder calls = %d, want 1", len(reqs))
	}
	req := reqs[0]
	if req.DestinationFolderID != "root-folder-xyz" {
		t.Errorf("root folder = %q, want root-folder-xyz", req.DestinationFolderID)
	}
	want := []string{"Matt Damon 5 Clips Verification"}
	if len(req.DestinationSubpath) != 1 || req.DestinationSubpath[0] != want[0] {
		t.Errorf("subpath = %v, want %v (SafeName preserves letters/digits/spaces)", req.DestinationSubpath, want)
	}
	if req.Destination != delivery.DestinationClipMetadata {
		t.Errorf("destination = %q, want clip_metadata", req.Destination)
	}
}

// TestClipRenderDestinationFolderResolver_FailClosed pins the two
// fail-closed guards: empty root is rejected before any Drive call, and a
// ResolveFolder returning an empty ID is a typed error — never a silent root
// fallback.
func TestClipRenderDestinationFolderResolver_FailClosed(t *testing.T) {
	drive := &fakeDeliveryPublisher{}
	r, err := NewClipRenderDestinationFolderResolver(drive, zap.NewNop())
	if err != nil {
		t.Fatalf("NewClipRenderDestinationFolderResolver() error = %v", err)
	}

	if _, err := r.ResolveDestinationFolder(context.Background(), cliprender.DestinationFolderResolveInput{
		RootFolderID:  "",
		SubfolderName: "Some Script",
	}); err == nil {
		t.Fatal("empty root folder must fail closed")
	}

	drive.resolveOut = "" // resolver returned no folder ID
	if _, err := r.ResolveDestinationFolder(context.Background(), cliprender.DestinationFolderResolveInput{
		RootFolderID:  "root-folder-xyz",
		SubfolderName: "Some Script",
	}); err == nil {
		t.Fatal("empty resolved folder ID must fail closed (never a silent root fallback)")
	}
}

// TestClipRenderDestinationFolderResolver_NilPublisher verifies the
// constructor fails closed at composition time when no delivery.Publisher is
// wired.
func TestClipRenderDestinationFolderResolver_NilPublisher(t *testing.T) {
	if _, err := NewClipRenderDestinationFolderResolver(nil, zap.NewNop()); err == nil {
		t.Fatal("NewClipRenderDestinationFolderResolver(nil) must fail closed")
	}
}

// TestClipRenderDestinationFolderResolver_MemoizesRepeatedResolution pins the
// batch memoization contract: N sequential jobs carrying the same
// subfolder_name cost exactly ONE Drive create-or-reuse round-trip (the
// historical behavior repeated it once per job).
func TestClipRenderDestinationFolderResolver_MemoizesRepeatedResolution(t *testing.T) {
	drive := &fakeDeliveryPublisher{resolveOut: "leaf-folder-memo"}
	r, err := NewClipRenderDestinationFolderResolver(drive, zap.NewNop())
	if err != nil {
		t.Fatalf("NewClipRenderDestinationFolderResolver() error = %v", err)
	}

	in := cliprender.DestinationFolderResolveInput{RootFolderID: "root-folder-xyz", SubfolderName: "Some Script"}
	for i := 0; i < 3; i++ {
		folderID, err := r.ResolveDestinationFolder(context.Background(), in)
		if err != nil {
			t.Fatalf("ResolveDestinationFolder() call %d error = %v", i, err)
		}
		if folderID != "leaf-folder-memo" {
			t.Fatalf("folder ID = %q, want leaf-folder-memo", folderID)
		}
	}
	if got := len(drive.resolveRequests()); got != 1 {
		t.Fatalf("ResolveFolder calls = %d, want 1 (one create-or-reuse per root+name)", got)
	}
}

// TestClipRenderDestinationFolderResolver_ConcurrentMissesShareOneResolve
// pins the concurrency contract: jobs of one batch may run in parallel and
// their simultaneous resolution misses MUST collapse into exactly one Drive
// round-trip (singleflight), all sharers receiving the same folder ID.
func TestClipRenderDestinationFolderResolver_ConcurrentMissesShareOneResolve(t *testing.T) {
	gate := make(chan struct{})
	drive := &fakeDeliveryPublisher{resolveOut: "leaf-folder-shared", resolveGate: gate}
	r, err := NewClipRenderDestinationFolderResolver(drive, zap.NewNop())
	if err != nil {
		t.Fatalf("NewClipRenderDestinationFolderResolver() error = %v", err)
	}

	in := cliprender.DestinationFolderResolveInput{RootFolderID: "root-folder-xyz", SubfolderName: "Some Script"}
	const jobs = 8
	var wg sync.WaitGroup
	errs := make(chan error, jobs)
	for i := 0; i < jobs; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			folderID, err := r.ResolveDestinationFolder(context.Background(), in)
			if err == nil && folderID != "leaf-folder-shared" {
				err = fmt.Errorf("folder ID = %q, want leaf-folder-shared", folderID)
			}
			errs <- err
		}()
	}
	// Keep the single Drive resolution open long enough that the other jobs'
	// misses must dedup into it, then release.
	time.Sleep(20 * time.Millisecond)
	close(gate)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if got := len(drive.resolveRequests()); got != 1 {
		t.Fatalf("ResolveFolder calls = %d, want 1 (concurrent misses share one flight)", got)
	}
}

// TestClipRenderDestinationFolderResolver_MemoExpiresAfterTTL pins the
// staleness bound: a memoized leaf ID is reused only within the TTL window,
// after which the next call re-resolves create-or-reuse (self-healing against
// a folder deleted externally).
func TestClipRenderDestinationFolderResolver_MemoExpiresAfterTTL(t *testing.T) {
	drive := &fakeDeliveryPublisher{resolveOut: "leaf-folder-ttl"}
	r, err := NewClipRenderDestinationFolderResolver(drive, zap.NewNop())
	if err != nil {
		t.Fatalf("NewClipRenderDestinationFolderResolver() error = %v", err)
	}
	now := time.Unix(1_700_000_000, 0)
	r.now = func() time.Time { return now }

	in := cliprender.DestinationFolderResolveInput{RootFolderID: "root-folder-xyz", SubfolderName: "Some Script"}
	if _, err := r.ResolveDestinationFolder(context.Background(), in); err != nil {
		t.Fatalf("ResolveDestinationFolder() error = %v", err)
	}
	now = now.Add(r.ttl / 2) // inside the window: memo hit
	if _, err := r.ResolveDestinationFolder(context.Background(), in); err != nil {
		t.Fatalf("ResolveDestinationFolder() error = %v", err)
	}
	if got := len(drive.resolveRequests()); got != 1 {
		t.Fatalf("ResolveFolder calls = %d, want 1 (fresh memo hit inside TTL)", got)
	}
	now = now.Add(r.ttl/2 + time.Nanosecond) // past the window: re-resolve
	if _, err := r.ResolveDestinationFolder(context.Background(), in); err != nil {
		t.Fatalf("ResolveDestinationFolder() error = %v", err)
	}
	if got := len(drive.resolveRequests()); got != 2 {
		t.Fatalf("ResolveFolder calls = %d, want 2 (expired memo re-resolves)", got)
	}
}

// TestClipRenderDestinationFolderResolver_MemoIsPerKey pins that the memo is
// keyed by root + sanitized subfolder name: different names never share a
// resolved leaf folder.
func TestClipRenderDestinationFolderResolver_MemoIsPerKey(t *testing.T) {
	drive := &fakeDeliveryPublisher{resolveOut: "leaf-folder"}
	r, err := NewClipRenderDestinationFolderResolver(drive, zap.NewNop())
	if err != nil {
		t.Fatalf("NewClipRenderDestinationFolderResolver() error = %v", err)
	}

	for _, name := range []string{"Some Script", "Other Script"} {
		if _, err := r.ResolveDestinationFolder(context.Background(), cliprender.DestinationFolderResolveInput{
			RootFolderID:  "root-folder-xyz",
			SubfolderName: name,
		}); err != nil {
			t.Fatalf("ResolveDestinationFolder(%q) error = %v", name, err)
		}
	}
	if got := len(drive.resolveRequests()); got != 2 {
		t.Fatalf("ResolveFolder calls = %d, want 2 (distinct subfolder names resolve independently)", got)
	}
}

// TestClipRenderDestinationFolderResolver_ErrorsAreNotCached pins the
// fail-closed retry contract: a failed resolution is never memoized, so the
// next call retries the Drive round-trip (and only a success is cached).
func TestClipRenderDestinationFolderResolver_ErrorsAreNotCached(t *testing.T) {
	drive := &fakeDeliveryPublisher{resolveErr: errors.New("drive unavailable")}
	r, err := NewClipRenderDestinationFolderResolver(drive, zap.NewNop())
	if err != nil {
		t.Fatalf("NewClipRenderDestinationFolderResolver() error = %v", err)
	}

	in := cliprender.DestinationFolderResolveInput{RootFolderID: "root-folder-xyz", SubfolderName: "Some Script"}
	if _, err := r.ResolveDestinationFolder(context.Background(), in); err == nil {
		t.Fatal("resolution failure must propagate fail-closed")
	}
	drive.mu.Lock()
	drive.resolveErr = nil
	drive.resolveOut = "leaf-folder-recovered"
	drive.mu.Unlock()

	folderID, err := r.ResolveDestinationFolder(context.Background(), in)
	if err != nil {
		t.Fatalf("ResolveDestinationFolder() retry error = %v", err)
	}
	if folderID != "leaf-folder-recovered" {
		t.Fatalf("folder ID = %q, want leaf-folder-recovered", folderID)
	}
	if got := len(drive.resolveRequests()); got != 2 {
		t.Fatalf("ResolveFolder calls = %d, want 2 (errors are never memoized)", got)
	}
}
