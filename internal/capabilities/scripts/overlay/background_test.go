package overlay

import (
	"context"
	"errors"
	"testing"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func TestResolveBackgroundCopiesColorInput(t *testing.T) {
	color := []float64{0.1, 0.2, 0.3, 1}
	got, err := ResolveBackground(context.Background(), nil, &scriptpkg.OverlayBackgroundSpec{Kind: "color", Color: color})
	if err != nil {
		t.Fatal(err)
	}
	color[0] = 9
	if got.Color[0] != 0.1 {
		t.Fatalf("result aliases request color slice: %v", got.Color)
	}
}

func TestResolveBackgroundAcceptsVerifiedVisualWithoutResolver(t *testing.T) {
	input := &scriptpkg.OverlayBackgroundSpec{Kind: "image", AssetID: "asset-1", URL: "https://assets.example/a.jpg", SHA256: "digest"}
	got, err := ResolveBackground(context.Background(), nil, input)
	if err != nil {
		t.Fatal(err)
	}
	if got == input || got.AssetID != input.AssetID || got.URL != input.URL || got.SHA256 != input.SHA256 {
		t.Fatalf("verified background was not preserved in a copy: %#v", got)
	}
}

func TestResolveBackgroundFailsClosedWithoutResolver(t *testing.T) {
	_, err := ResolveBackground(context.Background(), nil, &scriptpkg.OverlayBackgroundSpec{Kind: "video", AssetID: "asset-1"})
	if err == nil {
		t.Fatal("expected missing resolver to fail closed")
	}
}

func TestResolveBackgroundPropagatesResolverError(t *testing.T) {
	wantErr := errors.New("asset lookup unavailable")
	resolver := func(context.Context, string) (string, string, string, string, string, error) {
		return "", "", "", "", "", wantErr
	}
	_, err := ResolveBackground(context.Background(), resolver, &scriptpkg.OverlayBackgroundSpec{Kind: "video", AssetID: "asset-1"})
	if !errors.Is(err, wantErr) {
		t.Fatalf("ResolveBackground() error = %v, want wrapped %v", err, wantErr)
	}
}

func TestResolveBackgroundRequiresVerifiedHash(t *testing.T) {
	resolver := func(context.Context, string) (string, string, string, string, string, error) {
		return "asset-1", "/cache/a.mp4", "", "", "video/mp4", nil
	}
	_, err := ResolveBackground(context.Background(), resolver, &scriptpkg.OverlayBackgroundSpec{Kind: "video", AssetID: "asset-1"})
	if err == nil {
		t.Fatal("expected resolver result without sha256 to fail closed")
	}
}
