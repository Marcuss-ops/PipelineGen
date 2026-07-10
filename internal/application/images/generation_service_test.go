package images

import (
	"context"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"go.uber.org/zap"
)

func TestGenerationService_GenerateSmartImage_FallsBackWhenPrimaryFails(t *testing.T) {
	store := testImageService(t)
	store.cfg = &config.Config{Drive: config.DriveConfig{ImagesRootFolder: "images-root"}}
	store.publisher = &fakePublisher{
		fileID: "drive-file-fallback",
		link:   "https://drive.google.com/file/d/drive-file-fallback/view",
	}

	svc := &GenerationService{
		storage: store,
		log:     zap.NewNop(),
	}

	got, err := svc.GenerateSmartImage(context.Background(),
		"smoke", "smoke fixture", "realistic",
		[]string{"smoke fixture"},
		[]string{"smoke"},
		512, 512, "", false,
	)
	if err != nil {
		t.Fatalf("GenerateSmartImage returned error: %v", err)
	}
	if got == nil {
		t.Fatal("GenerateSmartImage returned nil asset")
	}
	if got.SourceURL != "https://drive.google.com/file/d/drive-file-fallback/view" {
		t.Fatalf("SourceURL = %q, want drive link", got.SourceURL)
	}
	if got.Provider != "google-slides" {
		t.Fatalf("Provider = %q, want google-slides", got.Provider)
	}
}
