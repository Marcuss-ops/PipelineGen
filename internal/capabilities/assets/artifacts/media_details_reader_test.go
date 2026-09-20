package artifacts

import (
	"context"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// TestClipsRegistryGetMedia_FailsClosedWithoutMediaDetailsReader pins MEDIA-SSOT
// P2-9 step 2 at the registry boundary: with no PostgreSQL media committer and
// no media details reader wired, GetMedia must surface a typed error rather
// than report a missing asset for a catalog it cannot read.
func TestClipsRegistryGetMedia_FailsClosedWithoutMediaDetailsReader(t *testing.T) {
	registry := NewClipsRegistryWithLogger(nil, nil, nil, nil)
	rec, err := registry.GetMedia(context.Background(), "clip-1")
	if err == nil {
		t.Fatal("expected a typed fail-closed error when no media details reader is wired")
	}
	if rec != nil {
		t.Fatalf("record = %+v, want nil alongside the error", rec)
	}
}

// TestClipsRegistry_SatisfiesAssetDetailsReaderPort pins that the registry
// consumes the consumer-owned narrow interface, so the composition root chooses
// the media engine instead of this capability naming the legacy SQLite service.
func TestClipsRegistry_SatisfiesAssetDetailsReaderPort(t *testing.T) {
	var _ AssetDetailsReader = (*stubArtifactsDetailsReader)(nil)
	if registry := NewClipsRegistryWithLogger(&stubArtifactsDetailsReader{}, nil, nil, nil); registry == nil {
		t.Fatal("expected a registry for a wired details reader")
	}
}

type stubArtifactsDetailsReader struct{}

func (s *stubArtifactsDetailsReader) Get(context.Context, string) (*asset.Details, error) {
	return nil, nil
}
