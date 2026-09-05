package images

import (
	"context"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"go.uber.org/zap"
)

func TestGenerationService_GenerateSmartImage_FailsClosedWhenPrimaryFails(t *testing.T) {
	store := testImageService(t)
	store.cfg = &config.Config{Drive: config.DriveConfig{ImagesRootFolder: "images-root"}}
	store.publisher = &fakePublisher{fileID: "drive-file-fallback", link: "https://drive.google.com/file/d/drive-file-fallback/view"}
	svc := NewGenerationService(nil, nil, zap.NewNop(), store)
	got, err := svc.GenerateSmartImage(context.Background(), "smoke", "smoke fixture", "realistic", []string{"smoke fixture"}, []string{"smoke"}, 512, 512, "", false)
	if err == nil {
		t.Fatal("expected GenerateSmartImage to fail closed when the primary provider fails")
	}
	if got != nil {
		t.Fatalf("GenerateSmartImage returned asset %+v, want nil on fail-closed path", got)
	}
}
