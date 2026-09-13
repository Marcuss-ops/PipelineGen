package ingest

import (
	"context"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// TestClipStoreGet_FailsClosedWithoutMediaDetailsReader pins MEDIA-SSOT P2-9
// step 2 at the ingest boundary. Before this pass the clip store read the
// operational SQLite mirror unconditionally, so it had no PostgreSQL hydration
// path at all: a clip committed by the canonical committer looked absent, and a
// stale pre-cutover row could be staged instead. Reporting "not found" for an
// unreadable catalog is a successful no-op, so a media-plane-closed boot must
// fail closed — the caller would otherwise stage and publish a clip it never
// verified.
func TestClipStoreGet_FailsClosedWithoutMediaDetailsReader(t *testing.T) {
	adapter := NewClipStoreAdapter(nil, nil, nil, nil, nil, nil)
	rec, err := adapter.Get(context.Background(), "clip-1")
	if err == nil {
		t.Fatal("expected a typed fail-closed error when no media details reader is wired")
	}
	if rec != nil {
		t.Fatalf("record = %+v, want nil alongside the error", rec)
	}
}

// TestClipStore_SatisfiesAssetDetailsReaderPort pins that the store consumes a
// consumer-owned narrow interface rather than the concrete legacy
// *detail.Service, so the composition root — not this capability — chooses the
// media engine. A stub with the right method set is accepted directly.
func TestClipStore_SatisfiesAssetDetailsReaderPort(t *testing.T) {
	var _ AssetDetailsReader = (*stubDetailsReader)(nil)
	if adapter := NewClipStoreAdapter(nil, nil, &stubDetailsReader{}, nil, nil, nil); adapter == nil {
		t.Fatal("expected an adapter for a wired details reader")
	}
}

type stubDetailsReader struct{}

func (s *stubDetailsReader) Get(context.Context, string) (*asset.Details, error) { return nil, nil }
