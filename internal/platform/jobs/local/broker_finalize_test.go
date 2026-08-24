// Package local — broker_finalize_test.go: unit tests for the overlay
// parent-video folder resolution seam (RenderingGen overlay → /video/.../overlay/).
package local

import (
	"context"
	"errors"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/finalization"
)

// stubArtifactFolderResolver is a deterministic ArtifactFolderResolver stub.
type stubArtifactFolderResolver struct {
	folderID string
	err      error
	calls    int
	gotID    string
}

func (s *stubArtifactFolderResolver) ResolveArtifactFolder(_ context.Context, parentVideoID string) (string, error) {
	s.calls++
	s.gotID = parentVideoID
	return s.folderID, s.err
}

var _ finalization.ArtifactFolderResolver = (*stubArtifactFolderResolver)(nil)

func TestResolveOverlayParentFolder_ResolvesWhenWired(t *testing.T) {
	stub := &stubArtifactFolderResolver{folderID: "video-folder-847"}
	folderID, ok, err := resolveOverlayParentFolder(context.Background(), map[string]any{"video_id": "video-847"}, stub)
	if err != nil {
		t.Fatalf("resolveOverlayParentFolder: %v", err)
	}
	if !ok || folderID != "video-folder-847" {
		t.Fatalf("resolved = (%q, %v), want (video-folder-847, true)", folderID, ok)
	}
	if stub.gotID != "video-847" {
		t.Fatalf("resolver received video_id %q, want video-847", stub.gotID)
	}
}

func TestResolveOverlayParentFolder_NoOpWithoutResolver(t *testing.T) {
	if _, ok, err := resolveOverlayParentFolder(context.Background(), map[string]any{"video_id": "video-847"}, nil); err != nil || ok {
		t.Fatalf("nil resolver should be a no-op, got ok=%v err=%v", ok, err)
	}
}

func TestResolveOverlayParentFolder_NoOpWithoutVideoID(t *testing.T) {
	stub := &stubArtifactFolderResolver{folderID: "video-folder-847"}
	for _, meta := range []map[string]any{nil, {}, {"video_id": "  "}} {
		if _, ok, err := resolveOverlayParentFolder(context.Background(), meta, stub); err != nil || ok {
			t.Fatalf("meta %#v should be a no-op, got ok=%v err=%v", meta, ok, err)
		}
	}
	if stub.calls != 0 {
		t.Fatalf("resolver called %d times, want 0 (no video_id present)", stub.calls)
	}
}

func TestResolveOverlayParentFolder_EmptyFolderIsNotResolved(t *testing.T) {
	stub := &stubArtifactFolderResolver{folderID: ""}
	if _, ok, err := resolveOverlayParentFolder(context.Background(), map[string]any{"video_id": "video-847"}, stub); err != nil || ok {
		t.Fatalf("empty folder should mean not-resolved, got ok=%v err=%v", ok, err)
	}
}

func TestResolveOverlayParentFolder_PropagatesResolverError(t *testing.T) {
	want := errors.New("folder lookup failed")
	stub := &stubArtifactFolderResolver{err: want}
	if _, _, err := resolveOverlayParentFolder(context.Background(), map[string]any{"video_id": "video-847"}, stub); !errors.Is(err, want) {
		t.Fatalf("expected resolver error to propagate, got %v", err)
	}
}
