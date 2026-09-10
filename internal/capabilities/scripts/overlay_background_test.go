package scriptgeneration

import (
	"context"
	"testing"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

type testOverlayBackgroundSource struct {
	asset OverlayBackgroundAsset
	seen  string
}

func (s *testOverlayBackgroundSource) ResolveOverlayBackground(_ context.Context, id string) (OverlayBackgroundAsset, error) {
	s.seen = id
	return s.asset, nil
}

func TestResolveOverlayBackgroundEnrichesAssetIDOnlyPayload(t *testing.T) {
	source := &testOverlayBackgroundSource{asset: OverlayBackgroundAsset{
		AssetID: "classic1", LocalPath: "/cache/classic1.mp4", SHA256: "abc", MediaType: "video/mp4",
	}}
	runner := &Runner{overlayBackgroundSource: source}
	got, err := runner.resolveOverlayBackground(context.Background(), &scriptpkg.OverlayBackgroundSpec{Kind: "video", AssetID: "classic1", Fit: "cover", Loop: true})
	if err != nil {
		t.Fatal(err)
	}
	if source.seen != "classic1" {
		t.Fatalf("resolver saw %q, want classic1", source.seen)
	}
	if got.AssetID != "classic1" || got.LocalPath != "/cache/classic1.mp4" || got.SHA256 != "abc" || got.MediaType != "video/mp4" {
		t.Fatalf("resolved background = %+v", got)
	}
}

func TestResolveOverlayBackgroundColorNeedsNoAssetResolver(t *testing.T) {
	runner := &Runner{}
	got, err := runner.resolveOverlayBackground(context.Background(), &scriptpkg.OverlayBackgroundSpec{Kind: "color", Color: []float64{0, 0, 0, 1}})
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.Kind != "color" || len(got.Color) != 4 {
		t.Fatalf("color background was changed: %+v", got)
	}
}

func TestResolveOverlayBackgroundFailsClosedWithoutIdentity(t *testing.T) {
	_, err := (&Runner{}).resolveOverlayBackground(context.Background(), &scriptpkg.OverlayBackgroundSpec{Kind: "video"})
	if err == nil {
		t.Fatal("expected missing visual background identity to fail closed")
	}
}
