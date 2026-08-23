// Package usecase — PR C8 (July 2026) lock: extra interface{} removed
// from ImageGenService.SearchAndDownload. Three properties pinned.
package usecase

import (
	"context"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// testImageGenSvc satisfies ImageGenService via fixed 4-string signature.
// Compile = assertion: drift back to 5-arg `extra interface{}` breaks build.
type testImageGenSvc struct{}

func (*testImageGenSvc) SearchAndDownload(_ context.Context, _, _, _, _ string) (*asset.ImageAsset, error) {
	return &asset.ImageAsset{SourceURL: "test://stub"}, nil
}

func (*testImageGenSvc) GenerateSceneImage(_ context.Context, _, _, _ string, _, _ []string, _, _ int, _ string, _ bool) (*asset.ImageAsset, error) {
	return nil, nil
}

var _ ImageGenService = (*testImageGenSvc)(nil)

func TestC8_SignatureLocked(t *testing.T) {
	t.Parallel()
	svc := &testImageGenSvc{}
	out, err := svc.SearchAndDownload(context.Background(), "scene", "text", "alt", "en")
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if out == nil || out.SourceURL != "test://stub" {
		t.Fatalf("want SourceURL=test://stub, got %+v", out)
	}
}
