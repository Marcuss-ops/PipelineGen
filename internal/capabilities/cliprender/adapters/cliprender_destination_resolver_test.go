package adapters

import (
	"context"
	"sync"
	"testing"

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
	defer f.mu.Unlock()
	f.resolved = append(f.resolved, req)
	if f.resolveErr != nil {
		return "", f.resolveErr
	}
	return f.resolveOut, nil
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
